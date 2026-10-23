//go:build windows

package service

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockReferenceDirectory(directory string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(directory, ".resumable.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrReferenceUnavailable
	}
	overlap := &windows.Overlapped{}
	if err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap); err != nil {
		file.Close()
		return nil, ErrReferenceBusy
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlap)
		file.Close()
	}, nil
}

func syncReferenceDirectoryPlatform(string) error { return nil }

func replaceReferenceFile(source, destination string) error {
	src, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
