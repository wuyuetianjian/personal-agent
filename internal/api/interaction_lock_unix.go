//go:build !windows

package api

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockRuntime(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockRuntime(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func secureRuntimeDirectory(path string) error { return os.Chmod(path, 0700) }
