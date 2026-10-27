//go:build live

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/redact"
)

// These hold §9.1's guard without a network or an account, so they run
// in `make test` beside the unit tests rather than only in a live run.

const self = "person@example.test"

// guard is a primary guard that writes its record into a directory of
// the test's own.
func guard(t *testing.T) *primaryGuard {
	t.Helper()
	g := &primaryGuard{dir: t.TempDir()}
	g.setSelf(self)
	return g
}

// Every write naming the primary calendar is refused, however it names
// it, except the one status event; a second one is refused too.
func TestTheGuardLetsOneStatusEventOntoThePrimaryCalendar(t *testing.T) {
	g := guard(t)
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
		{"an event declining every meeting", "create_event_declining_all", statusArgs("primary")},
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
	g := guard(t)
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
		g := guard(t)
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
		// The record stays, and is named, exactly while the event may exist.
		_, err := os.Stat(g.recordPath())
		if kept := err == nil; kept != c.warns {
			t.Errorf("%s: record kept %v, want %v", c.name, kept, c.warns)
		}
		if named := strings.Contains(b.String(), g.recordPath()); named != c.warns {
			t.Errorf("%s: record named %v, want %v:\n%s", c.name, named, c.warns, b.String())
		}
	}
}

// Stopped by a signal, the driver says what is left behind if it is
// stopped again before its cleanup, and says nothing of the status event
// when there is none.
func TestAnInterruptSaysWhatToDelete(t *testing.T) {
	g := guard(t)
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
	if !strings.Contains(after.String(), "Its record is "+g.recordPath()) {
		t.Fatalf("the interrupt does not name the record:\n%s", after.String())
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

// The status event is recorded, owner only and named for the run, before
// the create goes out, and the record gains the id once it is known.
func TestTheStatusEventIsRecordedBeforeItIsAskedFor(t *testing.T) {
	g := guard(t)
	path := g.recordPath()
	if want := filepath.Join(g.dir, "livecal-status-event-"+runSuffix+".txt"); path != want {
		t.Fatalf("record at %s, want %s", path, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a record exists before the status event was asked for: %v", err)
	}
	args := statusArgs("primary")
	if err := g.tool("create_event", args); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the create went out with no record: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %o, want 600", fi.Mode().Perm())
	}
	before := readFile(t, path)
	for _, want := range []string{"title: Livecal status probe\n", "start: " + args["start"].(string) + "\n",
		"id: not known yet\n"} {
		if !strings.Contains(before, want) {
			t.Fatalf("the record lacks %q:\n%s", want, before)
		}
	}
	g.answered(callResult{text: "Created the event.\nid: AAAAstatus1\n"})
	if after := readFile(t, path); !strings.Contains(after, "id: AAAAstatus1\n") {
		t.Fatalf("the record does not carry the id once it is known:\n%s", after)
	}
}

// A status event whose record cannot be written is not asked for, and
// leaves nothing for the cleanup to look for.
func TestAStatusEventWithNoRecordIsNotAskedFor(t *testing.T) {
	g := guard(t)
	g.dir = filepath.Join(g.dir, "missing")
	if err := g.tool("create_event", statusArgs("primary")); err == nil {
		t.Fatal("the status event went out with no record")
	}
	if g.pending() {
		t.Fatal("a status event that was never asked for is waiting to be deleted")
	}
	if err := g.tool("create_event", statusArgs("primary")); err == nil {
		t.Fatal("a second try went out")
	}
}

// The cleanup deletes the status event and reads it back. The record is
// removed only when the read finds it gone; otherwise the warning names
// the record and the record stays.
func TestTheCleanupReadsTheStatusEventBackGone(t *testing.T) {
	for _, c := range []struct {
		name      string
		del, read int
		status    string
		gone      bool
	}{
		{"deleted and not found", http.StatusNoContent, http.StatusNotFound, "", true},
		{"already gone and read cancelled", http.StatusGone, http.StatusOK, "cancelled", true},
		{"deleted but still there", http.StatusNoContent, http.StatusOK, "confirmed", false},
		{"the delete failed", http.StatusInternalServerError, http.StatusOK, "confirmed", false},
		{"the read failed", http.StatusNoContent, http.StatusInternalServerError, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := guard(t)
			defer swapPrimary(g)()
			if err := g.tool("create_event", statusArgs("primary")); err != nil {
				t.Fatal(err)
			}
			g.answered(callResult{text: "id: AAAAstatus1\n"})
			var deleted bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/calendars/primary/events/AAAAstatus1" {
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusTeapot)
					return
				}
				switch r.Method {
				case http.MethodDelete:
					deleted = true
					w.WriteHeader(c.del)
				case http.MethodGet:
					if !deleted {
						t.Error("read back before the delete")
					}
					w.WriteHeader(c.read)
					_, _ = w.Write([]byte(`{"status":"` + c.status + `"}`))
				}
			}))
			defer srv.Close()
			var b strings.Builder
			g.cleanUp(&liveAPI{hc: srv.Client(), base: srv.URL}, redact.New(&b))
			_, err := os.Stat(g.recordPath())
			if kept := err == nil; kept == c.gone {
				t.Fatalf("record kept %v with the event gone %v:\n%s", kept, c.gone, b.String())
			}
			if c.gone != strings.Contains(b.String(), "read back gone") {
				t.Fatalf("reported gone %v, want %v:\n%s", !c.gone, c.gone, b.String())
			}
			if !c.gone && !strings.Contains(b.String(), g.recordPath()) {
				t.Fatalf("a failed cleanup does not name the record:\n%s", b.String())
			}
		})
	}
}

// swapPrimary makes g the guard the driver's REST calls pass, and
// returns what restores the one before.
func swapPrimary(g *primaryGuard) func() {
	was := primaryCal
	primaryCal = g
	return func() { primaryCal = was }
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
