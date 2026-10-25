//go:build !darwin && !linux && !windows

package tun

import (
	"errors"

	"github.com/GrowlyX/tailmux/internal/mux"
)

var errUnsupported = errors.New("tun mode is only supported on macOS and Linux")

const defaultTUNName = "tun"

func newOSConfig(mux.TUNConfig) (osConfig, error) { return nil, errUnsupported }
