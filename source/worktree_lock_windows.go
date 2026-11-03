// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

//go:build windows

package anvil

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type worktreeLifecycleFileLock struct {
	file *os.File
}

func lockWorktreeLifecycleFile(path string) (*worktreeLifecycleFileLock, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("lifecycle lock is not a regular file")
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("open lifecycle lock handle")
	}
	overlapped := new(windows.Overlapped)
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
			errors.Is(err, windows.ERROR_IO_PENDING) {
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
	err := windows.UnlockFileEx(
		windows.Handle(lock.file.Fd()), 0, 1, 0, new(windows.Overlapped),
	)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
