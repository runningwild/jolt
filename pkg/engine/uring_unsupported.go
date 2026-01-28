//go:build !linux

package engine

import (
	"fmt"
)

type UringEngine struct {
}

func NewUring() *UringEngine {
	return &UringEngine{}
}

func (e *UringEngine) NumNodes() int { return 1 }

func (e *UringEngine) Run(params Params) (*Result, error) {
	return nil, fmt.Errorf("uring engine is only supported on Linux")
}
