//go:build live

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// These hold that the driver adopts only a scratch calendar it made for
// this account, and that emptying one mails nobody, against a fake
// Calendar API rather than an account.

const freshCalendar = "fresh@group.calendar.example.test"

type fakeEvent struct {
	ID        string              `json:"id"`
	Attendees []map[string]string `json:"attendees,omitempty"`
}

// fakeCalendarAPI answers the calls adoption makes: the calendar list,
// a calendar's events, an event delete and a calendar create.
type fakeCalendarAPI struct {
	t       *testing.T
	mu      sync.Mutex
	list    []calendarEntry
	events  map[string][]fakeEvent
	deletes []string
	created int
}

func (f *fakeCalendarAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == http.MethodGet && path == "/users/me/calendarList":
		reply(map[string]any{"items": f.list})
	case r.Method == http.MethodPost && path == "/calendars":
		f.created++
		reply(map[string]any{"id": freshCalendar})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/events"):
		cal := strings.TrimSuffix(strings.TrimPrefix(path, "/calendars/"), "/events")
		reply(map[string]any{"items": f.events[cal]})
	case r.Method == http.MethodDelete && strings.Contains(path, "/events/"):
		cal, id, _ := strings.Cut(strings.TrimPrefix(path, "/calendars/"), "/events/")
		f.events[cal] = slices.DeleteFunc(f.events[cal], func(e fakeEvent) bool { return e.ID == id })
		f.deletes = append(f.deletes, cal+"/"+id+" sendUpdates="+r.URL.Query().Get("sendUpdates"))
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}
}

func fakeAPI(t *testing.T, f *fakeCalendarAPI) *liveAPI {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &liveAPI{hc: srv.Client(), base: srv.URL}
}

// ownScratch is a calendar this driver made for the account: its title,
// its marker, and the account as owner and data owner.
func ownScratch(id string) calendarEntry {
	return calendarEntry{
		ID: id, Summary: scratchTitle, AccessRole: "owner", DataOwner: selfAddr,
		Description: "Created by the google-calendar-mcp live driver. Safe to delete. " + scratchMarker,
	}
}

// A calendar is adopted, and emptied, only with the title, the marker,
// and the account as its owner and data owner. Any one missing, the run
// creates its own and touches nothing on the other.
func TestOnlyACalendarTheDriverMadeIsAdopted(t *testing.T) {
	const other = "other@group.calendar.example.test"
	for _, c := range []struct {
		name  string
		entry func(calendarEntry) calendarEntry
	}{
		{"the title alone", func(e calendarEntry) calendarEntry {
			e.Description = "Created by the google-calendar-mcp live driver. Safe to delete."
			return e
		}},
		{"somebody else's, shared as owner", func(e calendarEntry) calendarEntry {
			e.DataOwner = guestAddr
			return e
		}},
		{"no data owner given", func(e calendarEntry) calendarEntry {
			e.DataOwner = ""
			return e
		}},
		{"shared to write", func(e calendarEntry) calendarEntry {
			e.AccessRole = "writer"
			return e
		}},
		{"another title", func(e calendarEntry) calendarEntry {
			e.Summary = scratchTitle + " copy"
			return e
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeCalendarAPI{
				list:   []calendarEntry{c.entry(ownScratch(other))},
				events: map[string][]fakeEvent{other: {{ID: "theirs"}}},
			}
			id, created, _, err := fakeAPI(t, f).ensureScratchCalendar(context.Background(), scratchTitle, selfAddr)
			if err != nil {
				t.Fatal(err)
			}
			if id != freshCalendar || !created || f.created != 1 {
				t.Fatalf("adopted %s (created %v), want a calendar of its own", id, created)
			}
			if len(f.deletes) > 0 || len(f.events[other]) != 1 {
				t.Fatalf("emptied a calendar it did not make: %v", f.deletes)
			}
		})
	}
	t.Run("the driver's own", func(t *testing.T) {
		own := ownScratch(other)
		own.DataOwner = "SELF@example.org"
		f := &fakeCalendarAPI{
			list:   []calendarEntry{own},
			events: map[string][]fakeEvent{other: {{ID: "leftover"}}},
		}
		id, created, _, err := fakeAPI(t, f).ensureScratchCalendar(context.Background(), scratchTitle, selfAddr)
		if err != nil {
			t.Fatal(err)
		}
		if id != other || created || f.created != 0 {
			t.Fatalf("got %s (created %v), want its own calendar adopted", id, created)
		}
		if want := []string{other + "/leftover sendUpdates=none"}; !slices.Equal(f.deletes, want) {
			t.Fatalf("deletes %v, want %v", f.deletes, want)
		}
	})
}

// Emptying an adopted calendar leaves every event with another guest on
// it, so a default run mails nobody. -sweep-spikes cancels them to their
// guests.
func TestEmptyingMailsNobodyUnlessASweepAsks(t *testing.T) {
	const own = "own@group.calendar.example.test"
	events := func() map[string][]fakeEvent {
		return map[string][]fakeEvent{own: {
			{ID: "plain"},
			{ID: "rsvp", Attendees: []map[string]string{{"email": "SELF@example.org"}}},
			{ID: "invited", Attendees: []map[string]string{{"email": selfAddr}, {"email": guestAddr}}},
		}}
	}
	restore := func(sweep bool, g *reachGuard) func() {
		wasSweep, wasReach := clearSpikeEvents, reach
		clearSpikeEvents, reach = sweep, g
		return func() { clearSpikeEvents, reach = wasSweep, wasReach }
	}

	t.Run("unarmed", func(t *testing.T) {
		defer restore(false, reachGuardFor(nil, false))()
		f := &fakeCalendarAPI{list: []calendarEntry{ownScratch(own)}, events: events()}
		_, _, kept, err := fakeAPI(t, f).ensureScratchCalendar(context.Background(), scratchTitle, selfAddr)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{own + "/plain sendUpdates=none", own + "/rsvp sendUpdates=none"}
		if !slices.Equal(f.deletes, want) || kept != 1 {
			t.Fatalf("deletes %v and kept %d, want %v and 1", f.deletes, kept, want)
		}
	})
	t.Run("swept", func(t *testing.T) {
		defer restore(true, reachGuardFor(nil, true))()
		f := &fakeCalendarAPI{list: []calendarEntry{ownScratch(own)}, events: events()}
		_, _, kept, err := fakeAPI(t, f).ensureScratchCalendar(context.Background(), scratchTitle, selfAddr)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(f.deletes, own+"/invited sendUpdates=all") || kept != 0 {
			t.Fatalf("deletes %v and kept %d, want the guest's event canceled to them", f.deletes, kept)
		}
	})
}
