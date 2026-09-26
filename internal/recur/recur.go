// Package recur is the recurrence model: the RFC 5545 rules Google puts
// in an event's recurrence field, the occurrences they expand to, and
// the three scopes a write to a series has to choose between.
//
// It has no network and no clock, like internal/when, and it expands
// against a zone rather than against an instant. That is the whole point
// of the package:
//
// # A recurrence is a wall clock, not an interval
//
// A weekly event at 09:00 is at 09:00 next week too, and the number of
// hours in between is 167 or 169 whenever a daylight-saving transition
// falls in the gap. Expanding by adding 7*24h to an instant drifts an
// hour at every boundary, which is §3's second row and the defect every
// surveyed server ships. So the walker here advances DATES and carries
// the wall-clock time of day unchanged, and each occurrence is resolved
// in the series' own zone at the end (§2.2).
//
// # The server sends the caller's rule
//
// Parsing is for validating and for explaining a rule back in prose
// (§6.4). The string that goes to Google is the string that came in:
// composing an RRULE from struct fields is where a BYDAY gets lost.
package recur

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// ErrInvalid wraps every parse and validation failure here.
var ErrInvalid = errors.New("recur: invalid")

// Freq is an RRULE's FREQ.
type Freq string

// The frequencies this package expands. Google accepts SECONDLY through
// YEARLY; the four below are what a calendar event uses, and anything
// else is carried but not expanded rather than expanded wrongly.
const (
	Daily   Freq = "DAILY"
	Weekly  Freq = "WEEKLY"
	Monthly Freq = "MONTHLY"
	Yearly  Freq = "YEARLY"
)

// Expandable reports whether this package can walk this frequency.
func (f Freq) Expandable() bool {
	switch f {
	case Daily, Weekly, Monthly, Yearly:
		return true
	default:
		return false
	}
}

// ByDay is one BYDAY entry: a weekday, with the ordinal that may precede
// it. "2TU" is the second Tuesday, "-1FR" the last Friday, "TU" every
// Tuesday.
type ByDay struct {
	Day time.Weekday
	// Ordinal is 0 when the entry carries none.
	Ordinal int
}

// Rule is one parsed RRULE.
//
// Raw is the line it came from and is what a write sends. Everything
// else is for validating, explaining and expanding.
type Rule struct {
	Raw      string
	Freq     Freq
	Interval int
	// Count is 0 when the rule carries none. COUNT and UNTIL are
	// mutually exclusive in RFC 5545 and this package refuses a rule
	// carrying both.
	Count int
	// Until is the last instant the series may reach, and UntilDate is
	// set instead when the rule carries a DATE. Both are zero when the
	// rule is open-ended.
	Until      time.Time
	UntilDate  when.Date
	ByDay      []ByDay
	ByMonthDay []int
	ByMonth    []time.Month
	WeekStart  time.Weekday
	// Unknown holds the parts this package does not model, so Explain
	// can admit to them rather than describing a rule it only half read.
	Unknown []string
}

// Open reports whether the rule runs forever.
func (r Rule) Open() bool { return r.Count == 0 && r.Until.IsZero() && r.UntilDate.IsZero() }

// Set is an event's whole recurrence field: the rule plus the dates
// added and removed around it.
type Set struct {
	// Lines are the recurrence strings exactly as they arrived.
	Lines []string
	// Rule is the first RRULE. Nil when the field carries none, which is
	// what an event with only RDATEs looks like.
	Rule *Rule
	// Excluded are EXDATE points: occurrences the rule produces and the
	// series does not have.
	Excluded []Point
	// Added are RDATE points.
	Added []Point
	// Unsupported names the lines this package will not expand, EXRULE
	// above all. Expand refuses rather than returning a set that is
	// quietly too large.
	Unsupported []string
}

// Recurring reports whether the set describes a repeating event.
func (s Set) Recurring() bool { return s.Rule != nil || len(s.Added) > 0 }

// Point is one EXDATE or RDATE value: a date, or a wall clock with the
// zone the line named.
//
// It keeps the two apart for the same reason internal/when does. An
// EXDATE on an all-day series is a date and has no instant.
type Point struct {
	AllDay bool
	Date   when.Date
	// Wall is the local reading for a timed point; Zone is the TZID the
	// line carried, empty when it carried none.
	Wall time.Time
	Zone string
	// loc is Zone already resolved. time.LoadLocation reads and parses a
	// zoneinfo file on every call and Go does not memoize it, so a line
	// listing twenty excluded dates under one TZID would otherwise read
	// the same file twenty times, inside a loop over input this package
	// does not control.
	loc *time.Location
	// UTC is set when the value ended in Z, which fixes the instant
	// whatever the series' own zone is.
	UTC bool
}

// Instant resolves a timed point in loc, which is the series' zone when
// the line named none.
func (p Point) Instant(loc *time.Location) (time.Time, bool) {
	if p.AllDay {
		return time.Time{}, false
	}
	switch {
	case p.UTC:
		return p.Wall.UTC(), true
	case p.loc != nil:
		return wallIn(p.Wall, p.loc), true
	case p.Zone != "":
		// A TZID this package could not resolve. Falling back to the
		// series' zone would put the point an offset away from where the
		// line says it is, so it has no instant at all.
		return time.Time{}, false
	case loc != nil:
		return wallIn(p.Wall, loc), true
	default:
		return time.Time{}, false
	}
}

func wallIn(w time.Time, loc *time.Location) time.Time {
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, loc)
}

// Parse reads an event's recurrence field.
//
// It never fails on a line it does not understand: Google is allowed to
// hand back a property this package has not met, and refusing to read
// the event would be the wrong answer. Such lines land in Unsupported,
// where Expand refuses to guess and Explain says so.
func Parse(lines []string) (Set, error) {
	set := Set{Lines: lines}
	for _, line := range lines {
		name, value := splitLine(line)
		switch strings.ToUpper(name) {
		case "RRULE":
			r, err := ParseRule(line)
			if err != nil {
				return Set{}, err
			}
			if set.Rule != nil {
				// More than one RRULE is legal in RFC 5545 and Google
				// does not produce it. Carrying the second as
				// unsupported keeps the expansion honest: this package
				// would otherwise expand the first and silently drop
				// every occurrence the second adds.
				set.Unsupported = append(set.Unsupported, line)
				continue
			}
			set.Rule = &r
		case "EXDATE":
			pts, err := parsePoints(line, value)
			if err != nil {
				return Set{}, err
			}
			set.Excluded = append(set.Excluded, pts...)
		case "RDATE":
			pts, err := parsePoints(line, value)
			if err != nil {
				return Set{}, err
			}
			set.Added = append(set.Added, pts...)
		default:
			set.Unsupported = append(set.Unsupported, line)
		}
	}
	return set, nil
}

// ParseRule reads one RRULE line, with or without its "RRULE:" prefix.
func ParseRule(line string) (Rule, error) {
	name, body := splitLine(line)
	if body == "" {
		body = name
	}
	r := Rule{Raw: line, Interval: 1, WeekStart: time.Monday}
	seen := map[string]bool{}
	for _, part := range strings.Split(body, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return Rule{}, fmt.Errorf("%w: %q is not KEY=VALUE in %q", ErrInvalid, part, line)
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if seen[key] {
			return Rule{}, fmt.Errorf("%w: %s appears twice in %q", ErrInvalid, key, line)
		}
		seen[key] = true

		var err error
		switch key {
		case "FREQ":
			r.Freq = Freq(strings.ToUpper(value))
		case "INTERVAL":
			r.Interval, err = positive(key, value)
		case "COUNT":
			r.Count, err = positive(key, value)
		case "UNTIL":
			err = r.parseUntil(value)
		case "BYDAY":
			r.ByDay, err = parseByDay(value)
		case "BYMONTHDAY":
			r.ByMonthDay, err = parseMonthDays(value)
		case "BYMONTH":
			r.ByMonth, err = parseMonths(value)
		case "WKST":
			r.WeekStart, err = parseWeekday(value)
		default:
			r.Unknown = append(r.Unknown, part)
		}
		if err != nil {
			return Rule{}, err
		}
	}

	if r.Freq == "" {
		return Rule{}, fmt.Errorf("%w: %q has no FREQ, which RFC 5545 requires", ErrInvalid, line)
	}
	if r.Count > 0 && (!r.Until.IsZero() || !r.UntilDate.IsZero()) {
		return Rule{}, fmt.Errorf("%w: %q sets both COUNT and UNTIL, which RFC 5545 forbids", ErrInvalid, line)
	}
	return r, nil
}

func (r *Rule) parseUntil(v string) error {
	// A DATE-TIME UNTIL is UTC on a zoned series, and RFC 5545 requires
	// the Z. A bare DATE is what an all-day series carries.
	switch {
	case strings.HasSuffix(v, "Z"):
		t, err := parseBasicUTC(v)
		if err != nil {
			return fmt.Errorf("%w: UNTIL %q is not a UTC timestamp", ErrInvalid, v)
		}
		r.Until = t
		return nil
	case len(v) == 8:
		d, err := parseBasicDate(v)
		if err != nil {
			return fmt.Errorf("%w: UNTIL %q is not a date", ErrInvalid, v)
		}
		r.UntilDate = d
		return nil
	default:
		// A local DATE-TIME without Z is legal only on a floating
		// series, which a Calendar event never is. Reading it as UTC
		// would move the end of the series by the offset.
		return fmt.Errorf("%w: UNTIL %q is neither a yyyymmdd date nor a UTC timestamp ending in Z", ErrInvalid, v)
	}
}

// UntilZoned reports the rule's end as an instant in loc, for a rule
// that carries one.
func (r Rule) UntilZoned(loc *time.Location) (when.Zoned, bool) {
	if !r.Until.IsZero() {
		return when.NewZoned(r.Until, loc), true
	}
	return when.Zoned{}, false
}

func splitLine(line string) (name, value string) {
	line = strings.TrimSpace(line)
	i := strings.Index(line, ":")
	if i < 0 {
		return line, ""
	}
	// A property can carry parameters: EXDATE;TZID=Europe/Copenhagen:...
	name = line[:i]
	if j := strings.Index(name, ";"); j >= 0 {
		name = name[:j]
	}
	return name, line[i+1:]
}

// parsePoints reads an EXDATE or RDATE line, which may carry several
// comma-separated values and a TZID parameter that applies to all of
// them.
func parsePoints(line, value string) ([]Point, error) {
	params := line[:strings.Index(line, ":")+1]
	tzid := ""
	valueDate := false
	for _, p := range strings.Split(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSuffix(p, ":"), "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "TZID":
			tzid = strings.TrimSpace(v)
		case "VALUE":
			valueDate = strings.EqualFold(strings.TrimSpace(v), "DATE")
		}
	}

	// One line, one TZID, one zone lookup — however many values follow.
	var loc *time.Location
	if tzid != "" {
		if z, err := when.LoadLocation(tzid); err == nil {
			loc = z
		}
	}

	var out []Point
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, err := parsePoint(v, tzid, valueDate)
		if err != nil {
			return nil, err
		}
		p.loc = loc
		out = append(out, p)
	}
	return out, nil
}

func parsePoint(v, tzid string, valueDate bool) (Point, error) {
	if valueDate || len(v) == 8 {
		d, err := parseBasicDate(v)
		if err != nil {
			return Point{}, fmt.Errorf("%w: %q is not a date", ErrInvalid, v)
		}
		return Point{AllDay: true, Date: d}, nil
	}
	if strings.HasSuffix(v, "Z") {
		t, err := parseBasicUTC(v)
		if err != nil {
			return Point{}, fmt.Errorf("%w: %q is not a UTC timestamp", ErrInvalid, v)
		}
		return Point{Wall: t, UTC: true}, nil
	}
	t, err := time.Parse("20060102T150405", v)
	if err != nil {
		return Point{}, fmt.Errorf("%w: %q is not a yyyymmddThhmmss local time", ErrInvalid, v)
	}
	return Point{Wall: t, Zone: tzid}, nil
}

// RFC 5545 writes dates and times in the basic ISO 8601 form —
// "20260501", "20260501T215959Z" — which is not what internal/when
// parses. These two are the only places that shape enters the package.
func parseBasicDate(v string) (when.Date, error) {
	if len(v) != 8 {
		return when.Date{}, fmt.Errorf("%w: %q is not a yyyymmdd date", ErrInvalid, v)
	}
	return when.ParseDate(v[:4] + "-" + v[4:6] + "-" + v[6:])
}

func parseBasicUTC(v string) (time.Time, error) {
	return time.Parse("20060102T150405Z", v)
}

func positive(key, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %s must be a positive whole number, got %q", ErrInvalid, key, v)
	}
	return n, nil
}

var weekdayCodes = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

func parseWeekday(v string) (time.Weekday, error) {
	d, ok := weekdayCodes[strings.ToUpper(strings.TrimSpace(v))]
	if !ok {
		return 0, fmt.Errorf("%w: %q is not a two-letter weekday", ErrInvalid, v)
	}
	return d, nil
}

func parseByDay(v string) ([]ByDay, error) {
	var out []ByDay
	for _, part := range strings.Split(v, ",") {
		part = strings.ToUpper(strings.TrimSpace(part))
		if len(part) < 2 {
			return nil, fmt.Errorf("%w: %q is not a BYDAY entry", ErrInvalid, part)
		}
		code := part[len(part)-2:]
		day, ok := weekdayCodes[code]
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a two-letter weekday", ErrInvalid, code)
		}
		entry := ByDay{Day: day}
		if prefix := part[:len(part)-2]; prefix != "" {
			n, err := strconv.Atoi(prefix)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("%w: %q is not an ordinal in %q", ErrInvalid, prefix, part)
			}
			entry.Ordinal = n
		}
		out = append(out, entry)
	}
	return out, nil
}

func parseMonthDays(v string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n == 0 || n < -31 || n > 31 {
			return nil, fmt.Errorf("%w: BYMONTHDAY %q is not a day of the month", ErrInvalid, part)
		}
		out = append(out, n)
	}
	return out, nil
}

func parseMonths(v string) ([]time.Month, error) {
	var out []time.Month
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 12 {
			return nil, fmt.Errorf("%w: BYMONTH %q is not a month", ErrInvalid, part)
		}
		out = append(out, time.Month(n))
	}
	return out, nil
}
