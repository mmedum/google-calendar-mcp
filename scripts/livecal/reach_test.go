//go:build live

package main

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// These hold §9.1's promise that a default run mails nobody, without a
// network or an account. example.com and its siblings stand for real
// people here; addresses under .test are the driver's invented ones.

const (
	selfAddr  = "self@example.org"
	guestAddr = "guest@example.com"
)

func reachGuardFor(invitees []string, sweep bool) *reachGuard {
	g := &reachGuard{}
	g.setSelf(selfAddr)
	g.arm(invitees, sweep)
	return g
}

// Unarmed, a tool call naming anybody but the account itself or an
// address under .test does not go out, however it names them.
func TestAnUnarmedRunCallsNoToolThatReachesAPerson(t *testing.T) {
	g := reachGuardFor(nil, false)
	for _, c := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"a guest", "create_event", map[string]any{"guests": []string{guestAddr}}},
		{"an optional guest", "create_event", map[string]any{"optional_guests": []string{guestAddr}}},
		{"a room", "create_event", map[string]any{"rooms": []string{guestAddr}}},
		{"an added guest", "update_event", map[string]any{"add_guests": []string{guestAddr}}},
		{"an added optional guest", "update_event", map[string]any{"add_optional_guests": []string{guestAddr}}},
		{"an added room", "update_event", map[string]any{"add_rooms": []string{guestAddr}}},
		{"a removed guest", "update_event", map[string]any{"remove_guests": []string{guestAddr}}},
		{"a share", "share_calendar", map[string]any{"who": guestAddr}},
		{"a mailbox", "update_event", map[string]any{"add_guests": []string{`"Guest" <` + guestAddr + `>`}}},
		{"behind an invented one", "update_event",
			map[string]any{"add_guests": []string{"nobody@example.test, " + guestAddr}}},
		{"a list as JSON decodes it", "create_event", map[string]any{"guests": []any{guestAddr}}},
		{"a shape it cannot read", "create_event", map[string]any{"guests": 7}},
	} {
		if err := g.tool(c.tool, c.args); err == nil {
			t.Errorf("%s: went out", c.name)
		}
	}
	for _, c := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"no guest", "create_event", map[string]any{"title": "Livecal probe"}},
		{"the account itself", "create_event", map[string]any{"optional_guests": []string{"SELF@example.org"}}},
		{"an invented address", "create_event", map[string]any{"guests": []string{"nobody@example.test"}}},
		{"an invented mailbox", "update_event",
			map[string]any{"add_guests": []string{`"Nobody" <nobody@example.test>`}}},
		{"the public", "publish_calendar", map[string]any{"who": "anyone"}},
		{"a read", "check_availability", map[string]any{"calendars": []string{guestAddr}}},
	} {
		if err := g.tool(c.tool, c.args); err != nil {
			t.Errorf("%s: refused: %v", c.name, err)
		}
	}
}

// Unarmed, none of the driver's own REST calls asks Google to mail
// anybody or names anybody but the account itself.
func TestAnUnarmedRunSendsNoRequestThatReachesAPerson(t *testing.T) {
	g := reachGuardFor(nil, false)
	guest := []map[string]any{{"email": guestAddr}}
	for _, c := range []struct {
		name, method, path string
		body               any
		allowed            bool
	}{
		{"a read", http.MethodGet, "/calendars/x/events?sendUpdates=all", nil, true},
		{"an event of its own", http.MethodPost, "/calendars/x/events?sendUpdates=none",
			map[string]any{"attendees": []map[string]any{{"email": selfAddr}}}, true},
		{"a guest", http.MethodPost, "/calendars/x/events?sendUpdates=none",
			map[string]any{"attendees": guest}, false},
		{"a guest as JSON decodes it", http.MethodPatch, "/calendars/x/events/y?sendUpdates=none",
			map[string]any{"attendees": []any{map[string]any{"email": guestAddr}}}, false},
		{"attendees it cannot read", http.MethodPost, "/calendars/x/events",
			map[string]any{"attendees": guestAddr}, false},
		{"a body it cannot read", http.MethodPost, "/calendars/x/events", []byte("{}"), false},
		{"a share with nobody", http.MethodPost, "/calendars/x/acl?sendNotifications=false",
			map[string]any{"scope": map[string]any{"type": "user", "value": "nobody@example.test"}}, true},
		{"a share with a person", http.MethodPost, "/calendars/x/acl?sendNotifications=false",
			map[string]any{"scope": map[string]any{"type": "user", "value": guestAddr}}, false},
		{"a share that notifies", http.MethodPost, "/calendars/x/acl?sendNotifications=true", nil, false},
		{"a cancellation that notifies", http.MethodDelete, "/calendars/x/events/y?sendUpdates=all", nil, false},
		{"an insert that notifies", http.MethodPost, "/calendars/x/events?sendUpdates=externalOnly", nil, false},
		{"a quiet delete", http.MethodDelete, "/calendars/x/events/y?sendUpdates=none", nil, true},
		{"a free/busy query", http.MethodPost, "/freeBusy",
			map[string]any{"items": []map[string]any{{"id": "x@group.calendar.example.test"}}}, true},
	} {
		err := g.rest(c.method, c.path, c.body)
		if (err == nil) != c.allowed {
			t.Errorf("%s: allowed=%v, want %v (%v)", c.name, err == nil, c.allowed, err)
		}
	}
}

// Armed, the run reaches the invitees it named and nobody else.
func TestAnArmedRunReachesOnlyTheNamedInvitees(t *testing.T) {
	g := reachGuardFor([]string{"GUEST@example.com"}, false)
	if err := g.tool("create_event", map[string]any{"guests": []string{guestAddr}, "notify": "all"}); err != nil {
		t.Errorf("the named invitee was refused: %v", err)
	}
	if err := g.rest(http.MethodPost, "/calendars/x/events?sendUpdates=all",
		map[string]any{"attendees": []map[string]any{{"email": guestAddr}}}); err != nil {
		t.Errorf("spike A's insert to the named invitee was refused: %v", err)
	}
	if err := g.tool("create_event", map[string]any{"guests": []string{"other@example.net"}}); err == nil {
		t.Error("a tool call reached somebody -spike-invitees did not name")
	}
	if err := g.rest(http.MethodPost, "/calendars/x/events?sendUpdates=all",
		map[string]any{"attendees": []map[string]any{{"email": guestAddr}, {"email": "other@example.net"}}}); err == nil {
		t.Error("a request reached somebody -spike-invitees did not name")
	}
}

// -sweep-spikes cancels kept events to their guests, and invites nobody.
func TestASweepCancelsButInvitesNobody(t *testing.T) {
	g := reachGuardFor(nil, true)
	if err := g.rest(http.MethodDelete, "/calendars/x/events/y?sendUpdates=all", nil); err != nil {
		t.Errorf("the sweep's cancellation was refused: %v", err)
	}
	if err := g.rest(http.MethodPost, "/calendars/x/events?sendUpdates=none",
		map[string]any{"attendees": []map[string]any{{"email": guestAddr}}}); err == nil {
		t.Error("a sweep invited somebody")
	}
}

// -spike-notify is refused unless -spike-invitees names exactly the
// configured guests.
func TestSpikeNotifyNeedsEveryInviteeNamed(t *testing.T) {
	two := guests{internal: guestAddr, external: "outside@example.net"}
	for _, c := range []struct {
		name   string
		notify bool
		list   string
		g      guests
		want   []string
		ok     bool
	}{
		{"unarmed", false, "", two, nil, true},
		{"invitees without -spike-notify", false, guestAddr, two, nil, false},
		{"no invitees", true, "", two, nil, false},
		{"no invitees and no guest", true, "", guests{}, nil, false},
		{"one configured guest not named", true, guestAddr, two, nil, false},
		{"one named guest not configured", true, guestAddr + ",outside@example.net,third@example.org", two, nil, false},
		{"no guest configured", true, guestAddr, guests{}, nil, false},
		{"exactly the configured guests", true, " Guest@example.com , outside@example.net,", two,
			[]string{guestAddr, "outside@example.net"}, true},
	} {
		got, err := armNotify(c.notify, c.list, c.g)
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", c.name, err, c.ok)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: invitees %q, want %q", c.name, got, c.want)
		}
	}
}

// The write steps that mail a real person are skipped, never called,
// when the run is not armed. Which steps those are is read from the
// steps themselves: every one that names the guest or an event made for
// the guest.
func TestAnUnarmedRunCallsNoRealGuestStep(t *testing.T) {
	const scratch = "scratch@group.calendar.example.test"
	w := &writeState{guest: guestAddr, guestEvent: "guestevent", quietEvent: "quietevent"}
	var reaching []step
	for _, st := range writeSteps(scratch, w) {
		args := st.arguments()
		named, _ := addresses(args["guests"])
		id, _ := args["event_id"].(string)
		if slices.Contains(named, guestAddr) || id == w.guestEvent || id == w.quietEvent {
			reaching = append(reaching, st)
		}
	}
	if len(reaching) < 5 {
		t.Fatalf("found %d steps that reach the guest, want at least 5: the test is not reading the steps",
			len(reaching))
	}
	w.guest = ""
	for _, st := range reaching {
		if st.skip == nil || st.skip() == "" {
			t.Errorf("%s runs unarmed", st.name)
		}
	}
}

// Both of the driver's paths to Google pass the guard: a write it
// refuses reaches neither the server nor the API.
func TestBothPathsToGooglePassTheReachGuard(t *testing.T) {
	was := reach
	reach = reachGuardFor(nil, false)
	defer func() { reach = was }()
	const cal = "x@group.calendar.example.test"
	invite := []map[string]any{{"email": guestAddr}}

	// The fake fails the test on any request: none is expected.
	api := fakeAPI(t, &fakeCalendarAPI{})
	if err := api.insertEvent(context.Background(), cal, map[string]any{"attendees": invite}); err == nil {
		t.Error("the driver's own insert inviting a person went out")
	}

	s := &session{stdin: refuseWrites{t}, stderr: &strings.Builder{}, nextID: 1}
	_, err := s.call(context.Background(), "create_event",
		map[string]any{"calendar": cal, "guests": []string{guestAddr}, "notify": "all"})
	if err == nil || !strings.Contains(err.Error(), "may not reach") {
		t.Errorf("a tool call inviting a person was not refused by the guard: %v", err)
	}
}

// refuseWrites is a server stdin that fails the test if anything is
// sent to it.
type refuseWrites struct{ t *testing.T }

func (r refuseWrites) Write(p []byte) (int, error) {
	r.t.Errorf("sent to the server: %s", p)
	return 0, io.ErrClosedPipe
}
func (refuseWrites) Close() error { return nil }
