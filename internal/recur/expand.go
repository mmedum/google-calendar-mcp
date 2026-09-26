package recur

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// MaxOccurrences bounds every expansion.
//
// An open-ended daily rule has no last occurrence, so a walker without a
// ceiling is an infinite loop with a good explanation. Callers pass
// their own limit; this is the one they cannot exceed.
const MaxOccurrences = 3650

// Occurrences is what an expansion produced.
//
// Truncated and Reason are the reason this is a struct rather than a
// slice: a caller that cannot tell a complete series from a capped one
// will report "10 occurrences" for a series that has no end.
type Occurrences struct {
	// Dates is set for an all-day series, Times for a timed one. Never
	// both: §4.1 holds here too, and an all-day series has no instant to
	// carry.
	Dates []when.Date
	Times []when.Zoned
	// Truncated says the limit stopped the walk before the rule did.
	Truncated bool
	// Reason names what stopped it, for a result to quote.
	Reason string
	// Notes carries what a zone did to a wall-clock reading: a weekly
	// 02:30 meets a morning that has no 02:30 once a year, and §4.1 says
	// the server names it rather than resolving it quietly.
	Notes []string
}

// Count is how many occurrences came back.
func (o Occurrences) Count() int {
	if len(o.Dates) > 0 {
		return len(o.Dates)
	}
	return len(o.Times)
}

// ExpandDates walks an all-day series from start.
//
// from and to bound the walk as a half-open range of dates; a zero to
// means "to the end of the rule", which only terminates for a rule with
// a COUNT or an UNTIL.
func (s Set) ExpandDates(start when.Date, from, to when.Date, limit int) (Occurrences, error) {
	out := Occurrences{}
	if err := s.expandable(); err != nil {
		return out, err
	}
	limit = clampLimit(limit)

	excluded := map[when.Date]bool{}
	for _, p := range s.Excluded {
		if p.AllDay {
			excluded[p.Date] = true
		}
	}

	// An UNTIL that is an instant rather than a date belongs to a timed
	// series, but nothing stops one arriving on an all-day rule. The
	// timed path honored it and this one did not, which is the drift
	// two copies of a loop produce.
	var untilDate when.Date
	if s.Rule != nil && !s.Rule.Until.IsZero() {
		u := s.Rule.Until.UTC()
		untilDate = when.Date{Year: u.Year(), Month: u.Month(), Day: u.Day()}
	}

	wanted := func(d when.Date) bool {
		if excluded[d] {
			return false
		}
		if !from.IsZero() && d.Before(from) {
			return false
		}
		if !to.IsZero() && !d.Before(to) {
			return false
		}
		return untilDate.IsZero() || !d.After(untilDate)
	}

	// One more than the limit, so "the rule ended here" and "the limit
	// stopped it" can be told apart. A series of exactly `limit`
	// occurrences is complete, and reporting it as truncated is what
	// made Reach describe a ten-occurrence series as having no end.
	seen := map[when.Date]bool{}
	dates := make([]when.Date, 0, limit+1)
	walk := s.walker(start)
	for len(dates) <= limit {
		d, ok := walk()
		if !ok {
			break
		}
		if !untilDate.IsZero() && d.After(untilDate) {
			break
		}
		if !to.IsZero() && !d.Before(to) {
			break
		}
		if !wanted(d) || seen[d] {
			continue
		}
		seen[d] = true
		dates = append(dates, d)
	}

	// RDATEs are occurrences too, and they are subject to the same
	// window, the same exclusions and the same limit. Appending them
	// afterward, as the first version did, reported a date 73 years
	// outside the requested window as being inside it.
	for _, p := range s.Added {
		if !p.AllDay || !wanted(p.Date) || seen[p.Date] {
			continue
		}
		seen[p.Date] = true
		dates = append(dates, p.Date)
	}

	sortDates(dates)
	if len(dates) > limit {
		dates = dates[:limit]
		out.Truncated = true
		out.Reason = fmt.Sprintf("stopped at %d occurrences", limit)
	}
	out.Dates = dates
	return out, nil
}

// ExpandTimes walks a timed series from start, in start's own zone.
//
// The zone is load-bearing and is why this takes a when.Zoned rather
// than an instant: each occurrence is the same WALL CLOCK on a later
// date, resolved in that zone afterward. A weekly 09:00 stays 09:00
// across a transition, which is what Google's own expansion does and
// what §2.2 requires the write to carry.
func (s Set) ExpandTimes(start when.Zoned, from, to when.Zoned, limit int) (Occurrences, error) {
	out := Occurrences{}
	if err := s.expandable(); err != nil {
		return out, err
	}
	if start.Loc == nil {
		return out, fmt.Errorf("%w: a timed series cannot be expanded without its time zone", ErrInvalid)
	}
	limit = clampLimit(limit)

	excluded := map[int64]bool{}
	for _, p := range s.Excluded {
		if t, ok := p.Instant(start.Loc); ok {
			excluded[t.Unix()] = true
		}
	}

	hour, minute := start.T.Hour(), start.T.Minute()
	until := time.Time{}
	if s.Rule != nil {
		until = s.Rule.Until
	}

	wanted := func(t time.Time) bool {
		if excluded[t.Unix()] {
			return false
		}
		if !from.IsZero() && t.Before(from.T) {
			return false
		}
		if !to.IsZero() && !t.Before(to.T) {
			return false
		}
		return until.IsZero() || !t.After(until)
	}

	seen := map[int64]bool{}
	times := make([]when.Zoned, 0, limit+1)
	walk := s.walker(start.Date())
	for len(times) <= limit {
		d, ok := walk()
		if !ok {
			break
		}
		z, amb := when.WallStrict(d.Year, d.Month, d.Day, hour, minute, start.Loc)
		if !until.IsZero() && z.T.After(until) {
			break
		}
		if !to.IsZero() && !z.T.Before(to.T) {
			break
		}
		if !wanted(z.T) || seen[z.T.Unix()] {
			continue
		}
		if note := amb.Note(); note != "" {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %s", z, note))
		}
		seen[z.T.Unix()] = true
		times = append(times, z)
	}

	for _, p := range s.Added {
		t, ok := p.Instant(start.Loc)
		if !ok || !wanted(t) || seen[t.Unix()] {
			continue
		}
		seen[t.Unix()] = true
		times = append(times, when.NewZoned(t, start.Loc))
	}

	sortTimes(times)
	if len(times) > limit {
		times = times[:limit]
		out.Truncated = true
		out.Reason = fmt.Sprintf("stopped at %d occurrences", limit)
	}
	out.Times = times
	return out, nil
}

// expandable refuses rather than guessing.
//
// A set carrying an EXRULE or a second RRULE expands to something this
// package cannot compute, and a count that is quietly too large is worse
// than no count: it is the number a caller would put in front of a user.
func (s Set) expandable() error {
	if len(s.Unsupported) > 0 {
		return fmt.Errorf("%w: this series carries %s, which this server does not expand; "+
			"ask Google for the occurrences with list_instances", ErrInvalid, s.Unsupported[0])
	}
	if s.Rule == nil {
		return fmt.Errorf("%w: no RRULE to expand", ErrInvalid)
	}
	if !s.Rule.Freq.Expandable() {
		return fmt.Errorf("%w: FREQ=%s is not one this server expands; "+
			"ask Google for the occurrences with list_instances", ErrInvalid, s.Rule.Freq)
	}
	return nil
}

// walker returns successive dates of the rule, starting at start.
//
// It yields dates and nothing else. Everything about time of day, zones
// and transitions belongs to the caller, which is what keeps the
// daylight-saving rule in one place.
func (s Set) walker(start when.Date) func() (when.Date, bool) {
	r := *s.Rule
	if r.Interval < 1 {
		r.Interval = 1
	}

	// A candidate generator per frequency. Each yields dates in order,
	// and the shared loop below applies COUNT, UNTIL and the BY* filters
	// so those rules live once rather than four times.
	var next func() (when.Date, bool)
	switch r.Freq {
	case Daily:
		cur, first := start, true
		next = func() (when.Date, bool) {
			if !first {
				cur = cur.AddDays(r.Interval)
			}
			first = false
			// The same runaway guard the other frequencies carry. An
			// open-ended rule is stopped by the caller's limit; this
			// stops a rule whose filters never match from walking for
			// ever.
			if cur.Year > start.Year+MaxOccurrences {
				return when.Date{}, false
			}
			return cur, true
		}
	case Weekly:
		next = weeklyWalker(start, r)
	case Monthly:
		next = monthlyWalker(start, r)
	default:
		next = yearlyWalker(start, r)
	}

	byDay := newWeekdaySet(r)
	emitted := 0
	guard := 0
	return func() (when.Date, bool) {
		for {
			guard++
			// The walk is bounded by candidates rather than by
			// occurrences: a BYMONTHDAY=31 monthly rule skips seven
			// months a year, so a counter on emitted dates alone would
			// scan forever on a rule that produces nothing.
			if guard > MaxOccurrences*12 {
				return when.Date{}, false
			}
			if r.Count > 0 && emitted >= r.Count {
				return when.Date{}, false
			}
			d, ok := next()
			if !ok {
				return when.Date{}, false
			}
			if !r.UntilDate.IsZero() && d.After(r.UntilDate) {
				return when.Date{}, false
			}
			if !matches(d, r, byDay) {
				continue
			}
			emitted++
			return d, true
		}
	}
}

// weeklyWalker yields the days of each interval-th week that BYDAY
// names, in order, starting from start's own week.
func weeklyWalker(start when.Date, r Rule) func() (when.Date, bool) {
	days := byDayWeekdays(r)
	if len(days) == 0 {
		days = []time.Weekday{start.Weekday()}
	}
	// In week order, not in the order the caller happened to write them.
	// The loop above this walker stops at the first date past the window
	// or the count, so a walker that yields Wednesday before Monday
	// drops occurrences — and "BYDAY=WE,MO" and "BYDAY=MO,WE" are the
	// same rule with different answers.
	sort.Slice(days, func(i, j int) bool {
		return daysSince(days[i], r.WeekStart) < daysSince(days[j], r.WeekStart)
	})
	// The week runs from WKST, which is what makes INTERVAL>1 land on
	// the weeks the caller means.
	weekStart := start.AddDays(-daysSince(start.Weekday(), r.WeekStart))
	idx := 0
	week := weekStart
	return func() (when.Date, bool) {
		for {
			if idx >= len(days) {
				idx = 0
				week = week.AddDays(7 * r.Interval)
			}
			d := week.AddDays(daysSince(days[idx], r.WeekStart))
			idx++
			if d.Before(start) {
				continue
			}
			return d, true
		}
	}
}

// monthlyWalker yields the candidate days of each interval-th month.
//
// BYDAY with an ordinal ("the second Tuesday") and BYMONTHDAY are both
// resolved here; a month that has no such day yields nothing and the
// walk moves on, which is the behavior RFC 5545 specifies and the
// reason the guard above counts candidates.
func monthlyWalker(start when.Date, r Rule) func() (when.Date, bool) {
	year, month := start.Year, start.Month
	var queue []when.Date
	return func() (when.Date, bool) {
		for {
			if len(queue) > 0 {
				d := queue[0]
				queue = queue[1:]
				if d.Before(start) {
					continue
				}
				return d, true
			}
			queue = monthCandidates(year, month, start, r)
			year, month = addMonths(year, month, r.Interval)
			if year > start.Year+MaxOccurrences {
				return when.Date{}, false
			}
		}
	}
}

// monthCandidates is which days of one month a rule selects.
//
// RFC 5545 intersects BYDAY and BYMONTHDAY when both are present — "the
// 13th, when it is a Friday" — rather than treating them as
// alternatives. The first version took whichever was listed first and
// returned 13 April for FREQ=MONTHLY;BYDAY=FR;BYMONTHDAY=13, which is a
// Monday.
func monthCandidates(year int, month time.Month, start when.Date, r Rule) []when.Date {
	var byDay []when.Date
	for _, bd := range r.ByDay {
		byDay = append(byDay, nthWeekdays(year, month, bd)...)
	}
	var byMonthDay []when.Date
	for _, n := range r.ByMonthDay {
		if d, ok := monthDay(year, month, n); ok {
			byMonthDay = append(byMonthDay, d)
		}
	}

	var out []when.Date
	switch {
	case len(byDay) > 0 && len(byMonthDay) > 0:
		keep := map[when.Date]bool{}
		for _, d := range byDay {
			keep[d] = true
		}
		for _, d := range byMonthDay {
			if keep[d] {
				out = append(out, d)
			}
		}
	case len(byDay) > 0:
		out = byDay
	case len(byMonthDay) > 0:
		out = byMonthDay
	default:
		// No BY* part: the day of the month the series started on. A
		// month that has no such day is skipped rather than rolled into
		// the next one, because a 31st-of-the-month series is not a
		// 1st-of-March series.
		if d, ok := monthDay(year, month, start.Day); ok {
			out = append(out, d)
		}
	}
	sortDates(out)
	return out
}

// yearlyWalker yields one date per interval-th year, honoring BYMONTH
// and BYMONTHDAY when they are present.
func yearlyWalker(start when.Date, r Rule) func() (when.Date, bool) {
	year := start.Year
	var queue []when.Date
	return func() (when.Date, bool) {
		for {
			if len(queue) > 0 {
				d := queue[0]
				queue = queue[1:]
				if d.Before(start) {
					continue
				}
				return d, true
			}
			months := r.ByMonth
			if len(months) == 0 {
				months = []time.Month{start.Month}
			}
			// The same day-of-month selection the monthly walker uses,
			// so BYDAY works here too. It did not: "FREQ=YEARLY;
			// BYMONTH=11;BYDAY=4TH" — the shape of every fourth-Thursday
			// holiday — silently used the start's day of the month
			// instead, and came back wrong rather than refused.
			for _, m := range months {
				queue = append(queue, monthCandidates(year, m, start, r)...)
			}
			sortDates(queue)
			year += r.Interval
			if year > start.Year+MaxOccurrences {
				return when.Date{}, false
			}
		}
	}
}

// weekdaySet is the rule's BYDAY weekdays as a lookup, built once per
// walk rather than per candidate date.
type weekdaySet struct {
	days [7]bool
	any  bool
}

func newWeekdaySet(r Rule) weekdaySet {
	var w weekdaySet
	for _, bd := range r.ByDay {
		w.days[int(bd.Day)] = true
		w.any = true
	}
	return w
}

func (w weekdaySet) has(d time.Weekday) bool { return w.days[int(d)] }

// matches applies the BY* filters that are restrictions rather than
// generators for this frequency.
func matches(d when.Date, r Rule, days weekdaySet) bool {
	if len(r.ByMonth) > 0 && r.Freq != Yearly && !slices.Contains(r.ByMonth, d.Month) {
		return false
	}
	switch r.Freq {
	case Daily:
		if days.any && !days.has(d.Weekday()) {
			return false
		}
		if len(r.ByMonthDay) > 0 && !matchesMonthDay(d, r.ByMonthDay) {
			return false
		}
	case Weekly:
		if len(r.ByMonthDay) > 0 && !matchesMonthDay(d, r.ByMonthDay) {
			return false
		}
	case Monthly, Yearly:
		// Both are generated with their BY* parts applied, and a second
		// filter here would drop the ordinal BYDAY entries the walker
		// just produced.
	}
	return true
}

func matchesMonthDay(d when.Date, days []int) bool {
	last := daysIn(d.Year, d.Month)
	for _, n := range days {
		if n > 0 && n == d.Day {
			return true
		}
		if n < 0 && last+n+1 == d.Day {
			return true
		}
	}
	return false
}

func byDayWeekdays(r Rule) []time.Weekday {
	var out []time.Weekday
	for _, bd := range r.ByDay {
		out = append(out, bd.Day)
	}
	return out
}

// nthWeekdays returns the dates in a month matching one BYDAY entry: all
// of that weekday when the entry has no ordinal, else the one it names,
// counted from the end when the ordinal is negative.
func nthWeekdays(year int, month time.Month, bd ByDay) []when.Date {
	last := daysIn(year, month)
	// Step by sevens from the first matching day rather than walking
	// every date in the month and asking each one what day it is.
	first := when.Date{Year: year, Month: month, Day: 1}.Weekday()
	start := 1 + (7+int(bd.Day)-int(first))%7

	var all []when.Date
	for day := start; day <= last; day += 7 {
		all = append(all, when.Date{Year: year, Month: month, Day: day})
	}
	switch {
	case bd.Ordinal == 0:
		return all
	case bd.Ordinal > 0 && bd.Ordinal <= len(all):
		return []when.Date{all[bd.Ordinal-1]}
	case bd.Ordinal < 0 && -bd.Ordinal <= len(all):
		return []when.Date{all[len(all)+bd.Ordinal]}
	default:
		return nil
	}
}

func monthDay(year int, month time.Month, n int) (when.Date, bool) {
	last := daysIn(year, month)
	day := n
	if n < 0 {
		day = last + n + 1
	}
	if day < 1 || day > last {
		return when.Date{}, false
	}
	return when.Date{Year: year, Month: month, Day: day}, true
}

// daysIn is the length of a month, taken from the zeroth day of the
// next one rather than from a table with a leap-year rule in it.
func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func addMonths(year int, month time.Month, n int) (int, time.Month) {
	t := time.Date(year, month+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	return t.Year(), t.Month()
}

// daysSince is how many days d is after the week's first day.
func daysSince(d, weekStart time.Weekday) int {
	return ((int(d) - int(weekStart)) + 7) % 7
}

func clampLimit(limit int) int {
	if limit <= 0 || limit > MaxOccurrences {
		return MaxOccurrences
	}
	return limit
}

func sortDates(ds []when.Date) {
	sort.Slice(ds, func(i, j int) bool { return ds[i].Before(ds[j]) })
}

func sortTimes(ts []when.Zoned) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
}
