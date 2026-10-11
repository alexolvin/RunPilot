//go:build linux

package main

import "syscall"

// coderSysProcAttr — SysProcAttr для кодера (Linux): Setpgid + Pdeathsig=SIGKILL
// (кодер убивается при смерти exec-процесса).
func coderSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
