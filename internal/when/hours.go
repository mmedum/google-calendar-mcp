package when

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TimeOfDay is a wall-clock reading with no day and no zone: "09:00".
//
// It is not a Zoned and cannot become one on its own, for the same
// reason a Date cannot (§4.1). 09:00 is a reading a zone turns into an
// instant on a given day, and which instant depends on the day — a
// daylight-saving Sunday has one fewer or one more hour in it than the
// Sunday before.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseTimeOfDay reads "hh:mm" on a 24-hour clock.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("15:04", s)
	if err != nil {
		return TimeOfDay{}, fmt.Errorf("%w: time of day %q is not hh:mm on a 24-hour clock", ErrInvalid, s)
	}
	return TimeOfDay{Hour: t.Hour(), Minute: t.Minute()}, nil
}

// String renders "09:00".
func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

// minutes is the reading as minutes since midnight, for ordering only.
func (t TimeOfDay) minutes() int { return t.Hour*60 + t.Minute }

// Hours is a daily mask: a wall-clock span on chosen weekdays.
//
// It exists because a window and a working week are different shapes
// (§17.2). A window is one interval, so "next week, 09:00 to 17:00" is
// not expressible as one — a caller either accepts a fifteen-hour
// overnight gap in the answer or makes five calls. A mask is the daily
// shape the window cannot hold.
//
// The mask is applied in the zone the answer is rendered in, day by
// local day, so it means 09:00 where the person is rather than a fixed
// number of hours after midnight UTC.
type Hours struct {
	From TimeOfDay
	To   TimeOfDay
	// Days are the weekdays the mask keeps. Empty means every day: the
	// server does not guess anybody's working week. A five-day Monday
	// default would be a guess, and it is wrong in every country whose
	// week runs Sunday to Thursday.
	Days []time.Weekday
	set  bool
}

// ParseHours builds a mask from the strings a caller passes.
//
// All three parts are optional and none has a default. from and to come
// together: one alone describes no span. days alone is a mask of whole
// days, which is how "not at the weekend" is asked for.
func ParseHours(from, to string, days []string) (Hours, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	wd, err := parseWeekdays(days)
	if err != nil {
		return Hours{}, err
	}
	switch {
	case from == "" && to == "":
		if len(wd) == 0 {
			return Hours{}, nil
		}
		// Whole days, on the named weekdays.
		return Hours{From: TimeOfDay{}, To: TimeOfDay{Hour: 24}, Days: wd, set: true}, nil
	case from == "" || to == "":
		return Hours{}, fmt.Errorf("%w: working hours need both a start and an end; got only %q",
			ErrInvalid, from+to)
	}

	f, err := ParseTimeOfDay(from)
	if err != nil {
		return Hours{}, err
	}
	t, err := ParseTimeOfDay(to)
	if err != nil {
		return Hours{}, err
	}
	if t.minutes() <= f.minutes() {
		// An overnight mask is refused rather than guessed at. 22:00 to
		// 06:00 spans two calendar days, so "which days" stops having
		// one answer: a Friday night shift ends on Saturday, and a mask
		// that quietly counted it as Friday would be inventing a rule
		// nobody stated.
		return Hours{}, fmt.Errorf("%w: working hours end at %s, which is not after their start %s. "+
			"A mask that crosses midnight is not supported, because the day a night shift belongs to "+
			"is a choice this server would be making for you — ask about each night as its own window",
			ErrInvalid, t, f)
	}
	return Hours{From: f, To: t, Days: wd, set: true}, nil
}

// Set reports whether a mask was asked for at all.
func (h Hours) Set() bool { return h.set }

// String says what the mask is, for a result that has to state what it
// applied (§4.5).
func (h Hours) String() string {
	if !h.set {
		return ""
	}
	span := h.From.String() + "-" + h.To.String()
	if h.From == (TimeOfDay{}) && h.To == (TimeOfDay{Hour: 24}) {
		span = "all day"
	}
	if len(h.Days) == 0 {
		return span + ", every day"
	}
	names := make([]string, 0, len(h.Days))
	for _, d := range h.Days {
		names = append(names, d.String()[:3])
	}
	return span + ", " + strings.Join(names, " ")
}

// keeps reports whether the mask covers this weekday.
func (h Hours) keeps(d time.Weekday) bool {
	if len(h.Days) == 0 {
		return true
	}
	for _, w := range h.Days {
		if w == d {
			return true
		}
	}
	return false
}

// Windows is the mask as concrete intervals inside w, one per local day
// it keeps.
//
// Each day is built from that day's own midnight, so a day carrying a
// daylight-saving transition is 23 or 25 hours long and 09:00 is still
// 09:00 on it. A mask built by adding a fixed number of hours to the
// window's start would drift by an hour halfway through the week, which
// is the defect §4.1 exists to prevent, one level up from an event.
func (h Hours) Windows(w Window) []Window {
	if !h.set {
		return nil
	}
	loc := w.Loc
	if loc == nil {
		loc = time.UTC
	}

	var out []Window
	for d := w.Start.In(loc).Date(); !d.After(w.End.In(loc).Date()); d = d.AddDays(1) {
		if !h.keeps(d.Weekday()) {
			continue
		}
		day := DayWindow(d, loc)
		start, end := day.Start, day.End
		if h.From != (TimeOfDay{}) {
			// The reading may be skipped or repeated on a transition
			// day. Either instant is the right edge for a filter: the
			// mask is deciding which minutes to keep, not naming a
			// moment somebody has to arrive at, so the ambiguity a
			// WallStrict caller would report has nothing to report to.
			z, _ := WallStrict(d.Year, d.Month, d.Day, h.From.Hour, h.From.Minute, loc)
			start = z
		}
		if h.To != (TimeOfDay{Hour: 24}) {
			z, _ := WallStrict(d.Year, d.Month, d.Day, h.To.Hour, h.To.Minute, loc)
			end = z
		}
		// Clipped to the window, so the mask never reaches outside the
		// interval it is masking. The last local day is usually a
		// partial one — a window ending at midnight touches the date
		// after it without containing any of it.
		if start.T.Before(w.Start.T) {
			start = w.Start.In(loc)
		}
		if end.T.After(w.End.T) {
			end = w.End.In(loc)
		}
		if !end.T.After(start.T) {
			continue
		}
		out = append(out, Window{Start: start, End: end, Loc: loc})
	}
	return out
}

// Intersect returns the intervals present in both sets.
//
// Both sides are half-open, so a mask ending at 17:00 and a gap starting
// at 17:00 share nothing, and no zero-length interval is produced.
func Intersect(a, b []Window) []Window {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	left, right := sortedCopy(a), sortedCopy(b)

	var out []Window
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		start, end := left[i].Start, left[i].End
		if right[j].Start.T.After(start.T) {
			start = right[j].Start
		}
		if right[j].End.T.Before(end.T) {
			end = right[j].End
		}
		if end.T.After(start.T) {
			loc := left[i].Loc
			if loc == nil {
				loc = right[j].Loc
			}
			out = append(out, Window{Start: start.In(loc), End: end.In(loc), Loc: loc})
		}
		// Advance whichever interval ends first: the other may still
		// overlap what comes next.
		if left[i].End.T.Before(right[j].End.T) {
			i++
			continue
		}
		j++
	}
	return out
}

func sortedCopy(w []Window) []Window {
	out := make([]Window, len(w))
	copy(out, w)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.T.Before(out[j].Start.T) })
	return out
}

// parseWeekdays reads the day names a caller passes.
func parseWeekdays(days []string) ([]time.Weekday, error) {
	var out []time.Weekday
	seen := map[time.Weekday]bool{}
	for _, raw := range days {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		d, ok := weekdayNames[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a weekday. Use mon tue wed thu fri sat sun", ErrInvalid, raw)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

var weekdayNames = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "weds": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}
