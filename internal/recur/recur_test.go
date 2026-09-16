package recur_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/recur"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

func TestParseRule(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		check func(*testing.T, recur.Rule)
	}{
		{
			name: "weekly with a count",
			line: "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10",
			check: func(t *testing.T, r recur.Rule) {
				if r.Freq != recur.Weekly || r.Count != 10 || r.Interval != 1 {
					t.Fatalf("got %+v", r)
				}
				if len(r.ByDay) != 1 || r.ByDay[0].Day != time.Tuesday || r.ByDay[0].Ordinal != 0 {
					t.Fatalf("BYDAY = %+v", r.ByDay)
				}
			},
		},
		{
			name: "interval and several days",
			line: "RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE,FR",
			check: func(t *testing.T, r recur.Rule) {
				if r.Interval != 2 || len(r.ByDay) != 3 {
					t.Fatalf("got %+v", r)
				}
				if !r.Open() {
					t.Fatal("a rule with neither COUNT nor UNTIL should be open")
				}
			},
		},
		{
			name: "an ordinal weekday",
			line: "RRULE:FREQ=MONTHLY;BYDAY=2TU",
			check: func(t *testing.T, r recur.Rule) {
				if len(r.ByDay) != 1 || r.ByDay[0].Ordinal != 2 {
					t.Fatalf("BYDAY = %+v", r.ByDay)
				}
			},
		},
		{
			name: "a negative ordinal",
			line: "RRULE:FREQ=MONTHLY;BYDAY=-1FR",
			check: func(t *testing.T, r recur.Rule) {
				if r.ByDay[0].Ordinal != -1 || r.ByDay[0].Day != time.Friday {
					t.Fatalf("BYDAY = %+v", r.ByDay)
				}
			},
		},
		{
			name: "a UTC until",
			line: "RRULE:FREQ=WEEKLY;UNTIL=20260501T215959Z",
			check: func(t *testing.T, r recur.Rule) {
				if r.Until.IsZero() || r.Until.UTC().Format("2006-01-02T15:04:05") != "2026-05-01T21:59:59" {
					t.Fatalf("UNTIL = %v", r.Until)
				}
				if r.Open() {
					t.Fatal("a rule with an UNTIL is not open")
				}
			},
		},
		{
			name: "a date until, which is what an all-day series carries",
			line: "RRULE:FREQ=DAILY;UNTIL=20260501",
			check: func(t *testing.T, r recur.Rule) {
				if r.UntilDate.String() != "2026-05-01" {
					t.Fatalf("UNTIL date = %v", r.UntilDate)
				}
			},
		},
		{
			name: "no prefix",
			line: "FREQ=DAILY;COUNT=3",
			check: func(t *testing.T, r recur.Rule) {
				if r.Freq != recur.Daily || r.Count != 3 {
					t.Fatalf("got %+v", r)
				}
			},
		},
		{
			name: "a part this package does not model is kept, not dropped",
			line: "RRULE:FREQ=WEEKLY;BYSETPOS=2;BYDAY=TU",
			check: func(t *testing.T, r recur.Rule) {
				if len(r.Unknown) != 1 || !strings.Contains(r.Unknown[0], "BYSETPOS") {
					t.Fatalf("Unknown = %v", r.Unknown)
				}
				if !strings.Contains(r.Explain(), "BYSETPOS") {
					t.Fatalf("Explain() hides the part it did not read: %q", r.Explain())
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := recur.ParseRule(c.line)
			if err != nil {
				t.Fatalf("ParseRule(%q): %v", c.line, err)
			}
			if r.Raw != c.line {
				t.Fatalf("Raw = %q, want the line as given: a write sends the caller's own rule", r.Raw)
			}
			c.check(t, r)
		})
	}
}

func TestParseRuleRefuses(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"no FREQ", "RRULE:COUNT=3", "FREQ"},
		{"COUNT and UNTIL together", "RRULE:FREQ=DAILY;COUNT=3;UNTIL=20260501T000000Z", "forbids"},
		{"a zero interval", "RRULE:FREQ=DAILY;INTERVAL=0", "positive"},
		{"a local UNTIL on a zoned rule", "RRULE:FREQ=DAILY;UNTIL=20260501T090000", "ending in Z"},
		{"not a weekday", "RRULE:FREQ=WEEKLY;BYDAY=XX", "weekday"},
		{"a repeated part", "RRULE:FREQ=DAILY;COUNT=2;COUNT=3", "twice"},
		{"not key=value", "RRULE:FREQ=DAILY;COUNT", "KEY=VALUE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := recur.ParseRule(c.line)
			if err == nil {
				t.Fatalf("ParseRule(%q) was accepted", c.line)
			}
			if !errors.Is(err, recur.ErrInvalid) {
				t.Fatalf("error does not wrap ErrInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestParseSetReadsTheWholeField: Google puts EXDATE and RDATE in the
// same array as the RRULE, and an expansion that ignores them reports
// occurrences the series does not have.
func TestParseSetReadsTheWholeField(t *testing.T) {
	set, err := recur.Parse([]string{
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=5",
		"EXDATE;TZID=Europe/Copenhagen:20260324T140000",
		"RDATE;TZID=Europe/Copenhagen:20260401T140000",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if set.Rule == nil {
		t.Fatal("no rule parsed")
	}
	if len(set.Excluded) != 1 || len(set.Added) != 1 {
		t.Fatalf("excluded %d, added %d", len(set.Excluded), len(set.Added))
	}
	if !set.Recurring() {
		t.Fatal("Recurring() should be true")
	}
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := set.Excluded[0].Instant(loc)
	if !ok {
		t.Fatal("the EXDATE produced no instant")
	}
	if got.Format(time.RFC3339) != "2026-03-24T14:00:00+01:00" {
		t.Fatalf("EXDATE resolved to %s", got.Format(time.RFC3339))
	}
}

// TestExdateValueDate: an all-day series excludes a DATE, which has no
// instant at all (§4.1).
func TestExdateValueDate(t *testing.T) {
	set, err := recur.Parse([]string{
		"RRULE:FREQ=DAILY;COUNT=5",
		"EXDATE;VALUE=DATE:20260322,20260323",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(set.Excluded) != 2 {
		t.Fatalf("got %d excluded dates, want 2", len(set.Excluded))
	}
	for _, p := range set.Excluded {
		if !p.AllDay {
			t.Fatalf("a VALUE=DATE point is not all-day: %+v", p)
		}
		if _, ok := p.Instant(time.UTC); ok {
			t.Fatal("an all-day point produced an instant; a date is not a time")
		}
	}
}

// TestUnsupportedLinesAreCarriedNotGuessed. EXRULE changes which
// occurrences exist, so a count computed while ignoring it is wrong in
// the direction that matters: too many.
func TestUnsupportedLinesAreCarriedNotGuessed(t *testing.T) {
	set, err := recur.Parse([]string{
		"RRULE:FREQ=DAILY;COUNT=10",
		"EXRULE:FREQ=WEEKLY;BYDAY=SA,SU",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(set.Unsupported) != 1 {
		t.Fatalf("Unsupported = %v", set.Unsupported)
	}
	loc := time.UTC
	_, err = set.ExpandTimes(when.Wall(2026, time.March, 16, 9, 0, loc), when.Zoned{}, when.Zoned{}, 0)
	if err == nil {
		t.Fatal("expanded a series carrying an EXRULE instead of refusing")
	}
	if !strings.Contains(err.Error(), "EXRULE") {
		t.Fatalf("the refusal does not name what it could not expand: %v", err)
	}
}

func TestExplain(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
	}{
		{[]string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10"}, "every week on Tuesday, 10 times"},
		{[]string{"RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=TU"}, "every 2 weeks on Tuesday"},
		{[]string{"RRULE:FREQ=DAILY"}, "every day"},
		{[]string{"RRULE:FREQ=MONTHLY;BYDAY=2TU"}, "every month on 2nd Tuesday"},
		{[]string{"RRULE:FREQ=MONTHLY;BYDAY=-1FR"}, "every month on last Friday"},
		{[]string{"RRULE:FREQ=MONTHLY;BYMONTHDAY=-1"}, "every month on the last day"},
		{[]string{"RRULE:FREQ=MONTHLY;BYMONTHDAY=11"}, "every month on the 11th"},
		{[]string{"RRULE:FREQ=YEARLY;COUNT=1"}, "every year, once"},
		{[]string{"RRULE:FREQ=DAILY;UNTIL=20260501"}, "every day, until 2026-05-01"},
		{[]string{"RRULE:FREQ=DAILY;UNTIL=20260501T215959Z"}, "every day, until 2026-05-01 (UTC)"},
		{[]string{"RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR"}, "every week on Monday, Wednesday and Friday"},
		{
			[]string{"RRULE:FREQ=WEEKLY;BYDAY=TU", "EXDATE;TZID=Europe/Copenhagen:20260324T140000"},
			"every week on Tuesday, except on 1 date",
		},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			set, err := recur.Parse(c.lines)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := set.Explain(); got != c.want {
				t.Fatalf("Explain() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestExplainFallsBackToTheRule: a rule this package cannot describe
// comes back as itself rather than as a wrong description.
func TestExplainFallsBackToTheRule(t *testing.T) {
	set, err := recur.Parse([]string{"RRULE:FREQ=HOURLY;INTERVAL=6"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := set.Explain(); got != "RRULE:FREQ=HOURLY;INTERVAL=6" {
		t.Fatalf("Explain() = %q, want the rule itself", got)
	}
}

func TestExplainNoRule(t *testing.T) {
	var empty recur.Set
	if got := empty.Explain(); got != "repeats" {
		t.Fatalf("Explain() = %q", got)
	}
	set, err := recur.Parse([]string{"RDATE;VALUE=DATE:20260322"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := set.Explain(); got != "RDATE;VALUE=DATE:20260322" {
		t.Fatalf("Explain() = %q, want the line itself", got)
	}
	if !set.Recurring() {
		t.Fatal("a set with an RDATE and no RRULE still repeats")
	}
}
