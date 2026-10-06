//go:build !darwin

package main

// The menu bar app is macOS only.

const appName = "TailmuxBar.app"

func syncApplicationsApp(bool) (string, bool, error) { return "", false, nil }
