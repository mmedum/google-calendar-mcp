package gcal_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
)

// TestEventDateTimeIsAUnion: date and dateTime are mutually exclusive on
// the wire, and IsAllDay is how the rest of the server tells them apart.
func TestEventDateTimeIsAUnion(t *testing.T) {
	cases := []struct {
		in   gcal.EventDateTime
		want bool
	}{
		{gcal.EventDateTime{Date: "2026-03-20"}, true},
		{gcal.EventDateTime{DateTime: "2026-03-20T09:00:00Z"}, false},
		{gcal.EventDateTime{}, false},
		// Both set is not a shape Google sends; it must not read as
		// all-day, because the timestamp is the more specific claim.
		{gcal.EventDateTime{Date: "2026-03-20", DateTime: "2026-03-20T09:00:00Z"}, false},
	}
	for _, c := range cases {
		if got := c.in.IsAllDay(); got != c.want {
			t.Fatalf("IsAllDay(%+v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestOmitemptyKeepsAPatchMinimal: a field this server does not set must
// not be sent, or a patch silently clears it.
func TestOmitemptyKeepsAPatchMinimal(t *testing.T) {
	b, err := json.Marshal(gcal.Event{Summary: "Only a title"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, forbidden := range []string{"attendees", "recurrence", "start", "end", "description", "location"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("an unset field %q was serialised; a patch built from this would clear it: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "Only a title") {
		t.Fatalf("the field that was set is missing: %s", got)
	}
}

func TestAtLeastOrdersRoles(t *testing.T) {
	cases := []struct {
		role, want string
		ok         bool
	}{
		{gcal.RoleOwner, gcal.RoleWriter, true},
		{gcal.RoleWriter, gcal.RoleWriter, true},
		{gcal.RoleWriter, gcal.RoleOwner, false},
		{gcal.RoleReader, gcal.RoleWriter, false},
		{gcal.RoleFreeBusyReader, gcal.RoleReader, false},
		{gcal.RoleReader, gcal.RoleFreeBusyReader, true},
		{gcal.RoleNone, gcal.RoleReader, false},
		// A role Google adds later ranks lowest, so it is refused rather
		// than silently treated as sufficient.
		{"newRoleFromGoogle", gcal.RoleReader, false},
		// And nothing is "at least none", which would make every check
		// pass against an unknown requirement.
		{gcal.RoleOwner, gcal.RoleNone, false},
	}
	for _, c := range cases {
		if got := gcal.AtLeast(c.role, c.want); got != c.ok {
			t.Fatalf("AtLeast(%q, %q) = %v, want %v", c.role, c.want, got, c.ok)
		}
	}
}

func TestRoleMeansExplainsEveryRole(t *testing.T) {
	for _, role := range []string{
		gcal.RoleOwner, gcal.RoleWriter, gcal.RoleWriterWithoutPrivateData,
		gcal.RoleReader, gcal.RoleFreeBusyReader, gcal.RoleNone,
	} {
		got := gcal.RoleMeans(role)
		if got == "" || strings.Contains(got, role) {
			t.Fatalf("RoleMeans(%q) = %q; it must explain rather than echo", role, got)
		}
	}
	if gcal.RoleMeans("unknown") == "" {
		t.Fatal("an unrecognised role got no explanation at all")
	}
}

func TestAclScopeIsPublic(t *testing.T) {
	if !(gcal.AclScope{Type: gcal.ScopeTypeDefault}).IsPublic() {
		t.Fatal(`scope type "default" means anyone and must report as public`)
	}
	for _, tpe := range []string{gcal.ScopeTypeUser, gcal.ScopeTypeGroup, gcal.ScopeTypeDomain} {
		if (gcal.AclScope{Type: tpe}).IsPublic() {
			t.Fatalf("%q reported as public", tpe)
		}
	}
}

func TestSettingsLookup(t *testing.T) {
	s := gcal.Settings{Items: []gcal.Setting{
		{ID: gcal.SettingTimezone, Value: "Europe/Copenhagen"},
		{ID: gcal.SettingWeekStart, Value: "1"},
	}}
	if v, ok := s.Lookup(gcal.SettingTimezone); !ok || v != "Europe/Copenhagen" {
		t.Fatalf("Lookup(timezone) = %q, %v", v, ok)
	}
	if _, ok := s.Lookup("nothing"); ok {
		t.Fatal("Lookup found a setting that is not there")
	}
}

func TestEventRoundTrips(t *testing.T) {
	in := gcal.Event{
		ID: "ev", Summary: "Title", ETag: `"1"`,
		Start:     &gcal.EventDateTime{Date: "2026-03-20"},
		End:       &gcal.EventDateTime{Date: "2026-03-21"},
		Attendees: []gcal.EventAttendee{{Email: "a@example.test", ResponseStatus: gcal.ResponseAccepted}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out gcal.Event
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Start.Date != "2026-03-20" || len(out.Attendees) != 1 {
		t.Fatalf("round trip lost data: %+v", out)
	}
}

func TestValidEventID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		ok   bool
	}{
		{"base32hex", "livecaltimedprobe00000000000001", true},
		{"digits only", "01234", true},
		{"too short", "abcd", false},
		{"w is not base32hex", "abcdw", false},
		{"z is not base32hex", "zzzzz", false},
		{"uppercase", "ABCDE", false},
		{"an underscore is not in the grammar", "ev-weekly_20260324T130000Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := gcal.ValidEventID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("%q was refused: %v", tc.id, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%q was accepted", tc.id)
			}
		})
	}
}

func TestSplitOccurrenceID(t *testing.T) {
	for _, tc := range []struct {
		id, series, start string
		ok                bool
	}{
		{"ev-weekly_20260324T130000Z", "ev-weekly", "20260324T130000Z", true},
		{"ev-weekly_20260324t130000z", "ev-weekly", "20260324t130000z", true},
		{"ev-allday_20260320", "ev-allday", "20260320", true},
		// A series id, which must survive untouched.
		{"livecalrepeatprobe0000000000001", "", "", false},
		// An underscore that is not an occurrence start.
		{"ev-weekly_notadate", "", "", false},
		{"_20260324T130000Z", "", "", false},
	} {
		series, start, ok := gcal.SplitOccurrenceID(tc.id)
		if ok != tc.ok || series != tc.series || start != tc.start {
			t.Fatalf("SplitOccurrenceID(%q) = %q, %q, %v; want %q, %q, %v",
				tc.id, series, start, ok, tc.series, tc.start, tc.ok)
		}
	}
}

// §2.11: the id this server mints has to be one Google will accept, and
// the rule is not obvious — w, x, y and z are out, and Google answers an
// illegal id with "Invalid resource id value" naming neither the field
// nor the constraint. That cost a live run (§18 row 21).
func TestNewEventIDIsAlwaysLegal(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id, err := gcal.NewEventID()
		if err != nil {
			t.Fatalf("NewEventID: %v", err)
		}
		if err := gcal.ValidEventID(id); err != nil {
			t.Fatalf("minted an id Google would refuse: %v", err)
		}
		if seen[id] {
			t.Fatalf("minted the same id twice: %q", id)
		}
		seen[id] = true
	}
}

// An occurrence id has to round-trip through the grammar that reads one,
// or the two are two opinions about the same shape (§6.2).
func TestOccurrenceIDRoundTrips(t *testing.T) {
	for _, c := range []struct {
		name   string
		series string
		start  gcal.EventDateTime
		want   string
	}{
		{"timed, west of UTC", "abcdef0123",
			gcal.EventDateTime{DateTime: "2026-03-24T09:00:00-05:00"}, "abcdef0123_20260324T140000Z"},
		{"timed, east of UTC", "abcdef0123",
			gcal.EventDateTime{DateTime: "2026-03-24T14:00:00+01:00"}, "abcdef0123_20260324T130000Z"},
		{"all day carries no time", "abcdef0123",
			gcal.EventDateTime{Date: "2026-03-24"}, "abcdef0123_20260324"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := gcal.OccurrenceID(c.series, c.start)
			if err != nil {
				t.Fatalf("OccurrenceID: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
			series, _, ok := gcal.SplitOccurrenceID(got)
			if !ok || series != c.series {
				t.Fatalf("SplitOccurrenceID(%q) = %q, %v; the two disagree about the grammar",
					got, series, ok)
			}
		})
	}
}

func TestOccurrenceIDNeedsAStart(t *testing.T) {
	if _, err := gcal.OccurrenceID("abcdef0123", gcal.EventDateTime{}); err == nil {
		t.Fatal("an occurrence with no start must be refused")
	}
	if _, err := gcal.OccurrenceID("", gcal.EventDateTime{Date: "2026-03-24"}); err == nil {
		t.Fatal("an occurrence with no series must be refused")
	}
	if _, err := gcal.OccurrenceID("abcdef0123", gcal.EventDateTime{DateTime: "not a time"}); err == nil {
		t.Fatal("an unparseable start must be refused")
	}
}

// The patch type exists so "clear it" and "leave it" are different
// things on the wire. Event cannot say the difference at all.
func TestEventPatchDistinguishesClearingFromLeaving(t *testing.T) {
	empty := ""
	both, err := json.Marshal(gcal.EventPatch{Location: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(both), `"location":""`) {
		t.Fatalf("clearing a field must be sent as empty, got %s", both)
	}
	none, err := json.Marshal(gcal.EventPatch{})
	if err != nil {
		t.Fatal(err)
	}
	if string(none) != "{}" {
		t.Fatalf("an empty patch must send nothing, got %s", none)
	}
	// And the same value on Event is dropped, which is the reason the
	// patch type exists at all.
	ev, err := json.Marshal(gcal.Event{Location: ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ev), "location") {
		t.Fatalf("Event was expected to drop an empty location: %s", ev)
	}
}

// An empty, non-nil recurrence list ends the repetition; a nil one
// leaves it alone.
func TestEventPatchCanEndARecurrence(t *testing.T) {
	stop := []string{}
	out, err := json.Marshal(gcal.EventPatch{Recurrence: &stop})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"recurrence":[]`) {
		t.Fatalf("an empty recurrence must be sent, got %s", out)
	}
}
