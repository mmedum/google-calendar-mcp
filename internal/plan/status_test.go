package plan_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/plan"
)

// statusDraft is a timed draft on the primary calendar carrying s.
func statusDraft(t *testing.T, s plan.Status) plan.Draft {
	t.Helper()
	s.OnPrimary = true
	return plan.Draft{
		Title: ptr("Away"), Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T17:00:00+02:00",
		Zone: zone(t), Status: &s,
	}
}

// Each status type is sent with its type, its details block and the
// transparency and visibility Google's guide requires, whatever the
// caller passed for them.
func TestAStatusEventIsBuiltAsGoogleRequires(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status             plan.Status
		eventType, block   string
		transparency, seen string
	}{
		{"out of office", plan.Status{Type: "outOfOffice", AutoDecline: "new", DeclineMessage: "Back Monday"},
			gcal.EventTypeOutOfOffice,
			`{"autoDeclineMode":"declineOnlyNewConflictingInvitations","declineMessage":"Back Monday"}`,
			gcal.TransparencyOpaque, ""},
		{"focus time", plan.Status{Type: "focusTime", AutoDecline: "none", ChatStatus: "do_not_disturb"},
			gcal.EventTypeFocusTime, `{"autoDeclineMode":"declineNone","chatStatus":"doNotDisturb"}`,
			gcal.TransparencyOpaque, ""},
		{"any case", plan.Status{Type: "OUTOFOFFICE", AutoDecline: "all"},
			gcal.EventTypeOutOfOffice, `{"autoDeclineMode":"declineAllConflictingInvitations"}`,
			gcal.TransparencyOpaque, ""},
		{"home", plan.Status{Type: "workingLocation", WorkingLocation: "home"},
			gcal.EventTypeWorkingLocation, `{"type":"homeOffice","homeOffice":{}}`,
			gcal.TransparencyTransparent, gcal.VisibilityPublic},
		{"office", plan.Status{Type: "workingLocation", WorkingLocation: "office", Label: "Annex"},
			gcal.EventTypeWorkingLocation, `{"type":"officeLocation","officeLocation":{"label":"Annex"}}`,
			gcal.TransparencyTransparent, gcal.VisibilityPublic},
		{"custom", plan.Status{Type: "workingLocation", WorkingLocation: "custom", Label: "Library"},
			gcal.EventTypeWorkingLocation, `{"type":"customLocation","customLocation":{"label":"Library"}}`,
			gcal.TransparencyTransparent, gcal.VisibilityPublic},
	} {
		e, err := plan.Insert("abcde12345", statusDraft(t, tc.status))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if e.EventType != tc.eventType || string(e.StatusDetails()) != tc.block {
			t.Errorf("%s: got %s %s, want %s %s", tc.name, e.EventType, e.StatusDetails(), tc.eventType, tc.block)
		}
		if e.Transparency != tc.transparency || e.Visibility != tc.seen {
			t.Errorf("%s: got transparency %q and visibility %q, want %q and %q",
				tc.name, e.Transparency, e.Visibility, tc.transparency, tc.seen)
		}
	}
}

// An ordinary event stays one: no event_type, or the word default.
func TestDefaultIsAnOrdinaryEvent(t *testing.T) {
	e, err := plan.Insert("abcde12345", statusDraft(t, plan.Status{Type: "default"}))
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if e.EventType != gcal.EventTypeDefault || e.StatusDetails() != nil || e.Transparency != "" {
		t.Fatalf("got type %q, details %s, transparency %q; want an ordinary event",
			e.EventType, e.StatusDetails(), e.Transparency)
	}
}

// An all-day working location is one whole day: the same date as start
// and end, which Google's exclusive end makes the day after.
func TestAnAllDayWorkingLocationIsOneDay(t *testing.T) {
	d := statusDraft(t, plan.Status{Type: "workingLocation", WorkingLocation: "home"})
	d.Start, d.End = "2026-04-01", "2026-04-01"
	e, err := plan.Insert("abcde12345", d)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if e.Start.Date != "2026-04-01" || e.End.Date != "2026-04-02" {
		t.Fatalf("got %s to %s, want 2026-04-01 to 2026-04-02", e.Start.Date, e.End.Date)
	}
}

// What Google's guide says it refuses is refused before a request, and so
// is what this server has not checked: guests, rooms and a Meet link.
func TestAStatusEventGoogleWouldRefuseIsRefused(t *testing.T) {
	away := plan.Status{Type: "outOfOffice", AutoDecline: "none"}
	working := plan.Status{Type: "workingLocation", WorkingLocation: "home"}
	with := func(s plan.Status, change func(*plan.Status)) plan.Status {
		change(&s)
		return s
	}
	for _, tc := range []struct {
		name   string
		status plan.Status
		draft  func(*plan.Draft)
		want   error
		says   string
	}{
		{"a secondary calendar", away, func(d *plan.Draft) { d.Status.OnPrimary = false },
			plan.ErrUnsupported, "only go on your primary calendar"},
		{"guests", away, func(d *plan.Draft) { d.AddGuests = []string{"colleague@example.test"} },
			plan.ErrBlocked, "does not put guests, rooms or a Meet link"},
		{"optional guests", away, func(d *plan.Draft) { d.AddOptional = []string{"colleague@example.test"} },
			plan.ErrBlocked, "does not put guests"},
		{"a room", working, func(d *plan.Draft) {
			d.AddRooms = []string{"room-sample@resource.calendar.google.com"}
		}, plan.ErrBlocked, "does not put guests"},
		{"a Meet link", away, func(d *plan.Draft) { d.Conference = true }, plan.ErrBlocked, "Meet link"},
		{"all-day out of office", away, func(d *plan.Draft) { d.Start, d.End = "2026-04-01", "2026-04-01" },
			plan.ErrUnsupported, "an out-of-office event cannot be all day"},
		{"all-day focus time", with(away, func(s *plan.Status) { s.Type = "focusTime" }),
			func(d *plan.Draft) { d.Start, d.End = "2026-04-01", "2026-04-01" },
			plan.ErrUnsupported, "focus time cannot be all day"},
		{"a two-day working location", working, func(d *plan.Draft) { d.Start, d.End = "2026-04-01", "2026-04-02" },
			plan.ErrUnsupported, "covers exactly one day"},
		{"free on out of office", away, func(d *plan.Draft) { d.Transparent = ptr(true) },
			plan.ErrInvalid, "always shows you as busy"},
		{"a private working location", working, func(d *plan.Draft) { d.Visibility = ptr("private") },
			plan.ErrInvalid, "always public"},
		{"no auto_decline", with(away, func(s *plan.Status) { s.AutoDecline = "" }), nil,
			plan.ErrInvalid, "auto_decline is required for an out-of-office event, and there is no default"},
		{"an auto_decline that is not one", with(away, func(s *plan.Status) { s.AutoDecline = "some" }), nil,
			plan.ErrInvalid, "Pass none, new or all"},
		{"a message that goes nowhere", with(away, func(s *plan.Status) { s.DeclineMessage = "Away" }), nil,
			plan.ErrInvalid, "auto_decline none declines nothing"},
		{"chat on out of office", with(away, func(s *plan.Status) { s.ChatStatus = "available" }), nil,
			plan.ErrInvalid, "chat_status belongs to focus time"},
		{"a chat status that is not one", plan.Status{Type: "focusTime", AutoDecline: "none", ChatStatus: "busy"},
			nil, plan.ErrInvalid, "Pass available or do_not_disturb"},
		{"a place on out of office", with(away, func(s *plan.Status) { s.WorkingLocation = "home" }), nil,
			plan.ErrInvalid, "belong to a working location"},
		{"no working_location", with(working, func(s *plan.Status) { s.WorkingLocation = "" }), nil,
			plan.ErrInvalid, "working_location is required"},
		{"a working_location that is not one", with(working, func(s *plan.Status) { s.WorkingLocation = "moon" }),
			nil, plan.ErrInvalid, "Pass home, office or custom"},
		{"a label at home", with(working, func(s *plan.Status) { s.Label = "Kitchen" }), nil,
			plan.ErrInvalid, "home has no label"},
		{"a decline on a working location", with(working, func(s *plan.Status) { s.AutoDecline = "all" }), nil,
			plan.ErrInvalid, "a working location declines nothing"},
		{"chat on a working location", with(working, func(s *plan.Status) { s.ChatStatus = "available" }), nil,
			plan.ErrInvalid, "chat_status belongs to focus time"},
		{"a setting without a type", plan.Status{AutoDecline: "all"}, nil,
			plan.ErrInvalid, "auto_decline belongs to a status event"},
		{"a type create_event does not make", plan.Status{Type: "birthday"}, nil,
			plan.ErrInvalid, `"birthday" is not an event type create_event makes`},
	} {
		d := statusDraft(t, tc.status)
		if tc.draft != nil {
			tc.draft(&d)
		}
		_, err := plan.Insert("abcde12345", d)
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: got %v, want %v saying %q", tc.name, err, tc.want, tc.says)
		}
	}
}
