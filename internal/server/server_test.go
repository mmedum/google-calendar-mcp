package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/server"
	"github.com/mmedum/google-calendar-mcp/internal/service"
)

func deps(t *testing.T) server.Deps {
	t.Helper()
	fake := caltest.Seed()
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base
	cfg := config.Config{MaxEvents: 250, MaxCalendars: 25, Concurrency: 4, Sharing: true}
	return server.Deps{Service: service.New(api, cfg), Config: cfg, Version: "test"}
}

func TestDumpSchemas(t *testing.T) {
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), &buf, deps(t)); err != nil {
		t.Fatalf("DumpSchemas: %v", err)
	}
	var dump server.SchemaDump
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatalf("the dump is not valid JSON: %v", err)
	}
	if dump.Server != server.Name {
		t.Fatalf("server = %q", dump.Server)
	}
	if dump.SDKVersion == "" {
		t.Fatal("the dump records no SDK version; an SDK upgrade would look like a surface change")
	}
	if len(dump.Tools) < 6 {
		t.Fatalf("the dump lists %d tools", len(dump.Tools))
	}
	// Sorted, so a diff between two dumps is about the surface rather
	// than about map ordering.
	for i := 1; i < len(dump.Tools); i++ {
		if dump.Tools[i-1].Name > dump.Tools[i].Name {
			t.Fatalf("the dump is not sorted: %s before %s", dump.Tools[i-1].Name, dump.Tools[i].Name)
		}
	}
	for _, tool := range dump.Tools {
		if tool.Description == "" {
			t.Fatalf("%s has no description in the dump", tool.Name)
		}
		if len(tool.InputSchema) == 0 {
			t.Fatalf("%s has no input schema in the dump", tool.Name)
		}
	}
}

// TestDumpSchemasUsesTheFullSurface: the dump has to show every tool,
// whatever this process's flags say, or the schema diff is blind to
// anything behind a gate.
func TestDumpSchemasUsesTheFullSurface(t *testing.T) {
	d := deps(t)
	d.Config.ReadOnly = true
	d.Config.Sharing = false

	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), &buf, d); err != nil {
		t.Fatal(err)
	}
	var dump server.SchemaDump
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatal(err)
	}
	if len(dump.Tools) < 6 {
		t.Fatalf("a read-only, sharing-off process dumped %d tools; the dump must show the full surface",
			len(dump.Tools))
	}
}

func TestInstructionsNameTheTraps(t *testing.T) {
	d := deps(t)
	srv := server.New(d)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	// The instructions are the model's orientation. The three defects
	// this server exists to avoid have to be in them, or a model will
	// walk into the same ones.
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), &buf, d); err != nil {
		t.Fatal(err)
	}
	// Instructions are not in the dump, so assert against the constant
	// through a session instead.
	for _, want := range []string{"all-day", "expand", "check_availability"} {
		if !strings.Contains(strings.ToLower(instructionsOf(t, d)), want) {
			t.Fatalf("the server instructions do not mention %q", want)
		}
	}
}

func instructionsOf(t *testing.T, d server.Deps) string {
	t.Helper()
	// The SDK exposes instructions on initialize; drive a session to
	// read them the way a client would, rather than reaching into the
	// package.
	ctx := context.Background()
	srv := server.New(d)
	ct, st := newTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Wait() }()
	cs, err := newClient().Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	return cs.InitializeResult().Instructions
}

func TestLoggerOnlyAttachedAtDebug(t *testing.T) {
	// At info the SDK's own session chatter would put two lines per
	// session into every client's log file for nothing.
	var buf bytes.Buffer
	d := deps(t)
	d.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := server.New(d)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if strings.Contains(buf.String(), "server connecting") {
		t.Fatal("the SDK logger was attached at info level")
	}
}
