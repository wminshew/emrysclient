package cmd

import (
	"context"
	"github.com/mindprince/gonvml"
	"github.com/wminshew/emrys/pkg/check"
	"log"
	"time"
)

func monitorGPU(ctx context.Context) {
	if err := gonvml.Initialize(); err != nil {
		log.Printf("Couldn't initialize gonvml: %v. Make sure NVML is in the shared library search path.", err)
		panic(err)
	}
	defer check.Err(gonvml.Shutdown)

	if driverVersion, err := gonvml.SystemDriverVersion(); err != nil {
		log.Printf("Nvidia driver error: %v", err)
	} else {
		log.Printf("Nvidia driver: %v", driverVersion)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
	}
}
