package cmd

import (
	"log"
	"os"
	"time"
)

func monitorInterrupts(stop <-chan os.Signal, cancelFunc func()) {
	defer func() {
		log.Printf("Canceling...\n")
		cancelFunc()
	}()
	<-stop
	if busy {
		log.Printf("Cancellation request received: please press ctrl-c again to quit.\n")
		log.Printf("Warning! You are currently working on a job and will be penalized for quitting. Otherwise, this program will terminate upon completion.\n")
		terminate = true
		<-stop
	} else if bidsOut > 0 {
		busy = true // stop miner from submitting new bids
		log.Printf("Cancellation request received: please press ctrl-c again to quit.\n")
		log.Printf("Warning! You have %d outstanding bids to wind down before quitting. If you force quit now and one of your bids wins, you will be penalized for quitting. Otherwise, in a few seconds, this program will terminate.\n", bidsOut)
		for bidsOut > 0 {
			select {
			case <-stop:
				return
			case <-time.After(1 * time.Second):
			}
		}
	}
}
