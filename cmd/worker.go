package cmd

import (
	"github.com/satori/go.uuid"
)

type worker struct {
	device  uint
	uuid    uuid.UUID
	busy    bool
	bidRate float64
	miner   *cryptoMiner
}
