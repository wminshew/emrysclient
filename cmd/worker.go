package cmd

import (
	"github.com/satori/go.uuid"
)

type worker struct {
	device uint
	uuid   uuid.UUID
	busy   bool
}
