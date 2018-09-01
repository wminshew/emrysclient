package cmd

import (
	"context"
	"log"
	"os"
	"os/exec"
	"syscall"
)

type cryptoMiner struct {
	command string
	startCh chan struct{}
	stopCh  chan struct{}
}

func (cm *cryptoMiner) init(ctx context.Context) {
	cm.startCh = make(chan struct{}, 1)
	cm.stopCh = make(chan struct{}, 1)

	cmdStr := "/bin/sh"
	args := append([]string{"-c"}, cm.command)
	go func() {
		for {
			cmd := exec.CommandContext(ctx, cmdStr, args...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Printf("Begin mining-command...\n")
			if err := cmd.Start(); err != nil {
				log.Printf("Error starting cryptomining process: %v\n", err)
				return
			}
			select {
			case <-ctx.Done():
			case <-cm.stopCh:
			}
			log.Printf("Stop mining-command...\n")
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP); err != nil {
				log.Printf("Error interrupting cryptomining process: %v\n", err)
				return
			}
			if err := cmd.Process.Release(); err != nil {
				log.Printf("Error releasing cryptomining process: %v\n", err)
				return
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
