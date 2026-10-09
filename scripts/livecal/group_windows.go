//go:build live && windows

package main

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd in a process group of its own. Windows sends Ctrl-C
// to every process on the console, except one in a new group, where it
// is turned off.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
