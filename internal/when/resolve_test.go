package when_test

import (
	"errors"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// TestResolveOrder holds §4.1's order: the call, then the calendar, then
// the user's settings. Every case names which source should win.
func TestResolveOrder(t *testing.T) {
	cases := []struct {
		name                     string
		call, calendar, settings string
		wantZone                 string
		wantSource               when.ZoneSource
	}{
		{"the call wins", tzChicago, tzCopenhagen, tzAuckland, tzChicago, when.ZoneFromCall},
		{"the calendar is next", "", tzCopenhagen, tzAuckland, tzCopenhagen, when.ZoneFromCalendar},
		{"settings are last", "", "", tzAuckland, tzAuckland, when.ZoneFromSettings},
		{"only the calendar", "", tzKathmandu, "", tzKathmandu, when.ZoneFromCalendar},
		{"only the call", tzKolkata, "", "", tzKolkata, when.ZoneFromCall},
		// A calendar carrying a zone Go cannot load must not take the
		// whole call down: the caller cannot fix another calendar's
		// stored zone, so fall through and say what was used.
		{"a broken calendar zone falls through", "", "Mars/Olympus_Mons", tzCopenhagen, tzCopenhagen, when.ZoneFromSettings},
		{"a broken settings zone falls through to nothing", "", "", "Mars/Olympus_Mons", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			z, err := when.Resolve(c.call, c.calendar, c.settings)
			if c.wantZone == "" {
				if err == nil {
					t.Fatalf("Resolve returned %q, want an error", z.Name())
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if z.Name() != c.wantZone {
				t.Fatalf("zone = %q, want %q", z.Name(), c.wantZone)
			}
			if z.Source != c.wantSource {
				t.Fatalf("source = %q, want %q", z.Source, c.wantSource)
			}
			if z.Explain() == "" {
				t.Fatal("Explain() is empty; every read must be able to say which zone it used")
			}
		})
	}
}

// TestResolveRefusesRatherThanFallingBack is §3's last row: the server's
// own zone is never a source.
func TestResolveRefusesRatherThanFallingBack(t *testing.T) {
	_, err := when.Resolve("", "", "")
	if err == nil {
		t.Fatal("Resolve with no sources succeeded; it must refuse rather than pick UTC or the process zone")
	}
	if !errors.Is(err, when.ErrNoZone) {
		t.Fatalf("error = %v, want ErrNoZone", err)
	}
	if !errors.Is(err, when.ErrInvalid) {
		t.Fatalf("ErrNoZone must wrap ErrInvalid; got %v", err)
	}
}

// TestResolveReportsABadZoneTheCallerNamed: a zone the caller typed is
// theirs to fix, so it is an error rather than a fall-through.
func TestResolveReportsABadZoneTheCallerNamed(t *testing.T) {
	if _, err := when.Resolve("Mars/Olympus_Mons", tzCopenhagen, tzCopenhagen); err == nil {
		t.Fatal("a zone named on the call must be reported, not silently replaced")
	}
	if _, err := when.Resolve("Local", tzCopenhagen, ""); err == nil {
		t.Fatal(`"Local" named on the call must be refused`)
	}
}

func TestZoneExplainNamesTheSource(t *testing.T) {
	for _, c := range []struct{ call, cal, set, want string }{
		{tzChicago, "", "", "as requested"},
		{"", tzChicago, "", "the calendar's own time zone"},
		{"", "", tzChicago, "from your Google Calendar settings"},
	} {
		z, err := when.Resolve(c.call, c.cal, c.set)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := z.Explain(); !contains(got, c.want) {
			t.Fatalf("Explain() = %q, want it to mention %q", got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
