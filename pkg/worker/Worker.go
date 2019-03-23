package worker

import (
	"docker.io/go-docker"
	"github.com/satori/go.uuid"
	"net/http"
)

const (
	maxRetries = 10
)

// Worker represents a GPU Worker to bid on & execute jobs
type Worker struct {
	MinerID       string
	Client        *http.Client
	Docker        *docker.Client
	AuthToken     *string
	BidsOut       *int
	JobsInProcess *int
	Device        uint
	UUID          uuid.UUID
	Busy          bool
	sshKey        []byte
	notebook      bool
	Port          string
	temperature   uint
	fanSpeed      uint
	JobID         string
	BidRate       float64
	gpu           string
	RAM           uint64
	Disk          uint64
	pcie          int
	Miner         *CryptoMiner
}
