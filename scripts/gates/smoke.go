package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// smoke drives the built binary over stdio without credentials, in TWO
// runs, because the two things worth proving need opposite treatment of
// stdin.
//
// The first run holds stdin open until the replies arrive. It checks
// that stdout carried JSON-RPC frames and nothing else, that the tools
// are there with descriptions, and that a tool call with no credentials
// comes back as a tool result saying so — rather than as a dead server.
//
// The second closes stdin the instant the last message is written and
// checks only the exit code. That is the shape that catches the SDK
// trap: a closed session arrives as JSON-RPC -32004 with the EOF only in
// the message text, so errors.Is(err, io.EOF) does not match it and the
// process exits non-zero on an ordinary disconnect, which every host
// logs as a crash. A smoke test that writes, sleeps and then closes
// never sees it — with nothing in flight the exit really is 0 — so the
// abrupt close is the whole point of the second run.
//
// Running both in one pass is what the first version of this gate did,
// and it proved neither: stdin closed before the session processed
// anything, so it read zero frames and reported the tools missing.
func smoke(bin string) error {
	if err := smokeSession(bin); err != nil {
		return err
	}
	return smokeAbruptClose(bin)
}

// smokeEnv is a server with nowhere to find a credential, which is how a
// first-run user's machine looks. Discovery must work there.
func smokeEnv(cmd *exec.Cmd) {
	cmd.Env = append(cmd.Environ(),
		"GCAL_CONFIG_DIR=no-such-directory-for-the-smoke-test",
		"GCAL_REFRESH_TOKEN=",
		"GCAL_LOG_LEVEL=error",
	)
}

var smokeMessages = []string{
	`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}`,
	`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	// Without credentials. This must answer, not die.
	`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_calendars","arguments":{}}}`,
}

// smokeReplies is how many of those expect an answer. The notification
// does not.
const smokeReplies = 3

type smokeFrame struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func startSmoke(bin string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, *strings.Builder, error) {
	cmd := exec.Command(bin)
	smokeEnv(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("start %s: %w", bin, err)
	}
	return cmd, stdin, stdout, stderr, nil
}

// smokeSession keeps stdin open until every reply has arrived.
func smokeSession(bin string) error {
	cmd, stdin, stdout, stderr, err := startSmoke(bin)
	if err != nil {
		return err
	}
	defer func() { _ = cmd.Process.Kill() }()

	for _, m := range smokeMessages {
		if _, err := fmt.Fprintf(stdin, "%s\n", m); err != nil {
			return fmt.Errorf("write to the server: %w (stderr: %s)", err, truncate(stderr.String(), 400))
		}
	}

	type outcome struct {
		tools    []string
		frames   int
		authSaid bool
		err      error
	}
	done := make(chan outcome, 1)

	go func() {
		var o outcome
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var f smokeFrame
			if err := json.Unmarshal([]byte(line), &f); err != nil {
				o.err = fmt.Errorf("stdout carried something that is not a JSON-RPC frame.\n"+
					"This is the protocol, not a preference: one stray print corrupts the stream "+
					"and the client silently stops working.\nline: %q", truncate(line, 200))
				done <- o
				return
			}
			o.frames++

			switch f.ID {
			case 2:
				var res struct {
					Tools []struct {
						Name        string `json:"name"`
						Description string `json:"description"`
					} `json:"tools"`
				}
				if err := json.Unmarshal(f.Result, &res); err != nil {
					o.err = err
					done <- o
					return
				}
				for _, t := range res.Tools {
					if strings.TrimSpace(t.Description) == "" {
						o.err = fmt.Errorf("tool %q has no description; a tool the model cannot find does not exist", t.Name)
						done <- o
						return
					}
					if strings.Contains(t.Name, ".") {
						o.err = fmt.Errorf("tool %q has a dot in its name", t.Name)
						done <- o
						return
					}
					o.tools = append(o.tools, t.Name)
				}
			case 3:
				// A call with no credentials must come back as a RESULT
				// that says so. The server must not refuse to start and
				// must not die here: a client launches it and lists
				// tools long before anybody logs in.
				body := string(f.Result) + string(f.Error)
				if strings.Contains(body, "[auth]") || strings.Contains(strings.ToLower(body), "login") {
					o.authSaid = true
				}
			}
			if o.frames >= smokeReplies {
				done <- o
				return
			}
		}
		o.err = sc.Err()
		done <- o
	}()

	var o outcome
	select {
	case o = <-done:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("the server did not answer within 30s (stderr: %s)", truncate(stderr.String(), 400))
	}
	_ = stdin.Close()

	if o.err != nil {
		return o.err
	}
	if o.frames < smokeReplies {
		return fmt.Errorf("read %d frames, expected %d (stderr: %s)",
			o.frames, smokeReplies, truncate(stderr.String(), 400))
	}
	if len(o.tools) < 5 {
		return fmt.Errorf("tools/list returned %d tools; phase 0 registers six read tools", len(o.tools))
	}
	if !o.authSaid {
		return errors.New("a tool call with no credentials did not come back saying so; " +
			"discovery and a first call have to work before login, because the client starts this server, " +
			"lists its tools, and only then does anybody sign in")
	}
	fmt.Printf("  session: %d frames, %d tools, unauthenticated call answered\n", o.frames, len(o.tools))
	return nil
}

// smokeAbruptClose closes stdin with the last message still in flight.
func smokeAbruptClose(bin string) error {
	cmd, stdin, stdout, stderr, err := startSmoke(bin)
	if err != nil {
		return err
	}
	// Drain stdout so the process is never blocked writing into a full
	// pipe, which would look like the hang this is testing for.
	go func() { _, _ = io.Copy(io.Discard, stdout) }()

	for _, m := range smokeMessages {
		if _, err := fmt.Fprintf(stdin, "%s\n", m); err != nil {
			break
		}
	}
	// Immediately. See the doc comment: with nothing in flight the exit
	// really is 0 and the bug hides.
	if err := stdin.Close(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("the binary exited non-zero on an ordinary disconnect: %w\n"+
				"The SDK reports a closed connection as JSON-RPC -32004 with the EOF only in the message "+
				"text, so errors.Is(err, io.EOF) does not catch it and every host logs this as a crash.\n"+
				"stderr: %s", err, truncate(stderr.String(), 500))
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("the binary did not exit after stdin closed")
	}
	fmt.Println("  abrupt close: clean exit")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
