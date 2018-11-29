package mine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/satori/go.uuid"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/gonvml"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"
)

const (
	meanGPUPeriod               = 10 * time.Second
	maxGPUPeriod                = 30 * time.Second
	nvmlFeatureEnabled          = 1
	nvmlComputeExclusiveProcess = 3
	minTemp                     = 40
	targetTemp                  = 65
	maxTemp                     = 75
	minFan                      = 25
	incFan                      = 5
	maxFan                      = 85
)

func (w *worker) monitorGPU(ctx context.Context, client *http.Client, u url.URL, authToken string) {
	dStr := strconv.Itoa(int(w.device))

	dev, err := gonvml.DeviceHandleByIndex(w.device)
	if err != nil {
		log.Printf("Device %s: DeviceHandleByIndex() error: %v", dStr, err)
		panic(err)
	}

	go userGPULog(ctx, dStr, dev)

	// initialize
	if err := dev.SetPersistenceMode(nvmlFeatureEnabled); err != nil {
		log.Printf("Device %s: SetPersistenceMode() error: %v", dStr, err)
		panic(err)
	}

	if err := dev.SetComputeMode(nvmlComputeExclusiveProcess); err != nil {
		log.Printf("Device %s: SetComputeMode() error: %v", dStr, err)
		panic(err)
	}

	// init snapshot
	initD := job.DeviceSnapshot{}
	initD.TimeStamp = time.Now().Unix()

	minorNumber, err := dev.MinorNumber()
	if err != nil {
		log.Printf("device %s: MinorNumber() error: %v", dStr, err)
		panic(err)
	}
	initD.MinorNumber = minorNumber

	uuidStr, err := dev.UUID()
	if err != nil {
		log.Printf("device %s: UUID() error: %v", dStr, err)
		panic(err)
	}
	initD.ID, err = uuid.FromString(uuidStr[4:]) // strip off "gpu-" prepend
	if err != nil {
		log.Printf("device %s: error converting device uuid to uuid.uuid: %v", dStr, err)
		panic(err)
	}

	name, err := dev.Name()
	if err != nil {
		log.Printf("device %s: Name() error: %v", dStr, err)
		panic(err)
	}
	initD.Name = name

	brand, err := dev.Brand()
	if err != nil {
		log.Printf("Device %s: Brand() error: %v", dStr, err)
		panic(err)
	}
	initD.Brand = brand

	defaultPowerLimit, err := dev.DefaultPowerLimit()
	if err != nil {
		log.Printf("Device %s: DefaultPowerLimit() error: %v", dStr, err)
		panic(err)
	}
	initD.DefaultPowerLimit = defaultPowerLimit

	totalMemory, _, err := dev.MemoryInfo()
	if err != nil {
		log.Printf("Device %s: MemoryInfo() error: %v", dStr, err)
		panic(err)
	}
	initD.TotalMemory = totalMemory

	grMaxClock, err := dev.GrMaxClock()
	if err != nil {
		log.Printf("Device %s: GrMaxClock() error: %v", dStr, err)
		panic(err)
	}
	initD.GrMaxClock = grMaxClock

	smMaxClock, err := dev.SMMaxClock()
	if err != nil {
		log.Printf("Device %s: SMMaxClock() error: %v", dStr, err)
		panic(err)
	}
	initD.SMMaxClock = smMaxClock

	memMaxClock, err := dev.MemMaxClock()
	if err != nil {
		log.Printf("Device %s: MemMaxClock() error: %v", dStr, err)
		panic(err)
	}
	initD.MemMaxClock = memMaxClock

	pcieMaxGeneration, err := dev.PcieMaxGeneration()
	if err != nil {
		log.Printf("Device %s: PcieGeneration() error: %v", dStr, err)
		panic(err)
	}
	initD.PcieMaxGeneration = pcieMaxGeneration

	pcieMaxWidth, err := dev.PcieMaxWidth()
	if err != nil {
		log.Printf("Device %s: PcieGeneration() error: %v", dStr, err)
		panic(err)
	}
	initD.PcieMaxWidth = pcieMaxWidth

	m := "POST"
	p := path.Join("miner", "device_snapshot")
	u.Path = p
	operation := func() error {
		body := &bytes.Buffer{}
		if err := json.NewEncoder(body).Encode(&initD); err != nil {
			return err
		}

		req, err := http.NewRequest(m, u.String(), body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
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
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("Monitor error: %v", err)
			log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Monitor error: %v", err)
		return
	}

	// monitor
	for {
		stochGPUPeriod := time.Duration(rand.ExpFloat64() * float64(meanGPUPeriod))
		// log.Printf("Device %s: period: %v", dStr, stochGPUPeriod)
		select {
		case <-ctx.Done():
			return
		case <-time.After(stochGPUPeriod):
		case <-time.After(maxGPUPeriod):
			stochGPUPeriod = maxGPUPeriod
		}
		d := job.DeviceSnapshot{}
		d.TimeStamp = time.Now().Unix()
		d.ID = initD.ID

		persistenceMode, err := dev.PersistenceMode()
		if err != nil {
			log.Printf("Device %s: PersistenceMode() error: %v", dStr, err)
			continue
		}
		if persistenceMode != nvmlFeatureEnabled {
			log.Printf("Persistence mode disabled--aborting\n")
			panic(fmt.Errorf("persistence mode disabled"))
		}

		computeMode, err := dev.ComputeMode()
		if err != nil {
			log.Printf("Device %s: ComputeMode() error: %v", dStr, err)
			continue
		}
		if computeMode != nvmlComputeExclusiveProcess {
			log.Printf("Exclusive compute mode disabled--aborting\n")
			panic(fmt.Errorf("exclusive compute mode disabled"))
		}

		powerLimit, err := dev.PowerLimit()
		if err != nil {
			log.Printf("Device %s: PowerLimit() error: %v", dStr, err)
		}
		if powerLimit != defaultPowerLimit {
			log.Printf("Power limit not set to default--aborting\n")
			panic(fmt.Errorf("power limit altered"))
		}

		performanceState, err := dev.PerformanceState()
		if err != nil {
			log.Printf("Device %s: PerformanceState() error: %v", dStr, err)
			continue
		}
		d.PerformanceState = performanceState

		gpuUtilSamplingPeriod := time.Duration(math.Max(float64(stochGPUPeriod), float64(150*time.Millisecond)))
		gpuUtilization, err := dev.AverageGPUUtilization(gpuUtilSamplingPeriod)
		if err != nil {
			log.Printf("Device %s: UtilizationRates() error: %v", dStr, err)
		}
		d.AvgGPUUtilization = gpuUtilization

		gpuPowerSamplingPeriod := time.Duration(math.Max(float64(stochGPUPeriod), float64(1000*time.Millisecond)))
		powerUsage, err := dev.AveragePowerUsage(gpuPowerSamplingPeriod)
		if err != nil {
			log.Printf("Device %s: PowerUsage() error: %v", dStr, err)
		}
		d.AvgPowerUsage = powerUsage

		_, usedMemory, err := dev.MemoryInfo()
		if err != nil {
			log.Printf("Device %s: MemoryInfo() error: %v", dStr, err)
		}
		d.UsedMemory = usedMemory

		grClock, err := dev.GrClock()
		if err != nil {
			log.Printf("Device %s: GrClock() error: %v", dStr, err)
		}
		d.GrClock = grClock

		smClock, err := dev.SMClock()
		if err != nil {
			log.Printf("Device %s: SMClock() error: %v", dStr, err)
		}
		d.SMClock = smClock

		memClock, err := dev.MemClock()
		if err != nil {
			log.Printf("Device %s: MemClock() error: %v", dStr, err)
		}
		d.MemClock = memClock

		pcieTxThroughput, err := dev.PcieTxThroughput()
		if err != nil {
			log.Printf("Device %s: PcieTxThroughput() error: %v", dStr, err)
		}
		d.PcieTxThroughput = pcieTxThroughput

		pcieRxThroughput, err := dev.PcieRxThroughput()
		if err != nil {
			log.Printf("Device %s: PcieRxThroughput() error: %v", dStr, err)
		}
		d.PcieRxThroughput = pcieRxThroughput

		pcieGen, err := dev.PcieGeneration()
		if err != nil {
			log.Printf("Device %s: PcieGeneration() error: %v", dStr, err)
		}
		d.PcieGeneration = pcieGen

		pcieWidth, err := dev.PcieWidth()
		if err != nil {
			log.Printf("Device %s: PcieGeneration() error: %v", dStr, err)
		}
		d.PcieWidth = pcieWidth

		temperature, err := dev.Temperature()
		if err != nil {
			log.Printf("Device %s: Temperature() error: %v", dStr, err)
		}
		d.Temperature = temperature

		fanSpeed, err := dev.FanSpeed()
		if err != nil {
			log.Printf("Device %s: FanSpeed() error: %v", dStr, err)
		}
		d.FanSpeed = fanSpeed

		operation := func() error {
			body := &bytes.Buffer{}
			if err := json.NewEncoder(body).Encode(&d); err != nil {
				return err
			}

			req, err := http.NewRequest(m, u.String(), body)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
			if w.busy {
				q := req.URL.Query()
				q.Set("jID", w.jID)
				req.URL.RawQuery = q.Encode()
			}
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
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxBackoffRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Device %s: error monitoring gpu: %v", dStr, err)
				log.Printf("Device %s: retrying in %s seconds\n", dStr, t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Device %s: error monitoring gpu: %v", dStr, err)
			return
		}
	}
}

// userGPULog regularly logs temperature to user; updates fan accordingly
func userGPULog(ctx context.Context, dStr string, dev gonvml.Device) {
	for {
		temperature, err := dev.Temperature()
		if err != nil {
			log.Printf("Device %s: Temperature() error: %v", dStr, err)
		}
		fanSpeed, err := dev.FanSpeed()
		if err != nil {
			log.Printf("Device %s: FanSpeed() error: %v", dStr, err)
		}

		log.Printf("Device %s: temperature: %v; fan: %v", dStr, temperature, fanSpeed)

		// TODO: add logic to set GPUFanControlState=1; maybe some of that other stuff too..?
		fs := int(fanSpeed)
		var newFanSpeed int
		if temperature > maxTemp {
			newFanSpeed = maxFan
		} else if temperature > targetTemp {
			newFanSpeed = fs + incFan
			if newFanSpeed > maxFan {
				newFanSpeed = maxFan
			}
		} else if temperature > minTemp {
			newFanSpeed = fs - incFan
			if newFanSpeed < minFan {
				newFanSpeed = minFan
			}
		} else {
			newFanSpeed = minFan
		}
		if err := updateFan(ctx, dStr, newFanSpeed); err != nil {
			log.Printf("Device %s: error updating fan speed: %v", dStr, err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(meanGPUPeriod):
		}
	}
}

func updateFan(ctx context.Context, dStr string, newFanSpeed int) error {
	// nvidia-settings -a '[fan:{dStr}]/GPUTargetFanSpeed={newFanSpeed}'
	cmdStr := "nvidia-settings"
	args := append([]string{"-a"}, fmt.Sprintf("[fan:%s]/GPUTargetFanSpeed=%d", dStr, newFanSpeed))
	cmd := exec.CommandContext(ctx, cmdStr, args...)
	// cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
	// cmd.Env = append(os.Environ(), fmt.Sprintf("DEVICE=%s", dStr))
	// if cm.command != "" {
	// 	mining = true
	// 	log.Printf("Device %s: begin mining...\n", dStr)
	// 	if err := cmd.Start(); err != nil {
	// 		log.Printf("Device %s: error starting cryptomining process: %v", dStr, err)
	// 		return
	// 	}
	// }
	// select {
	// case <-ctx.Done():
	// case <-cm.stopCh:
	// }
	// if mining {
	// 	log.Printf("Device %s: halt mining...\n", dStr)
	// 	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP); err != nil {
	// 		log.Printf("Device %s: error interrupting cryptomining process: %v", dStr, err)
	// 		return
	// 	}
	// 	if err := cmd.Process.Release(); err != nil {
	// 		log.Printf("Device %s: error releasing cryptomining process: %v", dStr, err)
	// 		return
	// 	}
	// }
}
