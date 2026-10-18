package main

import (
	"fmt"
	"os"
	"syscall"
)

// Lock is an advisory file lock (flock) that guards against concurrent
// on/off/switch, which could otherwise corrupt state or double-start servers.
type Lock struct {
	f *os.File
}

func acquireLock(p Paths) (*Lock, error) {
	f, err := os.OpenFile(p.LockFile, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	// Non-blocking: fail fast with a clear message rather than hang.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another aimode operation is in progress (lock: %s)", p.LockFile)
	}
	return &Lock{f: f}, nil
}

func (l *Lock) release() {
	if l == nil || l.f == nil {
		return
	}
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
