package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v5/internal/server"
	"github.com/mmedum/google-calendar-mcp/v5/internal/service"
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
	// The instructions are the model's orientation. The three defects
	// this server exists to avoid have to be in them, or a model will
	// walk into the same ones.
	got := strings.ToLower(instructionsOf(t, deps(t)))
	for _, want := range []string{"all-day", "expand", "check_availability"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the server instructions do not mention %q", want)
		}
	}
}

// share_calendar lowers any rule a calendar has, the public one
// included, so the instructions must not leave a model believing that
// needs a tool the operator left off.
func TestInstructionsSayShareCalendarLowersAnyRule(t *testing.T) {
	if got := instructionsOf(t, deps(t)); !strings.Contains(got, "lowers the access of any rule") {
		t.Fatalf("the server instructions do not say share_calendar lowers any rule:\n%s", got)
	}
}

func instructionsOf(t *testing.T, d server.Deps) string {
	t.Helper()
	// The SDK exposes instructions on initialize; drive a session to
	// read them the way a client would, rather than reaching into the
	// package. session is the one place that wiring lives.
	return session(t, d).InitializeResult().Instructions
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

// TestTheDumpCarriesEveryField holds the schema dump to what a client is
// told. The dump is a projection with its own field names, and a field
// it leaves out is one no schema gate can see change: it once dropped
// _meta, which carries the mark that makes Claude Code ask the person on
// every call. Every JSON field of the SDK's tool, resource and resource
// template must have a counterpart, under the dump's snake_case name.
func TestTheDumpCarriesEveryField(t *testing.T) {
	for _, tc := range []struct {
		sdk, dump any
	}{
		{mcp.Tool{}, server.SchemaTool{}},
		{mcp.Resource{}, server.SchemaResource{}},
		{mcp.ResourceTemplate{}, server.SchemaResource{}},
	} {
		have := map[string]bool{}
		for _, name := range jsonNames(reflect.TypeOf(tc.dump)) {
			have[name] = true
		}
		sdkNames := jsonNames(reflect.TypeOf(tc.sdk))
		if len(sdkNames) < 5 {
			t.Fatalf("%T has %d JSON fields; the test is not reading it", tc.sdk, len(sdkNames))
		}
		for _, name := range sdkNames {
			if !have[snake(name)] {
				t.Errorf("%T's field %q has no counterpart %q in %T", tc.sdk, name, snake(name), tc.dump)
			}
		}
	}
}

// jsonNames is the JSON name of every field of a struct type, the fields
// of an untagged embedded struct included, as encoding/json flattens
// them.
func jsonNames(typ reflect.Type) []string {
	var names []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			names = append(names, jsonNames(f.Type)...)
			continue
		}
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// The dump copies what it declares: the instructions, the marked tools
// their _meta, every tool its annotations, and every resource its title.
// A field declared and never set would pass the test above and still
// vanish.
func TestTheDumpCopiesTheFields(t *testing.T) {
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), &buf, deps(t)); err != nil {
		t.Fatal(err)
	}
	var dump server.SchemaDump
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dump.Instructions, "Google Calendar tools.") {
		t.Errorf("the dump's instructions are %q, want the server's", dump.Instructions)
	}
	marked := map[string]bool{}
	for _, tool := range dump.Tools {
		if len(tool.Annotations) == 0 {
			t.Errorf("%s: no annotations in the dump", tool.Name)
		}
		var meta map[string]any
		if len(tool.Meta) > 0 && json.Unmarshal(tool.Meta, &meta) == nil && meta["anthropic/requiresUserInteraction"] == true {
			marked[tool.Name] = true
		}
	}
	for _, name := range []string{"clear_calendar", "delete_calendar", "publish_calendar"} {
		if !marked[name] {
			t.Errorf("%s: the dump does not carry its _meta mark", name)
		}
	}
	if len(dump.Resources) < 3 {
		t.Fatalf("the dump has %d resources, want at least 3", len(dump.Resources))
	}
	for _, r := range dump.Resources {
		if r.Title == "" {
			t.Errorf("%s: no title in the dump", r.Name)
		}
	}
}

// This server registers no prompt, so the dump is held to carry one from
// a server that does: a prompt added later is part of the surface the
// day it lands.
func TestTheDumpCarriesAPrompt(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	srv.AddPrompt(&mcp.Prompt{
		Name: "plan_week", Title: "Plan the week", Description: "Lays out the week.",
		Arguments: []*mcp.PromptArgument{{Name: "calendar_id", Required: true}},
		Meta:      mcp.Meta{"example/mark": true},
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	})
	var buf bytes.Buffer
	if err := server.DumpServer(context.Background(), &buf, srv, "test"); err != nil {
		t.Fatal(err)
	}
	var dump struct {
		Prompts json.RawMessage `json:"prompts"`
	}
	if err := json.Unmarshal(buf.Bytes(), &dump); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, dump.Prompts); err != nil {
		t.Fatal(err)
	}
	want := `[{"_meta":{"example/mark":true},"arguments":[{"name":"calendar_id","required":true}],` +
		`"description":"Lays out the week.","name":"plan_week","title":"Plan the week"}]`
	if got.String() != want {
		t.Errorf("the dump's prompts are\n%s\nwant\n%s", got.String(), want)
	}
}

// snake is the dump's name for an SDK field: inputSchema is input_schema.
func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
