package service

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shirou/gopsutil/disk"
)

func (s *ReferenceUploadStore) requiredFreeBytes(space *disk.UsageStat) uint64 {
	return max(s.config.MinFreeBytes, space.Total/100*uint64(s.config.MinFreePercent))
}

// Called under the store and directory locks. Incomplete uploads retain their
// reservations; only unleased completed references can be evicted, oldest first.
// Re-read actual free space after each removal instead of assuming file sizes
// equal reclaimed disk blocks (sparse files, open handles and snapshots differ).
func (s *ReferenceUploadStore) reclaimReferenceSpaceLocked(additional int64) error {
	reserved, _ := s.resumableUsageLocked(0)
	needed := uint64(max(0, additional) + s.reserved + reserved)
	space, err := s.diskUsage(s.config.Directory)
	if err != nil {
		return ErrReferenceUnavailable
	}
	if space.Free >= s.requiredFreeBytes(space) && space.Free-s.requiredFreeBytes(space) >= needed {
		return nil
	}
	protected, err := protectedTaskReferences(model.DB, 0)
	if err != nil {
		return ErrReferenceUnavailable
	}
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return ErrReferenceUnavailable
	}
	var candidates []os.FileInfo
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() ||
			(!strings.HasSuffix(name, ".asset") && !strings.HasSuffix(name, ".url")) {
			continue
		}
		identity := strings.TrimSuffix(strings.TrimSuffix(name, ".asset"), ".url")
		owner, id, ok := strings.Cut(identity, "-")
		user, parseErr := strconv.Atoi(owner)
		if !ok || parseErr != nil || user <= 0 || !validReferenceID(id) || protected[identity] || s.active[filepath.Join(s.config.Directory, name)] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return ErrReferenceUnavailable
		}
		if info.Mode().IsRegular() {
			candidates = append(candidates, info)
		}
	}
	slices.SortFunc(candidates, func(a, b os.FileInfo) int {
		if order := a.ModTime().Compare(b.ModTime()); order != 0 {
			return order
		}
		return strings.Compare(a.Name(), b.Name())
	})
	for _, candidate := range candidates {
		if err := os.Remove(filepath.Join(s.config.Directory, candidate.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			common.SysError("reference rolling cleanup could not remove a cached asset")
			continue
		}
		// Reclaim associated receipts immediately, never reserve their old size.
		reserved, _ = s.resumableUsageLocked(0)
		needed = uint64(max(0, additional) + s.reserved + reserved)
		space, err = s.diskUsage(s.config.Directory)
		if err != nil {
			return ErrReferenceUnavailable
		}
		if space.Free >= s.requiredFreeBytes(space) && space.Free-s.requiredFreeBytes(space) >= needed {
			return nil
		}
	}
	return ErrReferenceDiskSpace
}
