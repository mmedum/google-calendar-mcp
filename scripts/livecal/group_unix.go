//go:build live && unix

package main

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own, so the Ctrl-C a
// terminal sends to the driver's group does not reach it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
