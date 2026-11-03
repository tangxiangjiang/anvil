// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build !windows

package anvil

import (
	"errors"
	"os"
	"syscall"
)

type worktreeLifecycleFileLock struct {
	file *os.File
}

func lockWorktreeLifecycleFile(path string) (*worktreeLifecycleFileLock, error) {
	fd, err := syscall.Open(
		path,
		syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("open lifecycle lock handle")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errWorktreeLifecycleLockBusy
		}
		return nil, err
	}
	return &worktreeLifecycleFileLock{file: file}, nil
}

func (lock *worktreeLifecycleFileLock) write(data []byte) error {
	if err := lock.file.Truncate(0); err != nil {
		return err
	}
	if _, err := lock.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := lock.file.Write(data); err != nil {
		return err
	}
	return lock.file.Sync()
}

func (lock *worktreeLifecycleFileLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
