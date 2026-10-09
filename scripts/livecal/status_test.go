//go:build live

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/redact"
)

// These hold §9.1's guard without a network or an account, so they run
// in `make test` beside the unit tests rather than only in a live run.

const self = "person@example.test"

func guard() *primaryGuard {
	g := &primaryGuard{}
	g.setSelf(self)
	return g
}

// Every write naming the primary calendar is refused, however it names
// it, except the one status event; a second one is refused too.
func TestTheGuardLetsOneStatusEventOntoThePrimaryCalendar(t *testing.T) {
	g := guard()
	for _, c := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"an update by alias", "update_event", map[string]any{"calendar": "primary", "event_id": "x"}},
		{"a cancel by address", "cancel_event", map[string]any{"calendar": "PERSON@example.test", "event_id": "x"}},
		{"a create naming no calendar", "create_event", map[string]any{"title": "Anything"}},
		{"a calendar that is not a string", "update_event", map[string]any{"calendar": 7}},
		{"a move onto it", "move_event", map[string]any{"calendar": "scratch@group.calendar.example.test",
			"to_calendar": "primary"}},
		{"a tool the driver does not know", "new_tool", map[string]any{"calendar": "scratch@group.calendar.example.test"}},
		{"the status event with a guest", "create_event", withArg(statusArgs("primary"), "guests", []string{"a@example.test"})},
		{"the status event declining", "create_event", withArg(statusArgs("primary"), "auto_decline", "all")},
		{"the status event as a dry run", "create_event", withArg(statusArgs("primary"), "dry_run", true)},
	} {
		if err := g.tool(c.tool, c.args); err == nil {
			t.Errorf("%s: went out", c.name)
		}
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"list_events", map[string]any{"calendars": []string{"primary"}}},
		{"update_event", map[string]any{"calendar": "scratch@group.calendar.example.test", "event_id": "x"}},
	} {
		if err := g.tool(c.tool, c.args); err != nil {
			t.Errorf("%s off the primary calendar was refused: %v", c.tool, err)
		}
	}
	if err := g.tool("create_event", statusArgs("primary")); err != nil {
		t.Fatalf("the one status event was refused: %v", err)
	}
	if err := g.tool("create_event", statusArgs("primary")); err == nil {
		t.Fatal("a second status event went out")
	}
}

// The driver's own REST calls pass the same guard: nothing but a read and
// the delete of the event it made reaches the primary calendar, and a
// move is held to its destination as well as its source.
func TestTheGuardHoldsTheDriversOwnCalls(t *testing.T) {
	g := guard()
	if err := g.tool("create_event", statusArgs("primary")); err != nil {
		t.Fatal(err)
	}
	g.answered(callResult{text: "Created the event.\nid: AAAAstatus1\n"})
	for _, c := range []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/calendars/primary/events", true},
		{http.MethodPost, "/calendars/primary/events", false},
		{http.MethodPatch, "/calendars/person%40example.test/events/x", false},
		{http.MethodDelete, "/calendars/primary/events/AAAAother1", false},
		{http.MethodDelete, "/calendars/primary/events/AAAAstatus1?sendUpdates=none", true},
		{http.MethodPatch, "/users/me/calendarList/primary", false},
		{http.MethodPost, "/calendars/scratch%40group.calendar.example.test/events/x/move?destination=primary", false},
		{http.MethodPost, "/calendars/scratch%40group.calendar.example.test/events/x/move?destination=PERSON%40example.test", false},
		{http.MethodPost, "/calendars/scratch%40group.calendar.example.test/events/x/move?destination=", false},
		{http.MethodPost, "/calendars/scratch%40group.calendar.example.test/events/x/move?destination=a&destination=b", false},
		{http.MethodPost, "/calendars/scratch%40group.calendar.example.test/events/x/move?destination=dest%40group.calendar.example.test", true},
		{http.MethodPost, "/calendars", true},
	} {
		err := g.rest(c.method, c.path)
		if (err == nil) != c.allowed {
			t.Errorf("%s %s: allowed=%v, want %v (%v)", c.method, c.path, err == nil, c.allowed, err)
		}
	}
}

// A create that was refused made nothing, so the cleanup says nothing.
// One that may have made the event without naming it says exactly what
// to delete by hand, and an ambiguous one that names its id is deleted
// by that id.
func TestTheCleanupWarnsOnlyWhenTheEventMayExist(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer callResult
		made   string
		warns  bool
	}{
		{"refused by the server", callResult{isError: true, text: "[invalid] auto_decline is required"}, "", false},
		{"refused by Google", callResult{isError: true, text: "[forbidden] Google refused it"}, "", false},
		{"no answer", callResult{isError: true, text: "[ambiguous_outcome] the request failed"}, "", true},
		{"no answer, with the id", callResult{isError: true,
			text: "[ambiguous_outcome] the request failed. It used the id AAAAstatus2 on calendar primary"},
			"AAAAstatus2", false},
	} {
		g := guard()
		args := statusArgs("primary")
		if err := g.tool("create_event", args); err != nil {
			t.Fatal(err)
		}
		if got := g.answered(c.answer); got != c.made {
			t.Errorf("%s: remembered %q, want %q", c.name, got, c.made)
		}
		if c.made != "" {
			continue // deleted by id, which needs the network
		}
		var b strings.Builder
		g.cleanUp(nil, redact.New(&b))
		want := `delete it by hand`
		if got := strings.Contains(b.String(), want); got != c.warns {
			t.Errorf("%s: warned %v, want %v:\n%s", c.name, got, c.warns, b.String())
		}
		if c.warns && !strings.Contains(b.String(), `"Livecal status probe" starting `+args["start"].(string)) {
			t.Errorf("%s: the warning does not say exactly what to delete:\n%s", c.name, b.String())
		}
	}
}

// Stopped by a signal, the driver says what is left behind if it is
// stopped again before its cleanup, and says nothing of the status event
// when there is none.
func TestAnInterruptSaysWhatToDelete(t *testing.T) {
	g := guard()
	var before strings.Builder
	g.interrupted(redact.New(&before))
	if strings.Contains(before.String(), "by hand") {
		t.Fatalf("an interrupt before the status event names one to delete:\n%s", before.String())
	}
	args := statusArgs("primary")
	if err := g.tool("create_event", args); err != nil {
		t.Fatal(err)
	}
	var after strings.Builder
	g.interrupted(redact.New(&after))
	if !strings.Contains(after.String(), `delete "Livecal status probe" starting `+args["start"].(string)+
		" on your primary calendar by hand") {
		t.Fatalf("the interrupt does not say exactly what to delete:\n%s", after.String())
	}
}

// Once the driver is interrupted no further step reaches the server, so
// the run ends and its deferred cleanup runs.
func TestAnInterruptedRunCallsNothingMore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b strings.Builder
	r := &results{out: redact.New(&b)}
	// A nil session: a step that reached it would panic.
	r.run(ctx, nil, step{name: "status event", tool: "create_event", args: statusArgs("primary")})
	if r.undetermined != 1 || !strings.Contains(b.String(), "not run: the driver was interrupted") {
		t.Fatalf("undetermined %d:\n%s", r.undetermined, b.String())
	}
}

func withArg(args map[string]any, k string, v any) map[string]any {
	args[k] = v
	return args
}
