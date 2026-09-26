package recur_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/recur"
	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// Zones, chosen for what each one proves (the same set internal/when
// tests against, for the same reasons).
const (
	tzCopenhagen = "Europe/Copenhagen"
	tzChicago    = "America/Chicago"
	tzAuckland   = "Pacific/Auckland"
	tzKathmandu  = "Asia/Kathmandu"
	tzLordHowe   = "Australia/Lord_Howe"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := when.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func mustParse(t *testing.T, lines ...string) recur.Set {
	t.Helper()
	set, err := recur.Parse(lines)
	if err != nil {
		t.Fatalf("Parse(%v): %v", lines, err)
	}
	return set
}

// walls renders occurrences as local wall clocks, which is what a
// drifting expansion gets wrong and an instant-based assertion hides.
func walls(times []when.Zoned) []string {
	out := make([]string, 0, len(times))
	for _, z := range times {
		out = append(out, z.T.Format("2006-01-02 15:04"))
	}
	return out
}

func wantWalls(t *testing.T, got []when.Zoned, want []string) {
	t.Helper()
	have := walls(got)
	if len(have) != len(want) {
		t.Fatalf("got %d occurrences %v, want %d %v", len(have), have, len(want), want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("occurrence %d = %s, want %s (all: %v)", i, have[i], want[i], have)
		}
	}
}

// TestWeeklyHoldsItsWallClockAcrossSpringForward is §3's second row and
// the reason this package expands dates rather than instants.
//
// The European transition is 29 March 2026. A weekly 09:00 must stay
// 09:00 on both sides of it.
func TestWeeklyHoldsItsWallClockAcrossSpringForward(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4")
	start := when.Wall(2026, time.March, 17, 9, 0, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{
		"2026-03-17 09:00",
		"2026-03-24 09:00",
		"2026-03-31 09:00", // after the 29 March transition
		"2026-04-07 09:00",
	})

	// The defect this prevents, reproduced: adding 7*24h to the instant
	// drifts an hour once the clocks go forward.
	drifted := start.T.Add(14 * 24 * time.Hour).In(loc)
	if drifted.Format("15:04") != "10:00" {
		t.Fatalf("precondition: expected the naive 14-day arithmetic to land at 10:00, got %s",
			drifted.Format("15:04"))
	}

	// And the instants really do differ, which is what makes the wall
	// clock a choice rather than an accident.
	gap := occ.Times[2].T.Sub(occ.Times[1].T)
	if gap != 167*time.Hour {
		t.Fatalf("the week containing the transition was %v, want 167h", gap)
	}
}

// TestWeeklyAcrossSouthernAutumn: the southern hemisphere runs the other
// way, and a sign error shows here and nowhere else. Auckland's clocks
// go back on 5 April 2026.
func TestWeeklyAcrossSouthernAutumn(t *testing.T) {
	loc := mustLoad(t, tzAuckland)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=WE;COUNT=3")
	start := when.Wall(2026, time.April, 1, 9, 0, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{
		"2026-04-01 09:00",
		"2026-04-08 09:00", // after the 5 April transition
		"2026-04-15 09:00",
	})
	if gap := occ.Times[1].T.Sub(occ.Times[0].T); gap != 169*time.Hour {
		t.Fatalf("the week containing the southern transition was %v, want 169h", gap)
	}
}

// TestHalfHourTransition: Lord Howe shifts by 30 minutes, which breaks
// anything that assumes a transition is a whole hour.
func TestHalfHourTransition(t *testing.T) {
	loc := mustLoad(t, tzLordHowe)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=SU;COUNT=3")
	start := when.Wall(2026, time.March, 29, 10, 0, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{
		"2026-03-29 10:00",
		"2026-04-05 10:00", // Lord Howe's clocks go back 30 minutes
		"2026-04-12 10:00",
	})
	if gap := occ.Times[1].T.Sub(occ.Times[0].T); gap != 168*time.Hour+30*time.Minute {
		t.Fatalf("the half-hour transition week was %v, want 168h30m", gap)
	}
}

// TestNonHourOffsetZoneWithoutTransitions: Kathmandu is +05:45 and never
// changes. Every week is exactly a week, and the wall clock holds.
func TestNonHourOffsetZoneWithoutTransitions(t *testing.T) {
	loc := mustLoad(t, tzKathmandu)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;COUNT=3")
	start := when.Wall(2026, time.March, 17, 9, 30, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{
		"2026-03-17 09:30",
		"2026-03-24 09:30",
		"2026-03-31 09:30",
	})
	for i, z := range occ.Times {
		if _, off := z.T.Zone(); off != 5*3600+45*60 {
			t.Fatalf("occurrence %d has offset %ds, want +05:45", i, off)
		}
	}
}

// TestOccurrenceInASkippedHourIsReported. A daily 02:30 in Copenhagen
// meets 29 March, a morning with no 02:30. The occurrence still exists —
// Google produces one — and §4.1 says the server names what the zone
// did rather than resolving it quietly.
func TestOccurrenceInASkippedHourIsReported(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=3")
	start := when.Wall(2026, time.March, 28, 2, 30, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	if occ.Count() != 3 {
		t.Fatalf("got %d occurrences, want 3", occ.Count())
	}
	if got := occ.Times[1].T.Format("2006-01-02 15:04"); got != "2026-03-29 03:30" {
		t.Fatalf("the occurrence in the missing hour resolved to %s, want 2026-03-29 03:30", got)
	}
	if len(occ.Notes) != 1 || !strings.Contains(occ.Notes[0], "does not exist") {
		t.Fatalf("Notes = %v, want one note about the missing hour", occ.Notes)
	}
}

func TestExpandTimesTable(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	cases := []struct {
		name  string
		rules []string
		start when.Zoned
		want  []string
	}{
		{
			name:  "daily with an interval",
			rules: []string{"RRULE:FREQ=DAILY;INTERVAL=3;COUNT=3"},
			start: when.Wall(2026, time.March, 16, 9, 0, loc),
			want:  []string{"2026-03-16 09:00", "2026-03-19 09:00", "2026-03-22 09:00"},
		},
		{
			name:  "weekly on several days",
			rules: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4"},
			start: when.Wall(2026, time.March, 16, 9, 0, loc), // a Monday
			want: []string{
				"2026-03-16 09:00", "2026-03-18 09:00",
				"2026-03-23 09:00", "2026-03-25 09:00",
			},
		},
		{
			name:  "every other week keeps to its own weeks",
			rules: []string{"RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO;COUNT=3"},
			start: when.Wall(2026, time.March, 16, 9, 0, loc),
			want:  []string{"2026-03-16 09:00", "2026-03-30 09:00", "2026-04-13 09:00"},
		},
		{
			name:  "monthly on the same day of the month",
			rules: []string{"RRULE:FREQ=MONTHLY;COUNT=3"},
			start: when.Wall(2026, time.January, 31, 9, 0, loc),
			// February and April have no 31st, and a 31st-of-the-month
			// series is not a 1st-of-March series: those months are
			// skipped rather than rolled forward.
			want: []string{"2026-01-31 09:00", "2026-03-31 09:00", "2026-05-31 09:00"},
		},
		{
			name:  "monthly on the second Tuesday",
			rules: []string{"RRULE:FREQ=MONTHLY;BYDAY=2TU;COUNT=3"},
			start: when.Wall(2026, time.March, 10, 9, 0, loc),
			want:  []string{"2026-03-10 09:00", "2026-04-14 09:00", "2026-05-12 09:00"},
		},
		{
			name:  "monthly on the last Friday",
			rules: []string{"RRULE:FREQ=MONTHLY;BYDAY=-1FR;COUNT=3"},
			start: when.Wall(2026, time.March, 27, 9, 0, loc),
			want:  []string{"2026-03-27 09:00", "2026-04-24 09:00", "2026-05-29 09:00"},
		},
		{
			name:  "monthly on the last day",
			rules: []string{"RRULE:FREQ=MONTHLY;BYMONTHDAY=-1;COUNT=3"},
			start: when.Wall(2026, time.February, 28, 9, 0, loc),
			want:  []string{"2026-02-28 09:00", "2026-03-31 09:00", "2026-04-30 09:00"},
		},
		{
			name:  "yearly",
			rules: []string{"RRULE:FREQ=YEARLY;COUNT=3"},
			start: when.Wall(2026, time.March, 14, 9, 0, loc),
			want:  []string{"2026-03-14 09:00", "2027-03-14 09:00", "2028-03-14 09:00"},
		},
		{
			name:  "yearly on a named month and day",
			rules: []string{"RRULE:FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=14;COUNT=2"},
			start: when.Wall(2026, time.March, 14, 9, 0, loc),
			want:  []string{"2026-03-14 09:00", "2027-03-14 09:00"},
		},
		{
			name:  "an excluded occurrence is not produced",
			rules: []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4", "EXDATE;TZID=Europe/Copenhagen:20260324T090000"},
			start: when.Wall(2026, time.March, 17, 9, 0, loc),
			want:  []string{"2026-03-17 09:00", "2026-03-31 09:00", "2026-04-07 09:00"},
		},
		{
			name:  "an added date appears",
			rules: []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=2", "RDATE;TZID=Europe/Copenhagen:20260320T090000"},
			start: when.Wall(2026, time.March, 17, 9, 0, loc),
			want:  []string{"2026-03-17 09:00", "2026-03-20 09:00", "2026-03-24 09:00"},
		},
		{
			name:  "until stops the series",
			rules: []string{"RRULE:FREQ=DAILY;UNTIL=20260318T080000Z"},
			start: when.Wall(2026, time.March, 16, 9, 0, loc),
			// 09:00 Copenhagen is 08:00 UTC, so the 18th is the last one
			// the UNTIL admits.
			want: []string{"2026-03-16 09:00", "2026-03-17 09:00", "2026-03-18 09:00"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := mustParse(t, c.rules...)
			occ, err := set.ExpandTimes(c.start, when.Zoned{}, when.Zoned{}, 0)
			if err != nil {
				t.Fatalf("ExpandTimes: %v", err)
			}
			wantWalls(t, occ.Times, c.want)
		})
	}
}

// TestExpandWindow: the walk is bounded by the window at both ends, so a
// long series costs the caller only the part it asked about.
func TestExpandWindow(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=30")
	start := when.Wall(2026, time.March, 1, 9, 0, loc)
	from := when.Wall(2026, time.March, 10, 0, 0, loc)
	to := when.Wall(2026, time.March, 13, 0, 0, loc)

	occ, err := set.ExpandTimes(start, from, to, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{
		"2026-03-10 09:00", "2026-03-11 09:00", "2026-03-12 09:00",
	})
}

// TestExpandOpenRuleTruncates: an open-ended rule has no last
// occurrence, so the limit is what stops it — and the result says so
// rather than reporting a total.
func TestExpandOpenRuleTruncates(t *testing.T) {
	loc := mustLoad(t, tzChicago)
	set := mustParse(t, "RRULE:FREQ=DAILY")
	start := when.Wall(2026, time.March, 1, 9, 0, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 5)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	if occ.Count() != 5 || !occ.Truncated {
		t.Fatalf("got %d occurrences, truncated=%v; want 5 and true", occ.Count(), occ.Truncated)
	}
	if occ.Reason == "" {
		t.Fatal("a truncated expansion must say what stopped it")
	}
}

// TestExpandRefusesWithoutAZone: a timed series cannot be expanded
// against an instant alone, which is §2.2 as a compile-time-adjacent
// guard.
func TestExpandRefusesWithoutAZone(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=3")
	_, err := set.ExpandTimes(when.Zoned{T: time.Now()}, when.Zoned{}, when.Zoned{}, 0)
	if err == nil {
		t.Fatal("expanded a timed series with no zone")
	}
	if !strings.Contains(err.Error(), "time zone") {
		t.Fatalf("the refusal does not name the missing zone: %v", err)
	}
}

// TestExpandDates: an all-day series stays a series of dates. Nothing
// here acquires an instant, in any zone.
func TestExpandDates(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=3")
	start := when.MustParseDate("2026-03-16")

	occ, err := set.ExpandDates(start, when.Date{}, when.Date{}, 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	if len(occ.Times) != 0 {
		t.Fatal("an all-day expansion produced instants")
	}
	want := []string{"2026-03-16", "2026-03-23", "2026-03-30"}
	if len(occ.Dates) != len(want) {
		t.Fatalf("got %v, want %v", occ.Dates, want)
	}
	for i, d := range occ.Dates {
		if d.String() != want[i] {
			t.Fatalf("date %d = %s, want %s", i, d, want[i])
		}
	}
}

// TestExpandDatesExcludesAndWindows covers the all-day path's own
// EXDATE handling and its window, which are separate code from the
// timed path because a date has no instant to compare.
func TestExpandDatesExcludesAndWindows(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=6", "EXDATE;VALUE=DATE:20260318")
	start := when.MustParseDate("2026-03-16")

	occ, err := set.ExpandDates(start, when.MustParseDate("2026-03-17"), when.MustParseDate("2026-03-21"), 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	want := []string{"2026-03-17", "2026-03-19", "2026-03-20"}
	got := make([]string, 0, len(occ.Dates))
	for _, d := range occ.Dates {
		got = append(got, d.String())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDatesTruncates(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=DAILY")
	occ, err := set.ExpandDates(when.MustParseDate("2026-03-16"), when.Date{}, when.Date{}, 4)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	if occ.Count() != 4 || !occ.Truncated {
		t.Fatalf("got %d dates, truncated=%v", occ.Count(), occ.Truncated)
	}
}

// TestExpandRefusesAFrequencyItCannotWalk: better no answer than a
// plausible wrong one. Google can expand an hourly rule; this package
// says so and points at the tool that asks Google.
func TestExpandRefusesAFrequencyItCannotWalk(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=HOURLY;COUNT=5")
	_, err := set.ExpandTimes(when.Wall(2026, time.March, 16, 9, 0, loc), when.Zoned{}, when.Zoned{}, 0)
	if err == nil {
		t.Fatal("expanded an HOURLY rule")
	}
	if !strings.Contains(err.Error(), "list_instances") {
		t.Fatalf("the refusal does not name what to use instead: %v", err)
	}
}

func TestExpandWithoutARule(t *testing.T) {
	set := mustParse(t, "RDATE;VALUE=DATE:20260322")
	if _, err := set.ExpandDates(when.MustParseDate("2026-03-16"), when.Date{}, when.Date{}, 0); err == nil {
		t.Fatal("expanded a set with no RRULE")
	}
}

// TestBoundedRuleThatProducesNothing: a BYMONTHDAY=31 daily rule filters
// nearly everything out, and the walker must stop rather than scan
// forever. The guard is the difference between a slow test and a hung
// server.
func TestBoundedRuleThatProducesNothing(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=DAILY;BYMONTHDAY=31;COUNT=3")
	occ, err := set.ExpandTimes(when.Wall(2026, time.January, 31, 9, 0, loc), when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{"2026-01-31 09:00", "2026-03-31 09:00", "2026-05-31 09:00"})
}

// TestAllDaySeriesHonorsAnInstantUntil: an UNTIL that is an instant
// belongs to a timed series, and nothing stops one arriving on an
// all-day rule. The timed path honored it while the date path ignored
// it, which is what two copies of one loop drift into.
func TestAllDaySeriesHonorsAnInstantUntil(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=DAILY;UNTIL=20260318T235959Z")
	occ, err := set.ExpandDates(when.MustParseDate("2026-03-16"), when.Date{}, when.Date{}, 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	if occ.Count() != 3 {
		got := make([]string, 0, len(occ.Dates))
		for _, d := range occ.Dates {
			got = append(got, d.String())
		}
		t.Fatalf("got %d dates %v, want the three up to the UNTIL", occ.Count(), got)
	}
}

// The six defects below were all found by review after the tests above
// were green. Each one is written as the reproduction that found it.

// TestRDATEObeysTheWindow: an RDATE is an occurrence and is subject to
// the same window, exclusions and limit as one the rule produced. The
// first version appended them after the walk, so a date 73 years
// outside the requested window was reported as being inside it.
func TestRDATEObeysTheWindow(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=3", "RDATE;VALUE=DATE:20991231")
	occ, err := set.ExpandDates(when.MustParseDate("2026-01-05"),
		when.MustParseDate("2026-01-05"), when.MustParseDate("2026-01-08"), 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	for _, d := range occ.Dates {
		if d.Year != 2026 {
			t.Fatalf("an RDATE outside the window came back: %s", d)
		}
	}
	if occ.Count() != 3 {
		t.Fatalf("got %d dates, want 3", occ.Count())
	}
}

// TestRDATEInsideTheWindowIsKept, so the fix above did not simply drop
// them.
func TestRDATEInsideTheWindowIsKept(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=2",
		"RDATE;TZID=Europe/Copenhagen:20260320T090000")
	occ, err := set.ExpandTimes(when.Wall(2026, time.March, 17, 9, 0, loc),
		when.Zoned{}, when.Zoned{}, 0)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	wantWalls(t, occ.Times, []string{"2026-03-17 09:00", "2026-03-20 09:00", "2026-03-24 09:00"})
}

// TestWeeklyByDayOrderDoesNotChangeTheAnswer. BYDAY is a set. The walker
// yielded its days in the order the caller wrote them, and every break
// above it assumes dates arrive in time order — so "WE,MO" and "MO,WE",
// the same rule, gave different answers.
func TestWeeklyByDayOrderDoesNotChangeTheAnswer(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.January, 5, 9, 0, loc) // a Monday

	want := []string{"2026-01-05 09:00", "2026-01-07 09:00", "2026-01-12 09:00"}
	for _, rule := range []string{
		"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=3",
		"RRULE:FREQ=WEEKLY;BYDAY=WE,MO;COUNT=3",
	} {
		t.Run(rule, func(t *testing.T) {
			set := mustParse(t, rule)
			occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 0)
			if err != nil {
				t.Fatalf("ExpandTimes: %v", err)
			}
			wantWalls(t, occ.Times, want)
		})
	}
}

// TestMonthlyIntersectsByDayAndByMonthDay: RFC 5545 says both present
// means "the 13th, when it is a Friday", not "whichever I read first".
func TestMonthlyIntersectsByDayAndByMonthDay(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=MONTHLY;BYDAY=FR;BYMONTHDAY=13;COUNT=3")
	occ, err := set.ExpandDates(when.MustParseDate("2026-02-13"), when.Date{}, when.Date{}, 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	for _, d := range occ.Dates {
		if d.Day != 13 || d.Weekday() != time.Friday {
			t.Fatalf("%s is not a Friday the 13th", d)
		}
	}
	if occ.Count() == 0 {
		t.Fatal("no occurrences at all")
	}
}

// TestYearlyHonorsAnOrdinalByDay is every fourth-Thursday holiday rule.
// BYDAY was never read on a yearly rule, so the series silently used the
// start's day of the month and came back wrong rather than refused.
func TestYearlyHonorsAnOrdinalByDay(t *testing.T) {
	set := mustParse(t, "RRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=4TH;COUNT=3")
	occ, err := set.ExpandDates(when.MustParseDate("2026-11-26"), when.Date{}, when.Date{}, 0)
	if err != nil {
		t.Fatalf("ExpandDates: %v", err)
	}
	want := []string{"2026-11-26", "2027-11-25", "2028-11-23"}
	got := make([]string, 0, len(occ.Dates))
	for _, d := range occ.Dates {
		got = append(got, d.String())
		if d.Weekday() != time.Thursday {
			t.Fatalf("%s is not a Thursday", d)
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestASeriesThatEndsAtTheLimitIsNotTruncated.
//
// Truncated was set whenever the count reached the limit, including when
// the rule ended there. Reach keys on it, so a complete ten-occurrence
// series was described to the caller as "no end date — at least 10
// occurrences", in the refusal text §4.2 uses to make a scope decision.
func TestASeriesThatEndsAtTheLimitIsNotTruncated(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=DAILY;COUNT=10")
	start := when.Wall(2026, time.March, 16, 9, 0, loc)

	occ, err := set.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 10)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	if occ.Count() != 10 {
		t.Fatalf("got %d occurrences, want 10", occ.Count())
	}
	if occ.Truncated {
		t.Fatalf("a series of exactly 10 occurrences read at a limit of 10 is complete, not truncated")
	}
	if got := set.Reach(start, 10); !strings.Contains(got, "10 occurrences, the last on") {
		t.Fatalf("Reach() = %q; a bounded series must not be described as endless", got)
	}
	// And one occurrence more than the limit still truncates.
	eleven := mustParse(t, "RRULE:FREQ=DAILY;COUNT=11")
	occ, err = eleven.ExpandTimes(start, when.Zoned{}, when.Zoned{}, 10)
	if err != nil {
		t.Fatalf("ExpandTimes: %v", err)
	}
	if !occ.Truncated || occ.Count() != 10 {
		t.Fatalf("got %d occurrences, truncated=%v; want 10 and true", occ.Count(), occ.Truncated)
	}
}
