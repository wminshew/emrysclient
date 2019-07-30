package worker

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"github.com/satori/go.uuid"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/gonvml"
	"log"
	"math"
	"os/exec"
	"strconv"
	"time"
)

const (
	nvmlFeatureEnabled          = 1
	nvmlComputeExclusiveProcess = 3
	minTemp                     = 40
	targetTemp                  = 65
	maxTemp                     = 75
	minFan                      = 25
	incFan                      = 5
	maxFan                      = 85
)

// InitGPUMonitoring initializes nvml
func (w *Worker) InitGPUMonitoring() error {
	var err error
	w.gonvmlDevice, err = gonvml.DeviceHandleByIndex(w.Device)
	if err != nil {
		return errors.Wrapf(err, "device %d: getting handle by index", w.Device)
	}

	// initialize
	if err := w.gonvmlDevice.SetPersistenceMode(nvmlFeatureEnabled); err != nil {
		return errors.Wrapf(err, "device %d: setting persistence mode", w.Device)
	}

	if err := w.gonvmlDevice.SetComputeMode(nvmlComputeExclusiveProcess); err != nil {
		return errors.Wrapf(err, "device %d: setting compute mode", w.Device)
	}

	name, err := w.gonvmlDevice.Name()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting name", w.Device)
	}
	var ok bool
	if w.Snapshot.Name, ok = job.ValidateGPU(name); !ok {
		return errors.Wrapf(err, "device %d: this device is not currently supported by the emrys network. "+
			"Please check https://docs.emrys.io/docs/suppliers/valid_gpus and contact support@emrys.io if you think there has been a mistake.", w.Device)
	}

	w.Snapshot.MinorNumber, err = w.gonvmlDevice.MinorNumber()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting minor number", w.Device)
	}

	uuidStr, err := w.gonvmlDevice.UUID()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting uuid", w.Device)
	}
	w.Snapshot.ID, err = uuid.FromString(uuidStr[4:]) // strip off "gpu-" prepend
	if err != nil {
		return errors.Wrapf(err, "device %d: converting uuid.uuid", w.Device)
	}

	w.Snapshot.Brand, err = w.gonvmlDevice.Brand()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting brand", w.Device)
	}

	w.Snapshot.DefaultPowerLimit, err = w.gonvmlDevice.DefaultPowerLimit()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting default power limit", w.Device)
	}

	w.Snapshot.GrMaxClock, err = w.gonvmlDevice.GrMaxClock()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting max gr clock", w.Device)
	}

	w.Snapshot.SMMaxClock, err = w.gonvmlDevice.SMMaxClock()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting max sm clock", w.Device)
	}

	w.Snapshot.MemMaxClock, err = w.gonvmlDevice.MemMaxClock()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting max mem clock", w.Device)
	}

	w.Snapshot.PcieMaxGeneration, err = w.gonvmlDevice.PcieMaxGeneration()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting max pcie gen", w.Device)
	}

	w.Snapshot.PcieMaxWidth, err = w.gonvmlDevice.PcieMaxWidth()
	if err != nil {
		return errors.Wrapf(err, "device %d: getting max pcie width", w.Device)
	}

	return nil
}

// GetGPUStats returns the worker's gpu stats
func (w *Worker) GetGPUStats(ctx context.Context, period time.Duration) (*job.DeviceSnapshot, error) {
	snapshot := &job.DeviceSnapshot{}

	persistenceMode, err := w.gonvmlDevice.PersistenceMode()
	if err != nil {
		return &job.DeviceSnapshot{}, errors.Wrapf(err, "device %d: getting persistence mode", w.Device)
	}
	if persistenceMode != nvmlFeatureEnabled {
		return &job.DeviceSnapshot{}, errors.New("persistence mode disabled")
	}

	computeMode, err := w.gonvmlDevice.ComputeMode()
	if err != nil {
		return &job.DeviceSnapshot{}, errors.Wrapf(err, "device %d: getting compute mode", w.Device)
	}
	if computeMode != nvmlComputeExclusiveProcess {
		return &job.DeviceSnapshot{}, errors.New("exclusive compute mode disabled")
	}

	powerLimit, err := w.gonvmlDevice.PowerLimit()
	if err != nil {
		return &job.DeviceSnapshot{}, errors.Wrapf(err, "device %d: getting power limit", w.Device)
	}
	if powerLimit != w.Snapshot.DefaultPowerLimit {
		return &job.DeviceSnapshot{}, errors.New("power limit not set to default")
	}

	operation := func() error {
		var err error
		snapshot.TimeStamp = time.Now().Unix()
		snapshot.MinorNumber = w.Snapshot.MinorNumber
		snapshot.ID = w.Snapshot.ID
		snapshot.Name = w.Snapshot.Name
		snapshot.Brand = w.Snapshot.Brand
		snapshot.DefaultPowerLimit = w.Snapshot.DefaultPowerLimit

		snapshot.TotalMemory, snapshot.UsedMemory, err = w.gonvmlDevice.MemoryInfo()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting memory info", w.Device)
		}

		snapshot.GrMaxClock = w.Snapshot.GrMaxClock
		snapshot.SMMaxClock = w.Snapshot.SMMaxClock
		snapshot.MemMaxClock = w.Snapshot.MemMaxClock
		snapshot.PcieMaxGeneration = w.Snapshot.PcieMaxGeneration
		snapshot.PcieMaxWidth = w.Snapshot.PcieMaxWidth

		snapshot.PerformanceState, err = w.gonvmlDevice.PerformanceState()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting performance state", w.Device)
		}

		samplingPeriod := time.Duration(math.Max(float64(period), float64(1000*time.Millisecond)))
		snapshot.AvgGPUUtilization, err = w.gonvmlDevice.AverageGPUUtilization(samplingPeriod)
		if err != nil {
			return errors.Wrapf(err, "device %d: getting average utilization rate", w.Device)
		}

		snapshot.AvgPowerUsage, err = w.gonvmlDevice.AveragePowerUsage(samplingPeriod)
		if err != nil {
			return errors.Wrapf(err, "device %d: getting average power usage", w.Device)
		}

		snapshot.GrClock, err = w.gonvmlDevice.GrClock()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting gr clock", w.Device)
		}

		snapshot.SMClock, err = w.gonvmlDevice.SMClock()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting sm clock", w.Device)
		}

		snapshot.MemClock, err = w.gonvmlDevice.MemClock()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting mem clock", w.Device)
		}

		snapshot.PcieTxThroughput, err = w.gonvmlDevice.PcieTxThroughput()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting pcie tx throughput", w.Device)
		}

		snapshot.PcieRxThroughput, err = w.gonvmlDevice.PcieRxThroughput()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting pcie rx throughput", w.Device)
		}

		snapshot.PcieGeneration, err = w.gonvmlDevice.PcieGeneration()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting pcie gen", w.Device)
		}

		snapshot.PcieWidth, err = w.gonvmlDevice.PcieWidth()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting pcie width", w.Device)
		}

		snapshot.Temperature, err = w.gonvmlDevice.Temperature()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting temperature", w.Device)
		}

		snapshot.FanSpeed, err = w.gonvmlDevice.FanSpeed()
		if err != nil {
			return errors.Wrapf(err, "device %d: getting fanspeed", w.Device)
		}

		return nil
	}
	if err := backoff.RetryNotify(operation,
		backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
		func(err error, t time.Duration) {
			log.Printf("device %d: error snapshotting gpu: %v", w.Device, err)
		}); err != nil {
		return &job.DeviceSnapshot{}, errors.Wrapf(err, "device %d: snapshotting gpu", w.Device)
	}

	return snapshot, nil
}

// UserGPULog regularly logs temperature to user; updates fan accordingly
func (w *Worker) UserGPULog(ctx context.Context, period time.Duration) {
	controlFan := true
	if err := w.updateFanControlState(ctx, 1); err != nil {
		log.Printf("Mine: device %d: error updating fan control state", w.Device)
		controlFan = false
	} else if fanControlState, err := w.getFanControlState(ctx); err != nil {
		log.Printf("Mine: device %d: error setting GPUFanControlState=1; emrys will not update your fan speed: %v", w.Device, err)
		controlFan = false
	} else if fanControlState != 1 {
		log.Printf("Mine: device %d: error setting GPUFanControlState=1; emrys will not update your fan speed: please ensure your cards don't overheat. If you would like emrys to control your fans, please visit https://docs.emrys.io/docs/suppliers/installation and follow the instructions under 'GPU cooling'. Please contact support@emrys.io if you think there is a mistake.", w.Device)
		controlFan = false
	}

	// TODO: may need to dynamically determine number of fans w/ nvidia-settings -q fans or something
	// (apparently some cards can have multiple fans (/controllers) per card)
	// nvidia-settings -q fans | sed -n 's/\s*FAN/FAN/p' | wc -l
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(period):
		}

		temp, err := w.gonvmlDevice.Temperature()
		if err != nil {
			log.Printf("Mine: device %d: error getting gpu temperature", w.Device)
		}

		fanSpeed, err := w.gonvmlDevice.FanSpeed()
		if err != nil {
			log.Printf("Mine: device %d: error getting gpu fan speed", w.Device)
		}

		log.Printf("Mine: device %d: temperature: %v; fan: %v", w.Device, temp, fanSpeed)

		if controlFan {
			fs := int(fanSpeed)
			var newFanSpeed int
			if temp > maxTemp {
				newFanSpeed = maxFan
			} else if temp > targetTemp {
				newFanSpeed = fs + incFan
				if newFanSpeed > maxFan {
					newFanSpeed = maxFan
				}
			} else if temp > minTemp {
				newFanSpeed = fs - incFan
				if newFanSpeed < minFan {
					newFanSpeed = minFan
				}
			} else {
				newFanSpeed = minFan
			}
			if err := w.updateFan(ctx, newFanSpeed); err != nil {
				log.Printf("Mine: device %d: error updating fan speed: %v", w.Device, err)
			}
		}
	}
}

func (w *Worker) updateFan(ctx context.Context, newFanSpeed int) error {
	// nvidia-settings -a '[fan:{w.Device}]/GPUTargetFanSpeed={newFanSpeed}'
	cmdStr := "nvidia-settings"
	args := append([]string{"-a"}, fmt.Sprintf("[fan:%d]/GPUTargetFanSpeed=%d", w.Device, newFanSpeed))
	cmd := exec.CommandContext(ctx, cmdStr, args...)
	return cmd.Run()
}

func (w *Worker) updateFanControlState(ctx context.Context, newFanControlState int) error {
	if newFanControlState != 0 && newFanControlState != 1 {
		return fmt.Errorf("improper new fan control state: %d", newFanControlState)
	}
	// nvidia-settings -a '[gpu:{w.Device}]/GPUFanControlState={newFanControlState}'
	cmdStr := "nvidia-settings"
	args := append([]string{"-a"}, fmt.Sprintf("[gpu:%d]/GPUFanControlState=%d", w.Device, newFanControlState))
	cmd := exec.CommandContext(ctx, cmdStr, args...)
	return cmd.Run()
}

func (w *Worker) getFanControlState(ctx context.Context) (int, error) {
	// nvidia-settings -q "[gpu:0]/GPUFanControlState" | sed -n 's/Attribute//p' - | awk '{print $NF}' | sed 's/[^0-9]*//g'
	cmdStr := "nvidia-settings -q \"[gpu:0]/GPUFanControlState\" | sed -n 's/Attribute//p' - | awk '{print $NF}' | sed 's/[^0-9]*//g'"
	cmd := exec.CommandContext(ctx, cmdStr)
	// Output runs the command and returns its standard output.
	// Any returned error will usually be of type *ExitError.
	// If c.Stderr was nil, Output populates ExitError.Stderr.
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	fanControlState, err := strconv.Atoi(string(output))
	if err != nil {
		return 0, err
	}

	return fanControlState, nil
}
