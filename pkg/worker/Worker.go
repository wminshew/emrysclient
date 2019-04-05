package worker

import (
	"docker.io/go-docker"
	"github.com/wminshew/emrys/pkg/job"
	"github.com/wminshew/gonvml"
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
	gonvmlDevice  gonvml.Device
	Snapshot      *job.DeviceSnapshot
	Busy          bool
	sshKey        []byte
	notebook      bool
	Port          string
	JobID         string
	ContainerID   string
	DataDir       string
	OutputDir     string
	BidRate       float64
	RAM           uint64
	Disk          uint64
	Miner         *CryptoMiner
}
