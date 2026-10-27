//go:build live && unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Ctrl-C, SIGTERM and a closed terminal each stop the steps rather than
// the driver, so the cleanup runs. A signal left out of the set kills
// this test's process, which fails it.
func TestEveryStopSignalEndsTheStepsNotTheDriver(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		ctx, stop := stopOnSignal(context.Background())
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Errorf("%v did not stop the steps", sig)
		}
		stop()
	}
}

// The hangup that stops a run can also close the pipe its transcript
// goes through. A print into that pipe fails; it does not kill the
// driver before its cleanup.
func TestAClosedTranscriptPipeDoesNotEndTheDriver(t *testing.T) {
	if os.Getenv("LIVECAL_CLOSED_PIPE") == "1" {
		stopOnSignal(context.Background())
		if _, err := os.Stderr.Write([]byte("into a closed pipe\n")); err == nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAClosedTranscriptPipeDoesNotEndTheDriver$")
	cmd.Env = append(os.Environ(), "LIVECAL_CLOSED_PIPE=1")
	cmd.Stderr = w
	err = cmd.Run()
	_ = w.Close()
	if err != nil {
		t.Fatalf("the driver did not survive a print into a closed pipe: %v", err)
	}
}
