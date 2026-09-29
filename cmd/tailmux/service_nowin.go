//go:build !windows

package main

import "context"

// Only Windows has a service manager that talks to the process itself.
func isService() bool { return false }

func runAsService(run func(context.Context) error) error { return run(context.Background()) }
