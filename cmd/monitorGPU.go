package cmd

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
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
	// "math/rand"
)

const (
	// meanGpuPeriod               = 10 * time.Second
	// maxGpuPeriod                = 25 * time.Second
	gpuPeriod                   = 10 * time.Second
	nvmlFeatureEnabled          = 1
	nvmlComputeExclusiveProcess = 3
)

func (w *worker) monitorGPU(ctx context.Context, client *http.Client, u url.URL, authToken string) {
	dStr := strconv.Itoa(int(w.device))

	dev, err := gonvml.DeviceHandleByIndex(w.device)
	if err != nil {
		log.Printf("Device %s: DeviceHandleByIndex() error: %v", dStr, err)
		panic(err)
	}

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

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(&initD); err != nil {
		log.Printf("Monitor error: encoding json: %v", err)
		return
	}

	req, err := http.NewRequest(m, u.String(), &body)
	if err != nil {
		log.Printf("Monitor error: creating request: %v", err)
		return
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
	req = req.WithContext(ctx)

	operation := func() error {
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer check.Err(resp.Body.Close)

		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			return fmt.Errorf("server response: %s", b)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
		func(err error, t time.Duration) {
			log.Printf("Monitor error: %v", err)
			log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
		}); err != nil {
		log.Printf("Monitor error: %v", err)
		return
	}

	// monitor
	for {
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

		gpuUtilization, err := dev.AverageGPUUtilization(gpuPeriod)
		if err != nil {
			log.Printf("Device %s: UtilizationRates() error: %v", dStr, err)
		}
		d.AvgGPUUtilization = gpuUtilization

		powerUsage, err := dev.AveragePowerUsage(gpuPeriod)
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

		log.Printf("Device %s: temperature: %v", dStr, d.Temperature)

		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(&d); err != nil {
			log.Printf("Monitor error: encoding json: %v", err)
			return
		}

		req, err := http.NewRequest(m, u.String(), &body)
		if err != nil {
			log.Printf("Monitor error: creating request: %v", err)
			return
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
		if w.busy {
			req.URL.Query().Set("jID", w.jID)
		}
		req = req.WithContext(ctx)

		operation := func() error {
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode != http.StatusOK {
				b, _ := ioutil.ReadAll(resp.Body)
				return fmt.Errorf("server response: %s", b)
			}

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5), ctx),
			func(err error, t time.Duration) {
				log.Printf("GPU monitor error: %v", err)
				log.Printf("Trying again in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("GPU monitor error: %v", err)
			return
		}

		// stochGpuPeriod := rand.ExpFloat64() * meanGpuPeriod
		// log.Printf("Stochastic gpu period: %v", stochGpuPeriod)
		select {
		case <-ctx.Done():
			return
		case <-time.After(gpuPeriod):
			// case <-time.After(stochGpuPeriod):
			// case <-time.After(maxGpuPeriod):
		}
	}
}
