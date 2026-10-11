//go:build !linux

package main

import "syscall"

// coderSysProcAttr — SysProcAttr для кодера (не-Linux): только Setpgid
// (Pdeathsig недоступен).
func coderSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
