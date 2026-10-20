// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

// errLockBusy is the platform's "someone else holds this" signal.
var errLockBusy = errors.New("lock is held by another handle")

// lockFile is one OS-held lock.
type lockFile struct {
	f *os.File
}

// lockExclusive opens (creating if needed) the lock file and takes an
// exclusive flock on it without blocking.
//
// flock is released by the kernel when the last descriptor for the open file
// closes, including on a crash, so a window that died does not leave a lock
// nobody can take.
func lockExclusive(path string) (*lockFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
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
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
