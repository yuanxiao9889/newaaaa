//go:build !windows

package service

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockReferenceDirectory(directory string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(directory, ".resumable.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrReferenceUnavailable
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, ErrReferenceBusy
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		file.Close()
	}, nil
}

func syncReferenceDirectoryPlatform(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func replaceReferenceFile(source, destination string) error { return os.Rename(source, destination) }
