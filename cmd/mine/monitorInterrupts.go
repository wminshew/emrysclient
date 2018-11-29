package mine

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
	terminate = true
	if jobsInProcess > 0 {
		log.Printf("Cancellation request received: please press ctrl-c again to force quit.\n")
		if jobsInProcess == 1 {
			log.Printf("Warning! You are currently working on a job and will be penalized for quitting. Otherwise, this program will terminate upon completion.\n")
		} else {
			log.Printf("Warning! You are currently working on %d jobs and will be penalized for quitting. Otherwise, this program will terminate upon completion.\n", jobsInProcess)
		}
		for jobsInProcess > 0 || bidsOut > 0 {
			select {
			case <-stop:
				return
			case <-time.After(1 * time.Second):
			}
		}
	} else if bidsOut > 0 {
		log.Printf("Cancellation request received: please press ctrl-c again to quit.\n")
		if bidsOut == 1 {
			log.Printf("Warning! You have 1 outstanding bid to wind down before quitting. If you force quit now and your bid wins, you will be penalized for quitting. Otherwise, in a few seconds, this program will terminate.\n")
		} else {
			log.Printf("Warning! You have %d outstanding bids to wind down before quitting. If you force quit now and one of your bids wins, you will be penalized for quitting. Otherwise, in a few seconds, this program will terminate.\n", bidsOut)
		}
		for bidsOut > 0 {
			select {
			case <-stop:
				return
			case <-time.After(1 * time.Second):
			}
		}
	}
}
