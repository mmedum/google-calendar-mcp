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
