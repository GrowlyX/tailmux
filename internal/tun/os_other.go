//go:build !darwin && !linux

package tun

import "errors"

var errUnsupported = errors.New("tun mode is only supported on macOS and Linux")

const defaultTUNName = "tun"

func newOSConfig() (osConfig, error) { return nil, errUnsupported }
