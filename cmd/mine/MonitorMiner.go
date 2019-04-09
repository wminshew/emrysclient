package mine

import (
	"bytes"
	"context"
	"docker.io/go-docker"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"github.com/satori/go.uuid"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/emrysclient/pkg/worker"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"path"
	"time"
)

// MonitorMiner monitors the miner's system and all its workers
func MonitorMiner(ctx context.Context, client *http.Client, dClient *docker.Client, authToken *string, workers []*worker.Worker, cancelFunc func(), u url.URL) {
	defer func() {
		select {
		case <-ctx.Done():
		default:
			cancelFunc()
		}
	}()

	stochPeriod := meanPeriod
	p := path.Join("miner", "stats")
	u.Path = p
	for {
		operation := func() error {
			stats := job.MinerStats{}

			// get cpu, mem, disk system stats
			cpuInfo, err := cpu.Info()
			if err != nil {
				return errors.Wrap(err, "getting cpu info")
			}
			stats.CPUInfo = cpuInfo

			cpuTimes, err := cpu.Times(true)
			if err != nil {
				return errors.Wrap(err, "getting cpu times")
			}
			stats.CPUTimes = cpuTimes

			memStats, err := mem.VirtualMemory()
			if err != nil {
				return errors.Wrap(err, "getting memory stats")
			}
			stats.Mem = memStats

			diskUsage, err := disk.Usage("/")
			if err != nil {
				return errors.Wrap(err, "getting disk usage: system")
			}
			stats.Disk = diskUsage

			for _, w := range workers {
				wStats := &job.WorkerStats{}
				if w.JobID != "" {
					if wStats.JobID, err = uuid.FromString(w.JobID); err != nil {
						return errors.Wrapf(err, "device %d: getting uuid from job ID", w.Device)
					}
				}

				// get gpu stats
				if wStats.GPUStats, err = w.GetGPUStats(ctx, stochPeriod); err != nil {
					return errors.Wrapf(err, "device %d: getting gpu stats", w.Device)
				}

				// get docker container stats [cpu, mem, disk]
				if w.ContainerID != "" {
					containerStats, err := w.Docker.ContainerStats(ctx, w.ContainerID, false)
					if err != nil {
						return errors.Wrapf(err, "device %d: getting container stats", w.Device)
					}
					defer check.Err(containerStats.Body.Close)

					if err := json.NewDecoder(containerStats.Body).Decode(&wStats.DockerStats); err != nil {
						return errors.Wrapf(err, "device %d: decoding container stats", w.Device)
					}

					// size of image & container
					wStats.DockerDisk = &job.DockerDisk{}
					dockerDisk, err := w.Docker.DiskUsage(ctx)
					if err != nil {
						return errors.Wrapf(err, "device %d: getting docker disk usage", w.Device)
					}
					for _, container := range dockerDisk.Containers {
						if container.ID == w.ContainerID {
							wStats.DockerDisk.SizeRw = container.SizeRw
							wStats.DockerDisk.SizeRootFs = container.SizeRootFs // should be image size
						}
					}

					// size of data folder
					dataDirUsage, err := disk.Usage(w.DataDir)
					if err != nil {
						return errors.Wrap(err, "getting disk usage: data folder")
					}
					wStats.DockerDisk.SizeDataDir = dataDirUsage.Total

					// size of output folder
					outputDirUsage, err := disk.Usage(w.OutputDir)
					if err != nil {
						return errors.Wrap(err, "getting disk usage: output folder")
					}
					wStats.DockerDisk.SizeOutputDir = outputDirUsage.Total

					// TODO: should be uint64, but keeping check consistent with server
					if int64(w.Disk) < (wStats.DockerDisk.SizeRw + wStats.DockerDisk.SizeRootFs +
						int64(wStats.DockerDisk.SizeDataDir) + int64(wStats.DockerDisk.SizeOutputDir)) {
						w.DiskQuotaExceeded = true
					}
				}

				stats.WorkerStats = append(stats.WorkerStats, wStats)
			}

			body := &bytes.Buffer{}
			if err := json.NewEncoder(body).Encode(&stats); err != nil {
				return err
			}

			req, err := http.NewRequest(http.MethodPost, u.String(), body)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *authToken))
			req = req.WithContext(ctx)

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode == http.StatusBadGateway {
				return fmt.Errorf("server: temporary error")
			} else if resp.StatusCode >= 300 {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server: %v", string(b))
			}

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Mine: error monitoring system: %v", err)
				log.Printf("Mine: retrying in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Mine: error monitoring system: %v", err)
			return
		}

		stochPeriod = time.Duration(rand.ExpFloat64() * float64(meanPeriod))
		select {
		case <-ctx.Done():
			return
		case <-time.After(stochPeriod):
		case <-time.After(maxPeriod):
			stochPeriod = maxPeriod
		}
	}
}
