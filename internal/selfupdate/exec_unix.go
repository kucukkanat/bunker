//go:build !windows

package selfupdate

import "syscall"

func sysExec(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
