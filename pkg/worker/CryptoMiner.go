package worker

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// CryptoMiner allows workers to mine cryptocurrencies inbetween emrys jobs
type CryptoMiner struct {
	Command string
	Device  uint
	startCh chan struct{}
	stopCh  chan struct{}
}

// Init initializes the cryptominer
func (cm *CryptoMiner) Init(ctx context.Context) {
	dStr := strconv.Itoa(int(cm.Device))
	cm.startCh = make(chan struct{}, 1)
	cm.stopCh = make(chan struct{}, 1)

	cmdStr := "/bin/sh"
	go func() {
		for {
			mining := false
			args := append([]string{"-c"}, cm.Command) // miner may wish to hot-reload config with new mining command
			cmd := exec.CommandContext(ctx, cmdStr, args...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = append(os.Environ(), fmt.Sprintf("DEVICE=%s", dStr))
			if cm.Command != "" {
				mining = true
				log.Printf("Device %s: begin mining...\n", dStr)
				if err := cmd.Start(); err != nil {
					log.Printf("Device %s: error starting cryptomining process: %v", dStr, err)
					return
				}
			}
			select {
			case <-ctx.Done():
			case <-cm.stopCh:
			}
			if mining {
				log.Printf("Device %s: halt mining...\n", dStr)
				if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
					log.Printf("Device %s: error killing cryptomining process: %v", dStr, err)
					return
				}
				if err := cmd.Process.Release(); err != nil {
					log.Printf("Device %s: error releasing cryptomining process: %v", dStr, err)
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-cm.startCh:
			}
		}
	}()
}

// Start starts the cryptominer
func (cm *CryptoMiner) Start() {
	cm.startCh <- struct{}{}
}

// Stop stops the cryptominer
func (cm *CryptoMiner) Stop() {
	cm.stopCh <- struct{}{}
}
