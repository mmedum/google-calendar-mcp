package when_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/when"
)

func mustHours(t *testing.T, from, to string, days []string) when.Hours {
	t.Helper()
	h, err := when.ParseHours(from, to, days)
	if err != nil {
		t.Fatalf("ParseHours(%q, %q, %v): %v", from, to, days, err)
	}
	return h
}

// windowOver is the window from the start of one date to the start of
// another, in loc.
func windowOver(t *testing.T, from, to string, loc *time.Location) when.Window {
	t.Helper()
	w, err := when.NewWindow(
		when.NewZoned(when.MustParseDate(from).StartIn(loc), loc),
		when.NewZoned(when.MustParseDate(to).StartIn(loc), loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func spans(w []when.Window) []string {
	out := make([]string, 0, len(w))
	for _, v := range w {
		out = append(out, v.Start.T.Format("2006-01-02 15:04")+"-"+v.End.T.Format("15:04"))
	}
	return out
}

func equalSpans(t *testing.T, got []when.Window, want []string) {
	t.Helper()
	g := spans(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("interval %d = %s, want %s (all: %v)", i, g[i], want[i], g)
		}
	}
}

// TestHoursMaskOneDayPerKeptWeekday is the shape §17.2 argues for: five
// days of a week come back as five intervals, not one.
func TestHoursMaskOneDayPerKeptWeekday(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	// 2026-03-16 is a Monday.
	w := windowOver(t, "2026-03-16", "2026-03-23", loc)
	h := mustHours(t, "09:00", "17:00", []string{"mon", "tue", "wed", "thu", "fri"})

	equalSpans(t, h.Windows(w), []string{
		"2026-03-16 09:00-17:00",
		"2026-03-17 09:00-17:00",
		"2026-03-18 09:00-17:00",
		"2026-03-19 09:00-17:00",
		"2026-03-20 09:00-17:00",
	})
}

// TestHoursHoldTheWallClockAcrossATransition is the mask's version of
// §4.1: 09:00 is 09:00 on both sides of a daylight-saving change, so
// every day of the week is eight hours long even though one of them is
// 23 hours long.
func TestHoursHoldTheWallClockAcrossATransition(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	// 2026-03-29 is the spring-forward Sunday.
	w := windowOver(t, "2026-03-27", "2026-03-31", loc)
	h := mustHours(t, "09:00", "17:00", nil)

	got := h.Windows(w)
	if len(got) != 4 {
		t.Fatalf("got %d masked days, want 4: %v", len(got), spans(got))
	}
	for _, day := range got {
		if day.Duration() != 8*time.Hour {
			t.Fatalf("%s is %v long, want 8h", spans([]when.Window{day})[0], day.Duration())
		}
		if got := day.Start.T.Format("15:04"); got != "09:00" {
			t.Fatalf("a masked day starts at %s, want 09:00", got)
		}
	}
}

// TestHoursSpanningTheTransitionItselfIsShort: a mask that contains the
// missing hour is an hour shorter that day, and an hour longer on the
// autumn side. This is the assertion an offset-based mask fails.
func TestHoursSpanningTheTransitionItselfIsShort(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	h := mustHours(t, "01:00", "05:00", nil)

	spring := h.Windows(windowOver(t, "2026-03-29", "2026-03-30", loc))
	if len(spring) != 1 || spring[0].Duration() != 3*time.Hour {
		t.Fatalf("spring forward: got %v, want one 3h interval", spans(spring))
	}
	autumn := h.Windows(windowOver(t, "2026-10-25", "2026-10-26", loc))
	if len(autumn) != 1 || autumn[0].Duration() != 5*time.Hour {
		t.Fatalf("autumn back: got %v, want one 5h interval", spans(autumn))
	}
}

// TestHoursInTheSouthernHemisphere runs the same assertion where the
// transitions go the other way round.
func TestHoursInTheSouthernHemisphere(t *testing.T) {
	loc := mustLoad(t, tzAuckland)
	h := mustHours(t, "01:00", "05:00", nil)

	// Auckland springs forward in September and falls back in April.
	spring := h.Windows(windowOver(t, "2026-09-27", "2026-09-28", loc))
	if len(spring) != 1 || spring[0].Duration() != 3*time.Hour {
		t.Fatalf("Auckland spring: got %v, want one 3h interval", spans(spring))
	}
	autumn := h.Windows(windowOver(t, "2026-04-05", "2026-04-06", loc))
	if len(autumn) != 1 || autumn[0].Duration() != 5*time.Hour {
		t.Fatalf("Auckland autumn: got %v, want one 5h interval", spans(autumn))
	}
}

// TestHoursInANonHourOffset: +05:45 has no bearing on a wall-clock mask,
// which is the point — the mask is a reading, not an offset.
func TestHoursInANonHourOffset(t *testing.T) {
	loc := mustLoad(t, tzKathmandu)
	h := mustHours(t, "09:00", "17:00", nil)
	got := h.Windows(windowOver(t, "2026-03-16", "2026-03-17", loc))
	if len(got) != 1 || got[0].Duration() != 8*time.Hour {
		t.Fatalf("got %v, want one 8h interval", spans(got))
	}
	if off := got[0].Start.String(); !strings.HasSuffix(off, "+05:45") {
		t.Fatalf("the masked day carries %s, want a +05:45 offset", off)
	}
}

// TestHoursWithDaysOnlyIsWholeDays: "not at the weekend" is a mask with
// no clock times in it.
func TestHoursWithDaysOnlyIsWholeDays(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	h := mustHours(t, "", "", []string{"sat", "sun"})
	got := h.Windows(windowOver(t, "2026-03-16", "2026-03-23", loc))

	equalSpans(t, got, []string{
		"2026-03-21 00:00-00:00",
		"2026-03-22 00:00-00:00",
	})
	for _, d := range got {
		if d.Duration() != 24*time.Hour {
			t.Fatalf("a whole day mask is %v long, want 24h", d.Duration())
		}
	}
}

// TestUnmaskedHoursProduceNothing: a zero Hours is "no mask", and a
// caller must be able to tell that from a mask that keeps nothing.
func TestUnmaskedHoursProduceNothing(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	var h when.Hours
	if h.Set() {
		t.Fatal("a zero Hours reports itself as set")
	}
	if got := h.Windows(windowOver(t, "2026-03-16", "2026-03-23", loc)); got != nil {
		t.Fatalf("a zero Hours produced %v", spans(got))
	}
	if got := h.String(); got != "" {
		t.Fatalf("a zero Hours renders as %q", got)
	}
}

// TestHoursRefusals are the four a caller can hit, each naming what to
// do instead.
func TestHoursRefusals(t *testing.T) {
	cases := []struct {
		name          string
		from, to      string
		days          []string
		wantSubstring string
	}{
		{"a span that crosses midnight", "22:00", "06:00", nil, "crosses midnight"},
		{"a span with no length", "09:00", "09:00", nil, "not after"},
		{"one end without the other", "09:00", "", nil, "both a start and an end"},
		{"a day that is not a day", "09:00", "17:00", []string{"mondayish"}, "not a weekday"},
		{"a reading that is not a clock", "9am", "17:00", nil, "not hh:mm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := when.ParseHours(c.from, c.to, c.days)
			if err == nil {
				t.Fatal("accepted")
			}
			if !errors.Is(err, when.ErrInvalid) {
				t.Fatalf("error %v is not when.ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), c.wantSubstring) {
				t.Fatalf("error %q does not mention %q", err, c.wantSubstring)
			}
		})
	}
}

// TestIntersectIsHalfOpen: a mask ending at 17:00 and a gap starting at
// 17:00 share nothing, and no zero-length interval is produced.
func TestIntersectIsHalfOpen(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	morning := windowOver(t, "2026-03-16", "2026-03-17", loc)
	h := mustHours(t, "09:00", "17:00", nil)
	mask := h.Windows(morning)

	after, err := when.NewWindow(
		when.Wall(2026, time.March, 16, 17, 0, loc),
		when.Wall(2026, time.March, 16, 18, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := when.Intersect([]when.Window{after}, mask); got != nil {
		t.Fatalf("touching intervals intersected to %v", spans(got))
	}

	over := when.Window{
		Start: when.Wall(2026, time.March, 16, 16, 0, loc),
		End:   when.Wall(2026, time.March, 16, 18, 0, loc), Loc: loc,
	}
	equalSpans(t, when.Intersect([]when.Window{over}, mask), []string{"2026-03-16 16:00-17:00"})
}

// TestIntersectKeepsEveryOverlapOfOneInterval: a gap spanning a week
// meets the mask once per day, and an implementation that advanced both
// sides together would report only the first.
func TestIntersectKeepsEveryOverlapOfOneInterval(t *testing.T) {
	loc := mustLoad(t, tzCopenhagen)
	week := windowOver(t, "2026-03-16", "2026-03-21", loc)
	h := mustHours(t, "09:00", "17:00", nil)

	got := when.Intersect([]when.Window{week}, h.Windows(week))
	if len(got) != 5 {
		t.Fatalf("a five-day gap met the mask %d times, want 5: %v", len(got), spans(got))
	}
}

// TestHoursString says what a result has to print, including the case
// where the caller named no days.
func TestHoursString(t *testing.T) {
	for _, c := range []struct {
		from, to string
		days     []string
		want     string
	}{
		{"09:00", "17:00", nil, "09:00-17:00, every day"},
		{"09:00", "17:00", []string{"mon", "fri"}, "09:00-17:00, Mon Fri"},
		{"", "", []string{"sun"}, "all day, Sun"},
	} {
		if got := mustHours(t, c.from, c.to, c.days).String(); got != c.want {
			t.Fatalf("String() = %q, want %q", got, c.want)
		}
	}
}
