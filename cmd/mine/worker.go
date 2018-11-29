package mine

import (
	"github.com/satori/go.uuid"
)

type worker struct {
	device  uint
	uuid    uuid.UUID
	busy    bool
	jID     string
	bidRate float64
	miner   *cryptoMiner
}
