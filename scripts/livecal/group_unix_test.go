//go:build live && unix

package main

import (
	"os/exec"
	"syscall"
	"testing"
)

// The server starts outside the driver's process group, so the Ctrl-C a
// terminal sends to that group stops the driver's steps and leaves the
// server to answer the call in flight.
func TestTheServerIsOutsideTheDriversProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	group, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if group == syscall.Getpgrp() {
		t.Fatalf("the child is in the driver's process group %d", group)
	}
}
