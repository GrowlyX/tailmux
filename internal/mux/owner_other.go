//go:build !unix

package mux

func fileOwner(string) (int, int, bool) { return 0, 0, false }
