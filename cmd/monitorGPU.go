package cmd

import (
	"context"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/gonvml"
	"log"
	"time"
)

// GPUSnapshot holds data collected about the mining GPU
type GPUSnapshot struct {
	TimeStamp         int64
	MinorNumber       uint
	UUID              string
	Name              string
	Brand             uint
	ComputeMode       uint
	PerformanceState  uint
	AvgGPUUtilization uint
	AvgPowerUsage     uint
	TotalMemory       uint64
	UsedMemory        uint64
	GrClock           uint
	SMClock           uint
	MemClock          uint
	GrMaxClock        uint
	SMMaxClock        uint
	MemMaxClock       uint
	PcieGeneration    uint
	PcieWidth         uint
	PcieMaxGeneration uint
	PcieMaxWidth      uint
	Temperature       uint
	FanSpeed          uint
}

var gpuPeriod = 10 * time.Second

func monitorGPU(ctx context.Context) {
	if err := gonvml.Initialize(); err != nil {
		log.Printf("Couldn't initialize gonvml: %v. Make sure NVML is in the shared library search path.", err)
		panic(err)
	}
	defer check.Err(gonvml.Shutdown)

	driverVersion, err := gonvml.SystemDriverVersion()
	if err != nil {
		log.Printf("Error finding nvidia driver: %v", err)
		return
	}
	log.Printf("Nvidia driver: %v", driverVersion)

	numDevices, err := gonvml.DeviceCount()
	if err != nil {
		log.Printf("Error counting nvidia devices: %v\n", err)
		return
	}

	for {
		for i := 0; i < int(numDevices); i++ {
			log.Printf("Collecting GPU snapshot for device %d...\n", i)
			g := GPUSnapshot{}
			g.TimeStamp = time.Now().Unix()

			dev, err := gonvml.DeviceHandleByIndex(uint(i))
			if err != nil {
				log.Printf("DeviceHandleByIndex(%d) error: %v", i, err)
				continue
			}

			minorNumber, err := dev.MinorNumber()
			if err != nil {
				log.Printf("MinorNumber() error: %v", err)
				continue
			}
			g.MinorNumber = minorNumber

			uuid, err := dev.UUID()
			if err != nil {
				log.Printf("UUID() error: %v", err)
				continue
			}
			g.UUID = uuid

			name, err := dev.Name()
			if err != nil {
				log.Printf("Name() error: %v", err)
				continue
			}
			g.Name = name

			brand, err := dev.Brand()
			if err != nil {
				log.Printf("Brand() error: %v", err)
				continue
			}
			g.Brand = brand

			computeMode, err := dev.ComputeMode()
			if err != nil {
				log.Printf("ComputeMode() error: %v", err)
				continue
			}
			g.ComputeMode = computeMode

			performanceState, err := dev.PerformanceState()
			if err != nil {
				log.Printf("PerformanceState() error: %v", err)
				continue
			}
			g.PerformanceState = performanceState

			gpuUtilization, err := dev.AverageGPUUtilization(gpuPeriod)
			if err != nil {
				log.Printf("UtilizationRates() error: %v", err)
			}
			g.AvgGPUUtilization = gpuUtilization

			powerUsage, err := dev.AveragePowerUsage(gpuPeriod)
			if err != nil {
				log.Printf("PowerUsage() error: %v", err)
			}
			g.AvgPowerUsage = powerUsage

			totalMemory, usedMemory, err := dev.MemoryInfo()
			if err != nil {
				log.Printf("MemoryInfo() error: %v", err)
			}
			g.TotalMemory = totalMemory
			g.UsedMemory = usedMemory

			grClock, err := dev.GrClock()
			if err != nil {
				log.Printf("GrClock() error: %v", err)
			}
			g.GrClock = grClock

			smClock, err := dev.SMClock()
			if err != nil {
				log.Printf("SMClock() error: %v", err)
			}
			g.SMClock = smClock

			memClock, err := dev.MemClock()
			if err != nil {
				log.Printf("MemClock() error: %v", err)
			}
			g.MemClock = memClock

			grMaxClock, err := dev.GrMaxClock()
			if err != nil {
				log.Printf("GrMaxClock() error: %v", err)
			}
			g.GrMaxClock = grMaxClock

			smMaxClock, err := dev.SMMaxClock()
			if err != nil {
				log.Printf("SMMaxClock() error: %v", err)
			}
			g.SMMaxClock = smMaxClock

			memMaxClock, err := dev.MemMaxClock()
			if err != nil {
				log.Printf("MemMaxClock() error: %v", err)
			}
			g.MemMaxClock = memMaxClock

			// pcieGen, err := dev.PcieGeneration()
			// if err != nil {
			// 	log.Printf("PcieGeneration() error: %v", err)
			// }
			// g.PcieGeneration = pcieGen

			// pcieWidth, err := dev.PcieWidth()
			// if err != nil {
			// 	log.Printf("PcieGeneration() error: %v", err)
			// }
			// g.PcieWidth = pcieWidth

			// pcieMaxGeneration, err := dev.PcieMaxGeneration()
			// if err != nil {
			// 	log.Printf("PcieGeneration() error: %v", err)
			// }
			// g.PcieMaxGeneration = pcieMaxGeneration
			//
			// pcieMaxWidth, err := dev.PcieMaxWidth()
			// if err != nil {
			// 	log.Printf("PcieGeneration() error: %v", err)
			// }
			// g.PcieMaxWidth = pcieMaxWidth

			temperature, err := dev.Temperature()
			if err != nil {
				log.Printf("Temperature() error: %v", err)
			}
			g.Temperature = temperature

			fanSpeed, err := dev.FanSpeed()
			if err != nil {
				log.Printf("FanSpeed() error: %v", err)
			}
			g.FanSpeed = fanSpeed

			log.Printf("GPU Snapshot: %+v", g)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(gpuPeriod):
		}
	}
}
