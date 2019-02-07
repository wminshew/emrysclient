package mine

import (
	"docker.io/go-docker"
	"github.com/satori/go.uuid"
	"net/http"
)

type worker struct {
	mID         string
	client      *http.Client
	dClient     *docker.Client
	authToken   *string
	device      uint
	uuid        uuid.UUID
	busy        bool
	sshKey      string
	notebook    bool
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
