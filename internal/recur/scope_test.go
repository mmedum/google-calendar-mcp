package recur_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/recur"
	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// TestParseScopeRefusesWithTheThreeChoices is §4.2 as a test: a missing
// scope is refused, and the refusal names the three options and what
// each would do. A caller who gets "scope is required" and nothing else
// guesses, which is the thing this rule exists to prevent.
func TestParseScopeRefusesWithTheThreeChoices(t *testing.T) {
	for _, in := range []string{"", "   ", "all", "everything", "future"} {
		t.Run("refuses "+in, func(t *testing.T) {
			_, err := recur.ParseScope(in)
			if err == nil {
				t.Fatalf("ParseScope(%q) was accepted", in)
			}
			for _, want := range []string{"instance", "series", "this_and_following"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal does not offer %q: %v", want, err)
				}
			}
			if !strings.Contains(err.Error(), "RESETS") {
				t.Fatalf("the refusal does not warn what this_and_following does: %v", err)
			}
		})
	}
}

func TestParseScope(t *testing.T) {
	cases := map[string]recur.Scope{
		"instance":           recur.ScopeInstance,
		"series":             recur.ScopeSeries,
		"this_and_following": recur.ScopeThisAndFollowing,
		"  SERIES  ":         recur.ScopeSeries,
	}
	for in, want := range cases {
		got, err := recur.ParseScope(in)
		if err != nil {
			t.Fatalf("ParseScope(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseScope(%q) = %q, want %q", in, got, want)
		}
		if got.Means() == "" {
			t.Fatalf("%q has no explanation", got)
		}
	}
	if recur.Scope("nonsense").Valid() {
		t.Fatal("an invented scope is valid")
	}
	if recur.Scope("nonsense").Means() != "" {
		t.Fatal("an invented scope explains itself")
	}
}

// TestReach is the other half of §4.2's refusal: before a caller picks
// "series", they are told how far it reaches.
func TestReach(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.March, 17, 9, 0, loc)

	cases := []struct {
		name  string
		rules []string
		want  string
	}{
		{"counted", []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"}, "4 occurrences, the last on 2026-04-07"},
		{"open", []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"}, "no end date"},
		{"one", []string{"RRULE:FREQ=WEEKLY;COUNT=1"}, "one occurrence"},
		{"unwalkable", []string{"RRULE:FREQ=HOURLY;COUNT=4"}, "list_instances"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := mustParse(t, c.rules...)
			got := set.Reach(start, 500)
			if !strings.Contains(got, c.want) {
				t.Fatalf("Reach() = %q, want it to mention %q", got, c.want)
			}
		})
	}

	var empty recur.Set
	if got := empty.Reach(start, 10); !strings.Contains(got, "does not repeat") {
		t.Fatalf("Reach() on a non-recurring event = %q", got)
	}
}

// TestSplitCountedSeries is §2.8's arithmetic: "this and following" ends
// the original series before the target and starts a new one at it. The
// two counts have to add up to the original, or occurrences appear or
// vanish.
func TestSplitCountedSeries(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10")
	start := when.Wall(2026, time.March, 17, 9, 0, loc)
	target := when.Wall(2026, time.April, 7, 9, 0, loc) // the fourth occurrence

	before, after, err := set.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if before != "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=3" {
		t.Fatalf("truncated rule = %q", before)
	}
	if after != "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=7" {
		t.Fatalf("new series rule = %q", after)
	}
}

// TestSplitOpenSeries: an open-ended series keeps its rule on the new
// half. There is nothing to count down.
func TestSplitOpenSeries(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU")
	start := when.Wall(2026, time.March, 17, 9, 0, loc)
	target := when.Wall(2026, time.March, 31, 9, 0, loc)

	before, after, err := set.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if before != "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=2" {
		t.Fatalf("truncated rule = %q", before)
	}
	if after != "RRULE:FREQ=WEEKLY;BYDAY=TU" {
		t.Fatalf("new series rule = %q, want the original", after)
	}
}

// TestSplitDropsUntilWhenItSetsCount: COUNT and UNTIL cannot both be
// present, and a rule carrying both is refused by Google.
func TestSplitDropsUntilWhenItSetsCount(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20260501T070000Z")
	start := when.Wall(2026, time.March, 17, 9, 0, loc)
	target := when.Wall(2026, time.March, 31, 9, 0, loc)

	before, after, err := set.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if strings.Contains(before, "UNTIL") {
		t.Fatalf("the truncated rule carries both COUNT and UNTIL: %q", before)
	}
	if !strings.Contains(before, "COUNT=2") {
		t.Fatalf("truncated rule = %q", before)
	}
	// The new half keeps the original end: the series still stops when
	// it always did.
	if !strings.Contains(after, "UNTIL=20260501T070000Z") {
		t.Fatalf("new series rule = %q, want it to keep the UNTIL", after)
	}
}

func TestSplitRefuses(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.March, 17, 9, 0, loc)

	cases := []struct {
		name   string
		rules  []string
		target when.Zoned
		want   string
	}{
		{
			name:   "the target is the first occurrence",
			rules:  []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"},
			target: start,
			want:   "series write",
		},
		{
			name:   "the target is before the series",
			rules:  []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"},
			target: when.Wall(2026, time.March, 10, 9, 0, loc),
			want:   "not after",
		},
		{
			name:   "the target is past the end of the series",
			rules:  []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=2"},
			target: when.Wall(2026, time.May, 12, 9, 0, loc),
			want:   "every occurrence",
		},
		{
			name:   "a rule this server cannot walk",
			rules:  []string{"RRULE:FREQ=HOURLY;COUNT=4"},
			target: when.Wall(2026, time.March, 31, 9, 0, loc),
			want:   "not one this server expands",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := mustParse(t, c.rules...)
			_, _, err := set.Split(start, c.target)
			if err == nil {
				t.Fatal("Split was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}

	set := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4")
	if _, _, err := set.Split(when.Zoned{}, when.Zoned{}); err == nil {
		t.Fatal("Split with no start or target was accepted")
	}
}

// TestUntilLine: the UNTIL a zoned series carries is UTC with a Z. A
// local one is invalid, and reading one back as UTC moves the end of the
// series by the offset.
func TestUntilLine(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	r, err := recur.ParseRule("RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10")
	if err != nil {
		t.Fatal(err)
	}
	end := when.Wall(2026, time.April, 7, 8, 59, loc)
	got := r.UntilLine(end.T)
	if got != "RRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20260407T065900Z" {
		t.Fatalf("UntilLine() = %q", got)
	}
	if strings.Contains(got, "COUNT") {
		t.Fatal("UntilLine kept the COUNT, which RFC 5545 forbids alongside UNTIL")
	}
	// It round-trips: what this writes, this package can read back.
	back, err := recur.ParseRule(got)
	if err != nil {
		t.Fatalf("the rule this package wrote cannot be parsed back: %v", err)
	}
	if !back.Until.Equal(end.T.UTC().Truncate(time.Second)) {
		t.Fatalf("UNTIL round-tripped to %v, want %v", back.Until, end.T.UTC())
	}
}

func TestUntilZoned(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	r, err := recur.ParseRule("RRULE:FREQ=DAILY;UNTIL=20260407T065900Z")
	if err != nil {
		t.Fatal(err)
	}
	z, ok := r.UntilZoned(loc)
	if !ok {
		t.Fatal("a rule with an UNTIL reported none")
	}
	if got := z.T.Format("2006-01-02 15:04"); got != "2026-04-07 08:59" {
		t.Fatalf("UntilZoned in %s = %s", tzCopenhagen, got)
	}
	open, err := recur.ParseRule("RRULE:FREQ=DAILY")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := open.UntilZoned(loc); ok {
		t.Fatal("an open rule reported an UNTIL")
	}
}

// TestSplitCountsWhatTheRuleGenerates.
//
// A COUNT counts what the RULE produces; Google applies EXDATE and RDATE
// on top of it. Splitting on the visible occurrence count wrote a COUNT
// that was too small when a date had been excluded — losing an
// occurrence from the original series — and too large when an RDATE had
// been added, running the truncated original past the split target and
// overlapping the new series.
func TestSplitCountsWhatTheRuleGenerates(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	start := when.Wall(2026, time.March, 17, 9, 0, loc)
	target := when.Wall(2026, time.April, 21, 9, 0, loc) // the sixth occurrence

	plain := mustParse(t, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10")
	wantBefore, wantAfter, err := plain.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if !strings.Contains(wantBefore, "COUNT=5") {
		t.Fatalf("the plain series truncated to %q, want COUNT=5", wantBefore)
	}

	// One occurrence excluded before the target. The rule still
	// generates five before it, so the split is unchanged.
	excluded := mustParse(t,
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10",
		"EXDATE;TZID=Europe/Copenhagen:20260324T090000")
	before, after, err := excluded.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if before != wantBefore || after != wantAfter {
		t.Fatalf("an EXDATE changed the split: got %q / %q, want %q / %q",
			before, after, wantBefore, wantAfter)
	}

	// An added date must not push the truncated series past the target
	// either.
	added := mustParse(t,
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10",
		"RDATE;TZID=Europe/Copenhagen:20260320T090000")
	before, after, err = added.Split(start, target)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if before != wantBefore || after != wantAfter {
		t.Fatalf("an RDATE changed the split: got %q / %q, want %q / %q",
			before, after, wantBefore, wantAfter)
	}
}
