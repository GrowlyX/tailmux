//go:build !unix

package update

import "errors"

func ownerUID(string) (int, error) { return 0, errors.New("not supported on this OS") }

func Reexec(string) error { return errors.New("restart it by hand") }
