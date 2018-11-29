package mine

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

type cryptoMiner struct {
	command string
	device  uint
	startCh chan struct{}
	stopCh  chan struct{}
}

func (cm *cryptoMiner) init(ctx context.Context) {
	dStr := strconv.Itoa(int(cm.device))
	cm.startCh = make(chan struct{}, 1)
	cm.stopCh = make(chan struct{}, 1)

	cmdStr := "/bin/sh"
	go func() {
		for {
			mining := false
			args := append([]string{"-c"}, cm.command) // miner may wish to hot-reload config with new mining command
			cmd := exec.CommandContext(ctx, cmdStr, args...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = append(os.Environ(), fmt.Sprintf("DEVICE=%s", dStr))
			if cm.command != "" {
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
				if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP); err != nil {
					log.Printf("Device %s: error interrupting cryptomining process: %v", dStr, err)
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

func (cm *cryptoMiner) start() {
	cm.startCh <- struct{}{}
}

func (cm *cryptoMiner) stop() {
	cm.stopCh <- struct{}{}
}
