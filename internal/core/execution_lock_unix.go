//go:build !windows

package core

import (
	"fmt"
	"os"
	"syscall"
)

func executionLock(path string) (func() error, error) {
	if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("execution lock must be regular")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another execution manager owns this data directory: %w", e)
	}
	return func() error { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); return f.Close() }, nil
}
