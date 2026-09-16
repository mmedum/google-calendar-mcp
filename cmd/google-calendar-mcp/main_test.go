package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/internal/config"
)

// isolate points the profile state at a temporary directory. Every test
// here needs it: a test that writes to the real config directory deletes
// a maintainer's credentials, which is exactly how a sibling lost its
// refresh token five times in one session.
func isolate(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GCAL_CONFIG_DIR", dir)
	return func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_REFRESH_TOKEN":
			return ""
		}
		return ""
	}
}

func TestVersionFlag(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"--version"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "google-calendar-mcp") {
		t.Fatalf("--version printed %q", out.String())
	}
}

func TestDumpSchemasWritesJSONToStdout(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"--dump-schemas"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("run: %v", err)
	}
	var dump struct {
		Server string `json:"server"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &dump); err != nil {
		t.Fatalf("--dump-schemas did not write JSON: %v\n%s", err, out.String())
	}
	if len(dump.Tools) < 6 {
		t.Fatalf("dumped %d tools", len(dump.Tools))
	}
}

// TestDumpSchemasNeedsNoCredentials: the dump is how the schema diff
// runs in CI, where nobody is logged in.
func TestDumpSchemasNeedsNoCredentials(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"--dump-schemas"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("--dump-schemas requires credentials, so CI cannot diff the surface: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("no output")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	err := run([]string{"frobnicate"}, strings.NewReader(""), &out, &errOut, env)
	if err == nil {
		t.Fatal("an unknown subcommand was accepted")
	}
	// It has to name the ones that exist, or the message is useless.
	for _, want := range []string{"login", "logout", "status", "doctor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not name %q: %v", want, err)
		}
	}
}

func TestStatusWithoutLogin(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"status"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("status: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "signed in:     no") {
		t.Fatalf("status does not say the account is not signed in:\n%s", s)
	}
	if !strings.Contains(s, "login") {
		t.Fatalf("status does not say what to do:\n%s", s)
	}
	// And it must report the flags that change what the server does.
	for _, want := range []string{"read-only", "sharing tools", "destructive", "event budget"} {
		if !strings.Contains(s, want) {
			t.Fatalf("status does not report %q:\n%s", want, s)
		}
	}
}

func TestStatusReportsTheConfiguredFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GCAL_CONFIG_DIR", dir)
	env := func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_READONLY":
			return "true"
		case "GCAL_SHARING":
			return "off"
		}
		return ""
	}
	var out, errOut bytes.Buffer
	if err := run([]string{"status"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "read-only:     true") {
		t.Fatalf("status does not reflect GCAL_READONLY:\n%s", s)
	}
	if !strings.Contains(s, "sharing tools: false") {
		t.Fatalf("status does not reflect GCAL_SHARING=off:\n%s", s)
	}
}

func TestInvalidConfigurationFailsBeforeAnnouncingItself(t *testing.T) {
	dir := t.TempDir()
	env := func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_LOG_LEVEL":
			return "chatty"
		}
		return ""
	}
	var out, errOut bytes.Buffer
	err := run(nil, strings.NewReader(""), &out, &errOut, env)
	if err == nil {
		t.Fatal("a bad log level started the server")
	}
	if out.Len() != 0 {
		t.Fatalf("a failed start wrote to stdout, which carries only JSON-RPC frames: %q", out.String())
	}
}

// TestIsDisconnect is the SDK trap the standard's §11 names:
// errors.Is(err, io.EOF) does NOT catch the end of a stdio session,
// because the SDK reports it as a JSON-RPC code with the EOF only in the
// message text. Matching the text would break on an SDK reword; matching
// the code does not.
func TestIsDisconnect(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"closed connection", &jsonrpc.Error{Code: -32004, Message: "server is closing: EOF"}, true},
		{"closed session", &jsonrpc.Error{Code: -32003, Message: "session closed"}, true},
		{"plain EOF", io.EOF, true},
		{"a real protocol error", &jsonrpc.Error{Code: -32600, Message: "invalid request"}, false},
		{"an ordinary error", errors.New("something broke"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDisconnect(c.err); got != c.want {
				t.Fatalf("isDisconnect(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}

	// The specific mistake, stated as a test: the SDK's disconnect does
	// not satisfy errors.Is(err, io.EOF), so a server matching on that
	// exits non-zero and every host logs a crash.
	sdkDisconnect := &jsonrpc.Error{Code: -32004, Message: "server is closing: EOF"}
	if errors.Is(sdkDisconnect, io.EOF) {
		t.Skip("the SDK now wraps io.EOF; the code match is still correct but this note is stale")
	}
	if !isDisconnect(sdkDisconnect) {
		t.Fatal("the SDK's disconnect is not recognised, so an ordinary disconnect exits non-zero")
	}
}

func TestAccessForFollowsTheConfiguration(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	// doctor with no credentials must still run and report, not panic.
	if err := run([]string{"doctor"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "FAIL") {
		t.Fatalf("doctor found no problem on a machine with no credentials:\n%s", s)
	}
	if !strings.Contains(s, "problem") {
		t.Fatalf("doctor does not summarise:\n%s", s)
	}
}

func TestLoginRejectsAMissingOrWrongClientSecret(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer

	// No client JSON at all: the message must say what to create.
	err := run([]string{"login"}, strings.NewReader(""), &out, &errOut, env)
	if err == nil {
		t.Fatal("login proceeded with no client secret")
	}
	if !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("the error does not say what kind of client to create: %v", err)
	}
}

func TestLoginRejectsAWebClient(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GCAL_CONFIG_DIR", dir)
	path := dir + "/web.json"
	if err := os.WriteFile(path, []byte(`{"web":{"client_id":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "GCAL_CONFIG_DIR" {
			return dir
		}
		return ""
	}
	var out, errOut bytes.Buffer
	err := run([]string{"login", "--client-secret", path}, strings.NewReader(""), &out, &errOut, env)
	if err == nil {
		t.Fatal("a Web application client was accepted")
	}
	if !strings.Contains(err.Error(), "Desktop app client instead") {
		t.Fatalf("the error does not name the common mistake: %v", err)
	}
}

func TestLogoutWithNothingStored(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	if err := run([]string{"logout"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("logout on a machine with no token should succeed: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "No stored token") {
		t.Fatalf("logout does not say there was nothing to revoke:\n%s", s)
	}
	if !strings.Contains(s, "Signed out") {
		t.Fatalf("logout does not confirm:\n%s", s)
	}
}

func TestSubcommandsRejectBadConfiguration(t *testing.T) {
	dir := t.TempDir()
	env := func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_PROFILE":
			return "Not A Profile"
		}
		return ""
	}
	for _, sub := range []string{"status", "doctor", "login", "logout"} {
		t.Run(sub, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := run([]string{sub}, strings.NewReader(""), &out, &errOut, env); err == nil {
				t.Fatalf("%s accepted an invalid profile name", sub)
			}
		})
	}
}

func TestAccessForMirrorsTheConfiguration(t *testing.T) {
	for _, c := range []struct {
		readOnly, sharing bool
	}{{false, true}, {true, true}, {false, false}, {true, false}} {
		got := accessFor(config.Config{ReadOnly: c.readOnly, Sharing: c.sharing})
		if got.ReadOnly != c.readOnly || got.Sharing != c.sharing {
			t.Fatalf("accessFor(%+v) = %+v", c, got)
		}
	}
}

func TestOAuthClientAlwaysHasATimeout(t *testing.T) {
	// Without one, a token endpoint that accepts the connection and
	// never answers hangs the first tool call for as long as the process
	// runs.
	hc := oauthClient(context.Background(), staticTokenSource{}, 0)
	if hc.Timeout <= 0 {
		t.Fatal("oauthClient returned a client with no timeout")
	}
	hc = oauthClient(context.Background(), staticTokenSource{}, 5*time.Second)
	if hc.Timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", hc.Timeout)
	}
}

type staticTokenSource struct{}

func (staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "at"}, nil
}

func TestServeSubcommandIsTheDefault(t *testing.T) {
	env := isolate(t)
	var out, errOut bytes.Buffer
	// `serve --version` takes the same path as `--version`.
	if err := run([]string{"serve", "--version"}, strings.NewReader(""), &out, &errOut, env); err != nil {
		t.Fatalf("serve --version: %v", err)
	}
	if !strings.Contains(out.String(), "google-calendar-mcp") {
		t.Fatalf("serve --version printed %q", out.String())
	}
}

// serveSession drives run()'s server branch in process.
//
// The input is a pipe held open until the replies have arrived, not a
// strings.Reader. With a reader that is already at EOF the session ends
// before it processes anything queued behind it, and the test reads zero
// frames — which looks like a broken server and is really a broken test.
// The same shape caught this in the smoke gate.
func serveSession(t *testing.T, env func(string) string, messages []string, wantFrames int) (frames []map[string]any, stderr string) {
	t.Helper()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errOut bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- run(nil, inR, outW, &errOut, env) }()

	read := make(chan []map[string]any, 1)
	go func() {
		var got []map[string]any
		dec := json.NewDecoder(outR)
		for len(got) < wantFrames {
			var frame map[string]any
			if err := dec.Decode(&frame); err != nil {
				break
			}
			got = append(got, frame)
		}
		read <- got
	}()

	for _, m := range messages {
		if _, err := io.WriteString(inW, m+"\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	select {
	case frames = <-read:
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not answer within 20s")
	}

	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serving must end cleanly when the client disconnects: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run did not return after the input closed")
	}
	_ = outW.Close()
	return frames, errOut.String()
}

var initMessages = []string{
	`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
	`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
}

// TestServeOverTheInjectedStreams drives the whole serve path in
// process: initialize, list the tools, call one without credentials.
// It is the only test that exercises run()'s server branch.
func TestServeOverTheInjectedStreams(t *testing.T) {
	env := isolate(t)
	msgs := append(append([]string{}, initMessages...),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_calendars","arguments":{}}}`)

	frames, stderr := serveSession(t, env, msgs, 3)
	if len(frames) < 3 {
		t.Fatalf("read %d frames, expected 3", len(frames))
	}
	for _, f := range frames {
		if f["jsonrpc"] != "2.0" {
			t.Fatalf("a frame is not JSON-RPC 2.0: %v", f)
		}
	}

	// The warning about missing credentials goes to stderr, never to
	// stdout, which carries only frames.
	if !strings.Contains(stderr, "without credentials") {
		t.Fatalf("no warning about the missing credentials:\n%s", stderr)
	}
}

func TestServeRespectsReadOnly(t *testing.T) {
	dir := t.TempDir()
	env := func(k string) string {
		switch k {
		case "GCAL_CONFIG_DIR":
			return dir
		case "GCAL_READONLY":
			return "true"
		}
		return ""
	}
	frames, _ := serveSession(t, env, initMessages, 2)
	if len(frames) < 2 {
		t.Fatalf("read %d frames", len(frames))
	}
	raw, err := json.Marshal(frames[len(frames)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "list_calendars") {
		t.Fatalf("read-only mode dropped the read tools: %s", raw)
	}
}
