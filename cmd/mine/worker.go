package mine

import (
	"github.com/satori/go.uuid"
)

type worker struct {
	device      uint
	uuid        uuid.UUID
	busy        bool
	temperature uint
	fanSpeed    uint
	jID         string
	bidRate     float64
	gpu         string
	ram         uint64
	disk        uint64
	pcie        int
	miner       *cryptoMiner
}
