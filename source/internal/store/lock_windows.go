// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// errLockBusy is the platform's "someone else holds this" signal.
var errLockBusy = errors.New("lock is held by another handle")

// lockFile is one OS-held lock.
type lockFile struct {
	f *os.File
}

// lockExclusive opens (creating if needed) the lock file and takes an
// exclusive lock on it without blocking.
//
// LockFileEx is the Windows primitive: the lock is owned by the open handle, so
// the kernel drops it when the process dies. Nothing has to reap a stale lock,
// which matters because the window that crashed is exactly the case this guards.
func lockExclusive(path string) (*lockFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	if err := windows.LockFileEx(h, flags, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, errLockBusy
		}
		return nil, err
	}
	return &lockFile{f: f}, nil
}

func (l *lockFile) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	h := windows.Handle(l.f.Fd())
	ol := new(windows.Overlapped)
	err := windows.UnlockFileEx(h, 0, 1, 0, ol)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
