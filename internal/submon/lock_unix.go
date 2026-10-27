//go:build !windows

package submon

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("monitor data directory already has an owner")
	}
	return f, nil
}
