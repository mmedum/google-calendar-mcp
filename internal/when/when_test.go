package when_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// Zones chosen for what each one proves, not for variety:
//
//	Copenhagen   northern DST, +01/+02
//	Chicago      west of UTC, where the all-day bug shows
//	Auckland     southern DST, so the transitions run the other way
//	Kathmandu    +05:45, a non-hour offset
//	Kolkata      +05:30, no DST at all
//	Lord Howe    a 30-minute DST shift, which is the one that breaks
//	             code assuming a transition is always an hour
const (
	tzCopenhagen = "Europe/Copenhagen"
	tzChicago    = "America/Chicago"
	tzAuckland   = "Pacific/Auckland"
	tzKathmandu  = "Asia/Kathmandu"
	tzKolkata    = "Asia/Kolkata"
	tzLordHowe   = "Australia/Lord_Howe"
	tzUTC        = "UTC"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := when.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

// TestDateIsNotAnInstant is the package's reason for existing. A Date
// renders the same string no matter which zone is in play, because it
// has no instant to shift.
func TestDateIsNotAnInstant(t *testing.T) {
	d := when.MustParseDate("2026-03-14")
	for _, tz := range []string{tzUTC, tzChicago, tzAuckland, tzKathmandu, tzLordHowe} {
		t.Run(tz, func(t *testing.T) {
			if got := d.String(); got != "2026-03-14" {
				t.Fatalf("Date.String() = %q in the presence of %s, want 2026-03-14", got, tz)
			}
		})
	}
}

// TestAllDayEventDoesNotSlipWestOfUTC is §3's first row, as a test.
//
// The defect being guarded against: an all-day event stored as midnight
// UTC and then read in a negative-offset zone renders as the day
// before. Chicago is UTC-5 or UTC-6, so midnight UTC is 18:00 or 19:00
// on the *previous* day there.
func TestAllDayEventDoesNotSlipWestOfUTC(t *testing.T) {
	d := when.MustParseDate("2026-03-14")
	chicago := mustLoad(t, tzChicago)

	// What the broken implementations do, reproduced so the test says
	// what it is preventing rather than only asserting the good case.
	midnightUTC := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	slipped := when.NewZoned(midnightUTC, chicago).Date()
	if slipped.String() != "2026-03-13" {
		t.Fatalf("precondition: expected the naive UTC-midnight approach to slip to 2026-03-13 in %s, got %s",
			tzChicago, slipped)
	}

	// What this package does: the Date never became an instant, so there
	// is nothing to slip.
	if d.String() != "2026-03-14" {
		t.Fatalf("Date slipped: got %s, want 2026-03-14", d)
	}
	// And when a zone is supplied explicitly, midnight is Chicago's.
	start := d.StartIn(chicago)
	if start.Year() != 2026 || start.Month() != time.March || start.Day() != 14 || start.Hour() != 0 {
		t.Fatalf("StartIn(%s) = %s, want midnight on 2026-03-14", tzChicago, start)
	}
}

// TestDayWindowSpansTheTransition catches the other half of the same
// family: a day is not always 24 hours.
func TestDayWindowSpansTheTransition(t *testing.T) {
	cases := []struct {
		name string
		tz   string
		date string
		want time.Duration
	}{
		{"spring forward loses an hour", tzCopenhagen, "2026-03-29", 23 * time.Hour},
		{"autumn back gains an hour", tzCopenhagen, "2026-10-25", 25 * time.Hour},
		{"ordinary day", tzCopenhagen, "2026-06-15", 24 * time.Hour},
		{"southern spring forward", tzAuckland, "2026-09-27", 23 * time.Hour},
		{"southern autumn back", tzAuckland, "2026-04-05", 25 * time.Hour},
		{"no DST at all", tzKolkata, "2026-03-29", 24 * time.Hour},
		// Lord Howe shifts by 30 minutes, not an hour. Code that
		// assumes a transition is always an hour passes every case
		// above and fails these two.
		{"half-hour shift forward", tzLordHowe, "2026-10-04", 23*time.Hour + 30*time.Minute},
		{"half-hour shift back", tzLordHowe, "2026-04-05", 24*time.Hour + 30*time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loc := mustLoad(t, c.tz)
			w := when.DayWindow(when.MustParseDate(c.date), loc)
			if got := w.Duration(); got != c.want {
				t.Fatalf("DayWindow(%s, %s).Duration() = %s, want %s", c.date, c.tz, got, c.want)
			}
		})
	}
}

// TestWallStrictReportsAmbiguity covers the two readings a year that are
// not one instant. Go resolves both silently; this package does not.
func TestWallStrictReportsAmbiguity(t *testing.T) {
	cases := []struct {
		name          string
		tz            string
		y             int
		mo            time.Month
		d, h, mi      int
		want          when.Ambiguity
		wantNoteEmpty bool
	}{
		{"ordinary morning", tzCopenhagen, 2026, time.June, 15, 9, 0, when.Unique, true},
		{"inside the spring gap", tzCopenhagen, 2026, time.March, 29, 2, 30, when.Skipped, false},
		{"inside the autumn fold", tzCopenhagen, 2026, time.October, 25, 2, 30, when.Repeated, false},
		{"southern gap", tzAuckland, 2026, time.September, 27, 2, 30, when.Skipped, false},
		{"southern fold", tzAuckland, 2026, time.April, 5, 2, 30, when.Repeated, false},
		{"zone with no transitions", tzKolkata, 2026, time.March, 29, 2, 30, when.Unique, true},
		{"non-hour offset, ordinary time", tzKathmandu, 2026, time.March, 29, 2, 30, when.Unique, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loc := mustLoad(t, c.tz)
			_, got := when.WallStrict(c.y, c.mo, c.d, c.h, c.mi, loc)
			if got != c.want {
				t.Fatalf("WallStrict(%s %d-%02d-%02d %02d:%02d) = %v, want %v",
					c.tz, c.y, c.mo, c.d, c.h, c.mi, got, c.want)
			}
			if gotEmpty := got.Note() == ""; gotEmpty != c.wantNoteEmpty {
				t.Fatalf("Note() empty = %v, want %v (note: %q)", gotEmpty, c.wantNoteEmpty, got.Note())
			}
		})
	}
}

// TestZonedKeepsTheZoneNotJustTheOffset is §2.2: a recurrence needs the
// rule, not one moment's offset.
func TestZonedKeepsTheZoneNotJustTheOffset(t *testing.T) {
	cph := mustLoad(t, tzCopenhagen)

	// A winter 09:00 and a summer 09:00 are the same wall clock and
	// different offsets. Both must report the zone, because that is what
	// goes on the wire alongside dateTime.
	winter := when.Wall(2026, time.January, 15, 9, 0, cph)
	summer := when.Wall(2026, time.July, 15, 9, 0, cph)

	if winter.ZoneName() != tzCopenhagen || summer.ZoneName() != tzCopenhagen {
		t.Fatalf("ZoneName lost: winter %q summer %q", winter.ZoneName(), summer.ZoneName())
	}
	_, winterOff := winter.T.Zone()
	_, summerOff := summer.T.Zone()
	if winterOff == summerOff {
		t.Fatalf("precondition: expected different offsets across the year in %s, both were %d", tzCopenhagen, winterOff)
	}
	if winter.T.Hour() != 9 || summer.T.Hour() != 9 {
		t.Fatalf("wall clock moved: winter %s summer %s", winter, summer)
	}
}

// TestWeeklyRecurrenceHoldsTheWallClock is the DST-drift defect from §3,
// reduced to the arithmetic that causes it.
//
// Adding 7*24h to an instant crosses a transition and lands an hour out.
// Stepping the wall clock in the zone does not.
func TestWeeklyRecurrenceHoldsTheWallClock(t *testing.T) {
	cph := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.March, 24, 9, 0, cph) // the Tuesday before the transition

	// The naive way, which is what a UTC-instant implementation does.
	drifted := start.T.Add(7 * 24 * time.Hour).In(cph)
	if drifted.Hour() == 9 {
		t.Fatalf("precondition: expected instant arithmetic to drift across the %s transition, it did not", tzCopenhagen)
	}
	if drifted.Hour() != 10 {
		t.Fatalf("precondition: expected a one-hour drift to 10:00, got %s", drifted.Format(time.RFC3339))
	}

	// The zoned way: same wall clock, a week later.
	next := when.Wall(2026, time.March, 31, 9, 0, cph)
	if next.T.Hour() != 9 {
		t.Fatalf("zoned step drifted: %s", next)
	}
	if next.T.Sub(start.T) != 7*24*time.Hour-time.Hour {
		t.Fatalf("expected the week containing the transition to be an hour short on the timeline, got %s",
			next.T.Sub(start.T))
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"2026-03-14", false},
		{" 2026-03-14 ", false},
		{"2026-02-29", true}, // 2026 is not a leap year
		{"2026-3-14", true},
		{"14-03-2026", true},
		{"2026-03-14T00:00:00Z", true},
		{"", true},
		{"not a date", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := when.ParseDate(c.in)
			if got := err != nil; got != c.wantErr {
				t.Fatalf("ParseDate(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
			if err != nil && !errors.Is(err, when.ErrInvalid) {
				t.Fatalf("ParseDate(%q) error does not wrap ErrInvalid: %v", c.in, err)
			}
		})
	}
}

func TestParseZonedRequiresAnOffset(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"2026-03-14T09:00:00Z", false},
		{"2026-03-14T09:00:00+02:00", false},
		{"2026-03-14T09:00:00", true}, // no offset
		{"2026-03-14", true},          // a date, not a time
		{"", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := when.ParseZoned(c.in, nil)
			if got := err != nil; got != c.wantErr {
				t.Fatalf("ParseZoned(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			}
		})
	}
}

// TestParseZonedRendersInTheGivenZoneWithoutMovingTheInstant is what
// makes "show me their Thursday in my zone" safe.
func TestParseZonedRendersInTheGivenZoneWithoutMovingTheInstant(t *testing.T) {
	chicago := mustLoad(t, tzChicago)
	z, err := when.ParseZoned("2026-03-14T09:00:00+01:00", chicago)
	if err != nil {
		t.Fatalf("ParseZoned: %v", err)
	}
	if z.ZoneName() != tzChicago {
		t.Fatalf("ZoneName = %q, want %q", z.ZoneName(), tzChicago)
	}
	if !z.T.Equal(time.Date(2026, 3, 14, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("instant moved: %s", z.T.UTC())
	}
	// 09:00+01:00 is 08:00 UTC. US DST begins on the second Sunday of
	// March, which in 2026 is the 8th, so Chicago is already on CDT
	// (UTC-5) by the 14th and reads 03:00. Picking UTC-6 here is the
	// same class of mistake the package exists to catch, one layer up.
	if z.T.Hour() != 3 {
		t.Fatalf("wall clock in %s = %02d:00, want 03:00", tzChicago, z.T.Hour())
	}
}

func TestLoadLocationRejectsLocalAndEmpty(t *testing.T) {
	for _, name := range []string{"", "   ", "Local", "local", "LOCAL"} {
		t.Run("reject "+name, func(t *testing.T) {
			if _, err := when.LoadLocation(name); err == nil {
				t.Fatalf("LoadLocation(%q) succeeded; the process's own zone must never be a source", name)
			}
		})
	}
	for _, name := range []string{tzUTC, tzCopenhagen, tzKathmandu, tzLordHowe} {
		t.Run("accept "+name, func(t *testing.T) {
			if _, err := when.LoadLocation(name); err != nil {
				t.Fatalf("LoadLocation(%q): %v", name, err)
			}
		})
	}
}

func TestDateArithmetic(t *testing.T) {
	cases := []struct {
		in   string
		days int
		want string
	}{
		{"2026-03-14", 1, "2026-03-15"},
		{"2026-03-14", -1, "2026-03-13"},
		{"2026-12-31", 1, "2027-01-01"},
		{"2027-01-01", -1, "2026-12-31"},
		{"2028-02-28", 1, "2028-02-29"}, // 2028 is a leap year
		{"2026-02-28", 1, "2026-03-01"}, // 2026 is not
		// A day containing a DST transition is still one day forward.
		{"2026-03-29", 1, "2026-03-30"},
		{"2026-10-25", 1, "2026-10-26"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := when.MustParseDate(c.in).AddDays(c.days).String(); got != c.want {
				t.Fatalf("%s.AddDays(%d) = %s, want %s", c.in, c.days, got, c.want)
			}
		})
	}
}

func TestDateOrdering(t *testing.T) {
	a := when.MustParseDate("2026-03-14")
	b := when.MustParseDate("2026-03-15")
	c := when.MustParseDate("2026-04-01")
	d := when.MustParseDate("2027-01-01")
	for _, p := range [][2]when.Date{{a, b}, {b, c}, {c, d}, {a, d}} {
		if !p[0].Before(p[1]) {
			t.Fatalf("%s.Before(%s) = false", p[0], p[1])
		}
		if !p[1].After(p[0]) {
			t.Fatalf("%s.After(%s) = false", p[1], p[0])
		}
	}
	if a.Before(a) || a.After(a) {
		t.Fatalf("%s compares unequal to itself", a)
	}
}

func TestWindowIsHalfOpen(t *testing.T) {
	cph := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.June, 15, 9, 0, cph)
	end := when.Wall(2026, time.June, 15, 10, 0, cph)
	w, err := when.NewWindow(start, end, cph)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if !w.Contains(start) {
		t.Fatal("window excludes its own start; it must be closed at the start")
	}
	if w.Contains(end) {
		t.Fatal("window includes its own end; it must be open at the end")
	}

	// Back-to-back meetings do not overlap. This is the property the
	// free-gap arithmetic in phase 1 will stand on.
	next, err := when.NewWindow(end, when.Wall(2026, time.June, 15, 11, 0, cph), cph)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if w.Overlaps(next) {
		t.Fatal("back-to-back windows reported as overlapping")
	}
}

func TestNewWindowRejectsAnEmptyOrBackwardsRange(t *testing.T) {
	cph := mustLoad(t, tzCopenhagen)
	at9 := when.Wall(2026, time.June, 15, 9, 0, cph)
	at10 := when.Wall(2026, time.June, 15, 10, 0, cph)
	for _, c := range []struct {
		name string
		a, b when.Zoned
	}{
		{"backwards", at10, at9},
		{"empty", at9, at9},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := when.NewWindow(c.a, c.b, cph); err == nil {
				t.Fatalf("NewWindow accepted a %s range", c.name)
			}
		})
	}
}

func TestZonedDateProjectsInItsOwnZone(t *testing.T) {
	chicago := mustLoad(t, tzChicago)
	auckland := mustLoad(t, tzAuckland)

	// One instant, two zones, two different local days. Both are right;
	// the point is that Date() answers for the zone it is read in.
	instant := time.Date(2026, 3, 14, 2, 0, 0, 0, time.UTC)
	if got := when.NewZoned(instant, chicago).Date().String(); got != "2026-03-13" {
		t.Fatalf("in %s the instant falls on %s, want 2026-03-13", tzChicago, got)
	}
	if got := when.NewZoned(instant, auckland).Date().String(); got != "2026-03-14" {
		t.Fatalf("in %s the instant falls on %s, want 2026-03-14", tzAuckland, got)
	}
}

func TestClockIsInjected(t *testing.T) {
	fixed := time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC)
	c := when.FixedClock(fixed)
	if !c().Equal(fixed) {
		t.Fatalf("FixedClock moved: %s", c())
	}
	if when.SystemClock() == nil {
		t.Fatal("SystemClock returned nil")
	}
}
