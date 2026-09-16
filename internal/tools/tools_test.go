package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/server"
	"github.com/mmedum/google-calendar-mcp/internal/service"
	"github.com/mmedum/google-calendar-mcp/internal/tools"
)

func listTools(t *testing.T, cfg config.Config) []*mcp.Tool {
	t.Helper()
	fake := caltest.Seed()
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base

	srv := server.New(server.Deps{
		Service: service.New(api, cfg), Config: cfg, Version: "test",
	})

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Wait() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

func baseConfig() config.Config {
	return config.Config{MaxEvents: 250, MaxCalendars: 25, Concurrency: 4, Sharing: true}
}

func names(ts []*mcp.Tool) map[string]*mcp.Tool {
	out := map[string]*mcp.Tool{}
	for _, t := range ts {
		out[t.Name] = t
	}
	return out
}

// readTools is the eight of §8's first block: everything read-only mode
// keeps, and everything that asks for only the read scopes.
var readTools = []string{
	"list_calendars", "get_calendar", "list_events",
	"search_events", "get_event", "list_instances",
	"check_availability", "get_settings",
}

// writeTools is phase 2's block: the five event writes.
var writeTools = []string{
	"create_event", "update_event", "cancel_event", "move_event", "respond_to_event",
}

// calendarTools is phase 3's, minus the ones behind a gate: creating a
// calendar and managing it register wherever a write does.
var calendarTools = []string{"create_calendar", "manage_calendar"}

// sharingTools is what GCAL_SHARING=off removes, the read among them
// (§7.6).
var sharingTools = []string{"list_sharing", "share_calendar", "unshare_calendar"}

// gatedTools need GCAL_ENABLE_DESTRUCTIVE to register at all, and
// confirm:true on the call besides (§9).
var gatedTools = []string{"delete_calendar", "clear_calendar"}

func TestTheDefaultSurfaceIsEverythingButTheGatedTools(t *testing.T) {
	got := names(listTools(t, baseConfig()))
	want := append(append([]string{}, readTools...), writeTools...)
	want = append(want, calendarTools...)
	want = append(want, sharingTools...)
	for _, n := range want {
		if _, ok := got[n]; !ok {
			t.Fatalf("tool %q is not registered", n)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("registered %d tools, expected %d: %v", len(got), len(want), got)
	}
}

// §10: read-only mode registers only the read tools, because it also
// requests only the read scopes. A write tool left registered there
// would fail at Google with a 403 nobody can act on.
func TestReadOnlyModeDropsEveryWrite(t *testing.T) {
	cfg := baseConfig()
	cfg.ReadOnly = true
	got := names(listTools(t, cfg))
	for _, n := range append(append(append([]string{}, writeTools...), calendarTools...),
		append(sharingTools, gatedTools...)...) {
		if _, ok := got[n]; ok {
			t.Fatalf("read-only mode registered %q", n)
		}
	}
	if len(got) != len(readTools) {
		t.Fatalf("read-only registered %d tools, expected the %d reads: %v", len(got), len(readTools), got)
	}
}

// TestReadOnlyKeepsEveryReadTool: read-only must not be a quieter
// server, only a safer one.
func TestReadOnlyKeepsEveryReadTool(t *testing.T) {
	cfg := baseConfig()
	cfg.ReadOnly = true
	readOnly := names(listTools(t, cfg))

	for _, name := range readTools {
		if _, ok := readOnly[name]; !ok {
			t.Fatalf("read-only mode dropped %q, which is a read tool", name)
		}
	}
}

// TestFullSurfaceRegistersEverything holds the claim that FullSurface
// names every gate `allowed` reads. A sibling's schema dump set one flag
// by hand for three phases; a fourth gate arrived and the tool behind it
// was missing from the schema diff with every test still green.
func TestFullSurfaceRegistersEverything(t *testing.T) {
	full := names(listTools(t, tools.FullSurface(baseConfig())))

	// Every combination of the gate flags. None may register a tool the
	// full surface does not.
	for _, readOnly := range []bool{false, true} {
		for _, destructive := range []bool{false, true} {
			for _, sharing := range []bool{false, true} {
				cfg := baseConfig()
				cfg.ReadOnly, cfg.EnableDestructive, cfg.Sharing = readOnly, destructive, sharing
				for name := range names(listTools(t, cfg)) {
					if _, ok := full[name]; !ok {
						t.Fatalf("config{readOnly:%v destructive:%v sharing:%v} registers %q, "+
							"which FullSurface does not — the schema dump would miss it",
							readOnly, destructive, sharing, name)
					}
				}
			}
		}
	}
}

func TestEveryToolHasADescriptionAndAFlatSchema(t *testing.T) {
	for _, tool := range listTools(t, tools.FullSurface(baseConfig())) {
		if strings.TrimSpace(tool.Description) == "" {
			t.Fatalf("%s has no description; a tool the model never finds does not exist", tool.Name)
		}
		if len(tool.Description) < 60 {
			t.Fatalf("%s has a description of %d characters; it is the model's only map",
				tool.Name, len(tool.Description))
		}
		if strings.Contains(tool.Name, ".") {
			t.Fatalf("%s has a dot in its name", tool.Name)
		}
		if tool.Annotations == nil {
			t.Fatalf("%s has no annotations", tool.Name)
		}
	}
}

// TestReadToolsAreAnnotatedReadOnly: the annotation is a hint a client
// may ignore, but an incorrect one is actively misleading.
func TestReadToolsAreAnnotatedReadOnly(t *testing.T) {
	got := names(listTools(t, baseConfig()))
	for _, name := range readTools {
		tool := got[name]
		if !tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s is a read tool and is not annotated read-only", name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("%s claims an open world; a calendar read does not reach outside the account", name)
		}
	}
	for _, name := range writeTools {
		if got[name].Annotations.ReadOnlyHint {
			t.Fatalf("%s writes and is annotated read-only", name)
		}
	}
}

// An annotation a client uses to decide whether to ask a person has to
// be true. cancel_event is not behind the destructive FLAG — §9 argues
// that a gate everybody turns on protects nobody — but it is still the
// one write here that removes a meeting, and the hint says so.
func TestCancelEventIsAnnotatedDestructiveWithoutBeingGated(t *testing.T) {
	got := names(listTools(t, baseConfig()))
	cancel, ok := got["cancel_event"]
	if !ok {
		t.Fatal("cancel_event must register without GCAL_ENABLE_DESTRUCTIVE (§9)")
	}
	if cancel.Annotations.DestructiveHint == nil || !*cancel.Annotations.DestructiveHint {
		t.Fatal("cancel_event removes a meeting and must be annotated destructive")
	}
	for _, name := range []string{"create_event", "update_event", "respond_to_event", "move_event"} {
		a := got[name].Annotations
		if a.DestructiveHint != nil && *a.DestructiveHint {
			t.Fatalf("%s is annotated destructive and is not", name)
		}
	}
}

// Every write tool has to teach the two rules a model will otherwise
// learn from a refusal.
func TestEveryWriteToolDocumentsNotifyAndDryRun(t *testing.T) {
	got := names(listTools(t, tools.FullSurface(baseConfig())))
	for _, name := range writeTools {
		d := got[name].Description
		if !strings.Contains(d, "notify") {
			t.Errorf("%s does not mention notify, which it requires", name)
		}
		if !strings.Contains(d, "dry_run") {
			t.Errorf("%s does not mention dry_run", name)
		}
	}
	for _, name := range []string{"update_event", "cancel_event", "move_event", "respond_to_event"} {
		if !strings.Contains(got[name].Description, "scope") {
			t.Errorf("%s does not mention scope, which it requires on a repeating event", name)
		}
	}
}

// TestOverlappingToolsPointAtEachOther is the standard's rule: if two
// tools overlap, each description names the other and says when to
// choose it.
func TestOverlappingToolsPointAtEachOther(t *testing.T) {
	got := names(listTools(t, baseConfig()))
	pairs := [][2]string{
		{"list_events", "search_events"},
		{"search_events", "list_events"},
		{"list_calendars", "get_calendar"},
		{"get_calendar", "list_calendars"},
		{"list_instances", "list_events"},
		{"list_events", "check_availability"},
		{"check_availability", "free"},
	}
	for _, p := range pairs {
		tool, ok := got[p[0]]
		if !ok {
			t.Fatalf("%s is missing", p[0])
		}
		if !strings.Contains(tool.Description, p[1]) {
			t.Fatalf("%s overlaps %s and does not name it:\n%s", p[0], p[1], tool.Description)
		}
	}
}

// TestListEventsWarnsAboutTheRecurrenceChoice: §2.9 makes expand a real
// decision, so the description has to say so.
func TestListEventsWarnsAboutTheRecurrenceChoice(t *testing.T) {
	tool := names(listTools(t, baseConfig()))["list_events"]
	for _, want := range []string{"expand", "occurrence", "series"} {
		if !strings.Contains(strings.ToLower(tool.Description), want) {
			t.Fatalf("list_events does not explain %q:\n%s", want, tool.Description)
		}
	}
}

// TestSearchDescribesItsOwnUnreliability: Google's q is undocumented
// free text, and a model must not read an empty result as proof.
func TestSearchDescribesItsOwnUnreliability(t *testing.T) {
	tool := names(listTools(t, baseConfig()))["search_events"]
	lower := strings.ToLower(tool.Description)
	for _, want := range []string{"undocumented", "found nothing"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("search_events does not warn that an empty result is not proof:\n%s", tool.Description)
		}
	}
}

func TestToolsCallSucceedsAgainstTheFake(t *testing.T) {
	fake := caltest.Seed()
	base := fake.Start()
	defer fake.Close()
	api := gapi.New(nil)
	api.Base = base
	cfg := baseConfig()

	srv := server.New(server.Deps{Service: service.New(api, cfg), Config: cfg, Version: "test"})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_calendars"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_calendars failed: %+v", res.Content)
	}
	// Both halves, and never the same bytes: a client shows one or the
	// other, and the failure to guard against is the half that carried
	// the substance being filtered away.
	if len(res.Content) == 0 {
		t.Fatal("no content block; a client that shows only content would see nothing")
	}
	if res.StructuredContent == nil {
		t.Fatal("no structured content; a client that shows only that would see nothing")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	if !strings.Contains(text.Text, "Sample Primary") {
		t.Fatalf("the readable half does not carry the calendars:\n%s", text.Text)
	}
}

// TestAnErrorComesBackAsAToolResult, formatted [class] message — never
// as a protocol error.
func TestAnErrorComesBackAsAToolResult(t *testing.T) {
	fake := caltest.Seed()
	base := fake.Start()
	defer fake.Close()
	api := gapi.New(nil)
	api.Base = base
	cfg := baseConfig()

	srv := server.New(server.Deps{Service: service.New(api, cfg), Config: cfg, Version: "test"})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_event", Arguments: map[string]any{"calendar": "primary", "event_id": "nope"},
	})
	if err != nil {
		t.Fatalf("a tool failure arrived as a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("a missing event did not produce an error result")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "[not_found]") {
		t.Fatalf("the error is not formatted [class] message: %q", text)
	}
}

// TestGatesAreServerSide walks every Kind against every configuration.
//
// All of these are registration gates, not annotations: the
// specification says a client treats annotations as untrusted, and a
// host in an auto-approve mode runs an annotated tool without prompting.
// If a tool must not run unattended, it must not be registered.
func TestGatesAreServerSide(t *testing.T) {
	cases := []struct {
		kind        tools.Kind
		readOnly    bool
		destructive bool
		sharing     bool
		want        bool
	}{
		{tools.Read, false, false, true, true},
		{tools.Read, true, false, true, true},

		{tools.Write, false, false, true, true},
		{tools.Write, true, false, true, false},
		{tools.IdempotentWrite, false, false, true, true},
		{tools.IdempotentWrite, true, false, true, false},

		{tools.Sharing, false, false, true, true},
		{tools.Sharing, false, false, false, false},
		{tools.Sharing, true, false, true, false},

		{tools.Destructive, false, false, true, false},
		{tools.Destructive, false, true, true, true},
		// Read-only wins over the destructive flag, in both orders.
		{tools.Destructive, true, true, true, false},
	}
	for _, c := range cases {
		cfg := config.Config{
			ReadOnly: c.readOnly, EnableDestructive: c.destructive, Sharing: c.sharing,
		}
		if got := tools.Allowed(c.kind, cfg); got != c.want {
			t.Fatalf("Allowed(kind=%d, readOnly=%v destructive=%v sharing=%v) = %v, want %v",
				c.kind, c.readOnly, c.destructive, c.sharing, got, c.want)
		}
	}
}

func TestAnnotationsMatchTheKind(t *testing.T) {
	read := tools.AnnotationsFor(tools.Read)
	if !read.ReadOnlyHint || !read.IdempotentHint {
		t.Fatalf("a read tool is not annotated read-only and idempotent: %+v", read)
	}

	destructive := tools.AnnotationsFor(tools.Destructive)
	if destructive.DestructiveHint == nil || !*destructive.DestructiveHint {
		t.Fatal("a destructive tool is not annotated destructive")
	}
	if destructive.ReadOnlyHint {
		t.Fatal("a destructive tool claims to be read-only")
	}

	// Sharing is the only Kind whose effect leaves the account, and the
	// open-world hint is the point of the distinction.
	sharing := tools.AnnotationsFor(tools.Sharing)
	if sharing.OpenWorldHint == nil || !*sharing.OpenWorldHint {
		t.Fatal("sharing is not annotated open-world; its effect leaves this account")
	}
	for _, k := range []tools.Kind{tools.Read, tools.Write, tools.IdempotentWrite, tools.Destructive} {
		a := tools.AnnotationsFor(k)
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Fatalf("kind %d claims an open world; only sharing reaches outside the account", k)
		}
	}

	write := tools.AnnotationsFor(tools.Write)
	if write.IdempotentHint {
		t.Fatal("a plain write claims to be idempotent; repeating it is not the same as doing it once")
	}
	if tools.AnnotationsFor(tools.IdempotentWrite).IdempotentHint != true {
		t.Fatal("an idempotent write is not annotated idempotent")
	}
}

func TestFullSurfaceOpensEveryGate(t *testing.T) {
	cfg := tools.FullSurface(config.Config{ReadOnly: true, EnableDestructive: false, Sharing: false})
	for _, k := range []tools.Kind{tools.Read, tools.Write, tools.IdempotentWrite, tools.Sharing, tools.Destructive} {
		if !tools.Allowed(k, cfg) {
			t.Fatalf("FullSurface does not register kind %d, so the schema dump would miss it", k)
		}
	}
}

func TestFailFormatsEveryError(t *testing.T) {
	classified := tools.Fail(gapi.Errf(gapi.ClassNotFound, "no such event"))
	if classified.Error() != "[not_found] no such event" {
		t.Fatalf("classified error = %q", classified.Error())
	}

	// An unclassified error is a bug in this server, and still has to
	// arrive with a class so the vocabulary stays closed from the
	// caller's side.
	plain := tools.Fail(errors.New("something unexpected"))
	if !strings.HasPrefix(plain.Error(), "[") {
		t.Fatalf("an unclassified error reached the caller without a class: %q", plain.Error())
	}
	cls := plain.Error()[1:strings.Index(plain.Error(), "]")]
	if !gapi.Class(cls).Valid() {
		t.Fatalf("an unclassified error produced %q, which is not in the vocabulary", cls)
	}
}

// TestEveryToolAnswers calls all six against the fake, so each handler
// runs at least once. A tool that compiles and has never been called is
// a tool nobody has checked.
func TestEveryToolAnswers(t *testing.T) {
	cs, cleanup := session(t, baseConfig())
	defer cleanup()
	ctx := context.Background()

	calls := []struct {
		name string
		args map[string]any
		want string
	}{
		{"list_calendars", nil, "Sample Primary"},
		{"list_calendars", map[string]any{"include_hidden": true}, "Sample Primary"},
		{"get_calendar", map[string]any{"calendar": "primary"}, "who can see it"},
		{"get_settings", nil, "Europe/Copenhagen"},
		{"list_events", map[string]any{"from": "2026-03-16", "to": "2026-03-17"}, "Morning sync"},
		{"list_events", map[string]any{
			"from": "2026-03-16", "to": "2026-03-31", "no_expand": true,
		}, "series"},
		{"list_events", map[string]any{
			"from": "2026-03-18", "to": "2026-03-19", "show_cancelled": true,
		}, "cancelled"},
		{"list_events", map[string]any{
			"from": "2026-03-16", "to": "2026-03-17", "time_zone": "America/Chicago",
		}, "America/Chicago"},
		{"search_events", map[string]any{
			"query": "sync", "from": "2026-03-16", "to": "2026-03-31",
		}, "Morning sync"},
		{"get_event", map[string]any{"calendar": "primary", "event_id": "ev-standup"}, "Morning sync"},
		{"get_event", map[string]any{"calendar": "primary", "event_id": "ev-holiday"}, "all day"},
		{"list_instances", map[string]any{
			"calendar": "primary", "event_id": "ev-weekly",
		}, "occurrence"},
		{"list_instances", map[string]any{
			"calendar": "primary", "event_id": "ev-weekly", "show_cancelled": true,
		}, "CANCELLED"},
		{"list_instances", map[string]any{
			"calendar": "primary", "event_id": "ev-weekly",
			"from": "2026-03-20", "to": "2026-03-26",
		}, "2026-03-24"},
		{"check_availability", map[string]any{"from": "2026-03-16", "to": "2026-03-16"}, "Free"},
		{"check_availability", map[string]any{
			"from": "2026-03-16", "to": "2026-03-16", "min_minutes": 30,
		}, "30m or longer"},
	}
	for _, c := range calls {
		t.Run(c.name+"/"+c.want, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", c.name, err)
			}
			if res.IsError {
				t.Fatalf("%s failed: %s", c.name, text(t, res))
			}
			if res.StructuredContent == nil {
				t.Fatalf("%s returned no structured half", c.name)
			}
			if got := text(t, res); !strings.Contains(got, c.want) {
				t.Fatalf("%s did not mention %q:\n%s", c.name, c.want, got)
			}
		})
	}
}

// TestAnAllDayEventNeverRendersATimeThroughTheTools is §4.1 end to end,
// at the boundary a model actually sees.
func TestAnAllDayEventNeverRendersATimeThroughTheTools(t *testing.T) {
	cs, cleanup := session(t, baseConfig())
	defer cleanup()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_event",
		Arguments: map[string]any{
			"calendar": "primary", "event_id": "ev-holiday",
			// Read from a zone far west of the calendar's own.
			"time_zone": "Pacific/Honolulu",
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("get_event: %v %v", err, res)
	}
	got := text(t, res)
	if !strings.Contains(got, "2026-03-20") {
		t.Fatalf("the all-day event moved when read from Honolulu:\n%s", got)
	}
	if strings.Contains(got, "00:00") {
		t.Fatalf("an all-day event rendered a time:\n%s", got)
	}
}

func TestToolErrorsAreClassified(t *testing.T) {
	cs, cleanup := session(t, baseConfig())
	defer cleanup()
	ctx := context.Background()

	cases := []struct {
		name  string
		args  map[string]any
		class string
	}{
		{"get_calendar", map[string]any{"calendar": "Sample"}, "[ambiguous]"},
		{"get_calendar", map[string]any{"calendar": "No Such Thing"}, "[not_found]"},
		{"list_events", map[string]any{"from": "tomorrow", "to": "2026-03-17"}, "[invalid]"},
		{"get_event", map[string]any{"calendar": "primary", "event_id": "nope"}, "[not_found]"},
		{"list_instances", map[string]any{
			"calendar": "primary", "event_id": "ev-weekly", "from": "2026-03-20",
		}, "[invalid]"},
	}
	for _, c := range cases {
		t.Run(c.name+c.class, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			if err != nil {
				t.Fatalf("a tool failure arrived as a protocol error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("%s(%v) succeeded", c.name, c.args)
			}
			if got := text(t, res); !strings.Contains(got, c.class) {
				t.Fatalf("want %s, got: %s", c.class, got)
			}
		})
	}
}

// TestAMissingRequiredArgumentIsCaughtBySchema.
//
// The SDK validates against the declared input schema before the handler
// runs, so a missing `to` never reaches this server's own checks. That
// is the right layer for it — the message names the property — but it
// means such a failure does NOT carry one of the twelve classes, and a
// test asserting [invalid] here would be asserting about the SDK.
func TestAMissingRequiredArgumentIsCaughtBySchema(t *testing.T) {
	cs, cleanup := session(t, baseConfig())
	defer cleanup()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_events", Arguments: map[string]any{"from": "2026-03-16"},
	})
	if err != nil {
		t.Fatalf("schema validation arrived as a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("a call missing a required argument succeeded")
	}
	got := text(t, res)
	if !strings.Contains(got, "to") {
		t.Fatalf("the refusal does not name the missing property: %s", got)
	}
}

func session(t *testing.T, cfg config.Config) (*mcp.ClientSession, func()) {
	t.Helper()
	return sessionWith(t, cfg, caltest.Seed())
}

func sessionWith(t *testing.T, cfg config.Config, fake *caltest.Server) (*mcp.ClientSession, func()) {
	t.Helper()
	base := fake.Start()
	api := gapi.New(nil)
	api.Base = base

	srv := server.New(server.Deps{Service: service.New(api, cfg), Config: cfg, Version: "test"})
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, func() {
		_ = cs.Close()
		_ = ss.Wait()
		fake.Close()
	}
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("no content block")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", res.Content[0])
	}
	return tc.Text
}

// TestCheckAvailabilityExplainsWhyItIsNotAnEventList is §4.6 at the
// boundary a model reads: a list of events answers a different question,
// and an unreadable calendar is not free.
func TestCheckAvailabilityExplainsWhyItIsNotAnEventList(t *testing.T) {
	tool := names(listTools(t, baseConfig()))["check_availability"]
	lower := strings.ToLower(tool.Description)
	for _, want := range []string{"cannot read", "unknown", "never as free"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("check_availability does not warn about %q:\n%s", want, tool.Description)
		}
	}
}

// TestListInstancesExplainsAnOccurrenceID.
//
// It asserts the explanation and not the class: Google does not document
// what events.instances returns for an id that exists but is not a
// series, so the class here is whatever Google chose. The live driver
// probes it and §18 will carry the answer; this holds the part that is
// this server's to get right.
func TestListInstancesExplainsAnOccurrenceID(t *testing.T) {
	cs, cleanup := session(t, baseConfig())
	defer cleanup()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_instances",
		Arguments: map[string]any{
			"calendar": "primary", "event_id": "ev-weekly_20260324T130000Z",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("an occurrence id was accepted as a series id")
	}
	got := text(t, res)
	if !strings.Contains(got, "series_id") {
		t.Fatalf("the refusal does not explain the mistake: %s", got)
	}
}

// TestListInstancesWarnsAboutTheIDItNeeds: the id a model has in hand is
// usually an occurrence's, and passing it gets a bare "not found".
func TestListInstancesWarnsAboutTheIDItNeeds(t *testing.T) {
	tool := names(listTools(t, baseConfig()))["list_instances"]
	for _, want := range []string{"series_id", "SERIES"} {
		if !strings.Contains(tool.Description, want) {
			t.Fatalf("list_instances does not say which id it wants (%q):\n%s", want, tool.Description)
		}
	}
}

// The write tools at the boundary a model actually sees: the arguments
// go in as JSON, and both halves of the result come back.
//
// The service tests hold the rules; this holds the wiring, which is the
// half a unit test on the service cannot reach — a handler that passed
// the wrong field through would leave every service test green.
func TestTheWriteToolsWorkThroughTheProtocol(t *testing.T) {
	fake := caltest.Seed()
	invite := caltest.Timed("ev-invite", "Somebody else's meeting",
		"2026-03-19T13:00:00+01:00", "2026-03-19T14:00:00+01:00", "Europe/Copenhagen")
	invite.Organizer = &gcal.EventPerson{Email: "host@example.test"}
	invite.Attendees = []gcal.EventAttendee{
		{Email: "host@example.test", Organizer: true, ResponseStatus: gcal.ResponseAccepted},
		{Email: "owner@example.test", Self: true, ResponseStatus: gcal.ResponseNeedsAction},
	}
	fake.AddEvent("primary", invite)

	cs, cleanup := sessionWith(t, tools.FullSurface(baseConfig()), fake)
	defer cleanup()
	ctx := context.Background()

	calls := []struct {
		name string
		args map[string]any
		want string
	}{
		{"create_event", map[string]any{
			"title": "Written through the protocol",
			"start": "2026-04-01T09:00:00+02:00", "end": "2026-04-01T10:00:00+02:00",
		}, "Created"},
		{"create_event", map[string]any{
			"title": "All day, through the protocol",
			"start": "2026-04-03", "end": "2026-04-03", "dry_run": true,
		}, "DRY RUN"},
		{"update_event", map[string]any{
			"calendar": "primary", "event_id": "ev-standup", "location": "Room 2",
		}, "location"},
		{"respond_to_event", map[string]any{
			"calendar": "primary", "event_id": "ev-invite",
			"response": "tentative", "notify": "all",
		}, "your response"},
		{"move_event", map[string]any{
			"calendar": "primary", "event_id": "ev-standup",
			"to_calendar": "team@group.calendar.example.test",
		}, "Moved"},
		{"cancel_event", map[string]any{
			"calendar": "primary", "event_id": "ev-transparent",
		}, "Cancelled"},
	}
	for _, c := range calls {
		t.Run(c.name+"/"+c.want, func(t *testing.T) {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", c.name, err)
			}
			if res.IsError {
				t.Fatalf("%s failed: %s", c.name, text(t, res))
			}
			// Both halves, never the same bytes (§10).
			if res.StructuredContent == nil {
				t.Fatalf("%s returned no structured half", c.name)
			}
			if got := text(t, res); !strings.Contains(got, c.want) {
				t.Fatalf("%s did not mention %q:\n%s", c.name, c.want, got)
			}
		})
	}
}

// A refusal arrives as a tool result carrying a class, never as a
// protocol error (§6.5).
func TestAWriteRefusalArrivesAsAClassifiedToolResult(t *testing.T) {
	cs, cleanup := session(t, tools.FullSurface(baseConfig()))
	defer cleanup()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_event",
		Arguments: map[string]any{
			"title": "No notify", "start": "2026-04-01T09:00:00+02:00",
			"end": "2026-04-01T10:00:00+02:00", "guests": []string{"somebody@example.test"},
		},
	})
	if err != nil {
		t.Fatalf("a refusal must not be a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatal("creating an event with guests and no notify must be refused (§4.3)")
	}
	if got := text(t, res); !strings.Contains(got, "[invalid]") {
		t.Fatalf("the refusal carries no class:\n%s", got)
	}
}
