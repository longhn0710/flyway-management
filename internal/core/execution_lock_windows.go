//go:build windows

package core

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func executionLock(path string) (func() error, error) {
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("execution lock must be regular")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	var overlapped windows.Overlapped
	if e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); e != nil {
		f.Close()
		return nil, fmt.Errorf("another execution manager owns this data directory: %w", e)
	}
	return func() error { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped); return f.Close() }, nil
}
