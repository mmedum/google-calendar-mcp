// Package when is this server's time model. It has no network and no
// clock of its own: the clock is injected, so a test can stand at a
// daylight-saving boundary and stay there.
//
// The package exists because of one rule, and the rule is the reason
// half the calendar integrations surveyed render all-day events on the
// wrong day (docs/architecture.md §3):
//
// # A date is not a time
//
// An all-day event happens on a Date. It has no instant, no offset and
// no zone. "2026-03-14" is the fourteenth of March everywhere, and the
// moment you give it an instant you have picked somebody's midnight —
// which is the thirteenth for every user west of whoever picked it.
//
// So there is no Date -> Zoned conversion in this package. Not an
// awkward one, not one with a comment: none. A caller who needs an
// instant for a Date has to supply a zone and say so, through
// Date.StartIn, which names what it did in its result. The renderer is
// the only caller that does this, for display, labelled.
//
// # A zone is not an offset
//
// An offset is a fact about one moment; a zone is a rule. "+02:00" is
// enough to place a single instant and not enough to expand a
// recurrence, because the next occurrence may fall the other side of a
// transition. Google requires timeZone on a recurring event's start and
// end for exactly this reason (§2.2), so Zoned carries the IANA name
// and not just the offset.
package when

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RFC3339 layouts Calendar uses on the wire.
const (
	// DateLayout is an all-day event's date: "2026-03-14".
	DateLayout = "2006-01-02"
	// DateTimeLayout is an instant with an offset.
	DateTimeLayout = time.RFC3339
)

// ErrInvalid wraps every parse and validation failure in this package.
var ErrInvalid = errors.New("when: invalid")

// Clock supplies the current instant. The server injects one; tests
// inject a fixed one. Nothing in this package calls time.Now.
type Clock func() time.Time

// SystemClock is the production clock.
func SystemClock() Clock { return time.Now }

// FixedClock returns a Clock that always reports t.
func FixedClock(t time.Time) Clock { return func() time.Time { return t } }

// ---------------------------------------------------------------- Date

// Date is a calendar day with no zone and no instant: the "date" half of
// Google's EventDateTime, which is what an all-day event carries.
//
// It is deliberately not a time.Time. A time.Time is an instant, and
// every bug this package exists to prevent begins with storing a day as
// one.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// ParseDate reads "yyyy-mm-dd".
func ParseDate(s string) (Date, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("%w: date %q is not yyyy-mm-dd", ErrInvalid, s)
	}
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}, nil
}

// MustParseDate is ParseDate for tests and constants.
func MustParseDate(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// String renders "yyyy-mm-dd", which is the wire format.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// IsZero reports whether the Date was never set.
func (d Date) IsZero() bool { return d == Date{} }

// AddDays returns the date n days later, n possibly negative.
func (d Date) AddDays(n int) Date {
	t := time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// Before reports whether d falls before other.
func (d Date) Before(other Date) bool { return d.compare(other) < 0 }

// After reports whether d falls after other.
func (d Date) After(other Date) bool { return d.compare(other) > 0 }

func (d Date) compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return d.Year - o.Year
	case d.Month != o.Month:
		return int(d.Month) - int(o.Month)
	default:
		return d.Day - o.Day
	}
}

// Weekday needs a zone-free anchor, and UTC is the arbitrary one that is
// correct: the day of the week of a bare date is the same everywhere,
// because it is a property of the date and not of any instant.
func (d Date) Weekday() time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Weekday()
}

// StartIn is the only bridge from a Date to an instant, and it is
// explicit on purpose.
//
// It returns midnight at the start of this date in loc, and it is the
// caller's job to have a reason for choosing loc. There is no default
// and there is no version of this that takes no zone, because the whole
// class of defect this package guards against is a Date silently
// acquiring somebody's midnight.
func (d Date) StartIn(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// EndExclusiveIn is the instant the day after this one starts in loc.
// All-day events on the wire are half-open the same way: Google's end
// date for a one-day event is the following day.
func (d Date) EndExclusiveIn(loc *time.Location) time.Time {
	return d.AddDays(1).StartIn(loc)
}

// --------------------------------------------------------------- Zoned

// Zoned is an instant together with the IANA zone it was expressed in.
//
// Both halves are load-bearing. The instant places it on the timeline;
// the zone is what lets a recurrence be expanded correctly across a
// daylight-saving transition, and what lets a read say which Thursday it
// was looking at.
type Zoned struct {
	// T is the instant. Its own location is set to Loc, so t.Format
	// renders in the right wall clock.
	T time.Time
	// Loc is the IANA zone. Never nil in a valid Zoned.
	Loc *time.Location
}

// LoadLocation resolves an IANA zone name.
//
// It rejects the empty string and it rejects "Local". "Local" is the
// process's zone, which on a user's laptop is plausible and in a
// container is UTC, and §4.1 says the zone is read from the call, the
// calendar or the user's settings — never from the process. A zone that
// silently means "wherever this happens to be running" is exactly the
// bug.
func LoadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: no time zone given", ErrInvalid)
	}
	if strings.EqualFold(name, "Local") {
		return nil, fmt.Errorf("%w: time zone %q means the machine this server happens to run on; "+
			"name an IANA zone such as Europe/Copenhagen, or let the calendar's own zone be used", ErrInvalid, name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: time zone %q is not an IANA zone name: %w", ErrInvalid, name, err)
	}
	return loc, nil
}

// ParseZoned reads an RFC3339 timestamp and places it in loc.
//
// The string must carry an offset; RFC3339 requires one. When loc is
// non-nil the instant is rendered in loc afterwards, so the returned
// Zoned reports the wall clock a user in that zone would read. The
// instant itself never moves.
func ParseZoned(s string, loc *time.Location) (Zoned, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse(DateTimeLayout, s)
	if err != nil {
		return Zoned{}, fmt.Errorf("%w: %q is not an RFC3339 timestamp with an offset", ErrInvalid, s)
	}
	if loc == nil {
		loc = t.Location()
	}
	return Zoned{T: t.In(loc), Loc: loc}, nil
}

// NewZoned builds a Zoned from an instant and a zone.
func NewZoned(t time.Time, loc *time.Location) Zoned {
	if loc == nil {
		loc = time.UTC
	}
	return Zoned{T: t.In(loc), Loc: loc}
}

// Wall builds a Zoned from a wall-clock reading in loc.
//
// Two wall-clock readings a year have no unique instant and one has
// none at all, and time.Date resolves both silently. Callers that care
// use WallStrict.
func Wall(year int, month time.Month, day, hour, minute int, loc *time.Location) Zoned {
	return Zoned{T: time.Date(year, month, day, hour, minute, 0, 0, loc), Loc: loc}
}

// IsZero reports whether the Zoned was never set.
func (z Zoned) IsZero() bool { return z.T.IsZero() }

// String renders RFC3339 with the offset, which is the wire format.
func (z Zoned) String() string { return z.T.Format(DateTimeLayout) }

// ZoneName is the IANA name, for the timeZone field Google wants
// alongside dateTime on every write (§4.1).
func (z Zoned) ZoneName() string {
	if z.Loc == nil {
		return ""
	}
	return z.Loc.String()
}

// In returns the same instant read in another zone.
func (z Zoned) In(loc *time.Location) Zoned { return Zoned{T: z.T.In(loc), Loc: loc} }

// Date is the calendar day this instant falls on, in its own zone.
//
// This is a projection and not a conversion: it answers "which day is it
// where this event is" and throws the time away. The reverse does not
// exist, which is the point of the package.
func (z Zoned) Date() Date {
	return Date{Year: z.T.Year(), Month: z.T.Month(), Day: z.T.Day()}
}

// Before reports whether z is earlier than other on the timeline.
func (z Zoned) Before(other Zoned) bool { return z.T.Before(other.T) }

// After reports whether z is later than other on the timeline.
func (z Zoned) After(other Zoned) bool { return z.T.After(other.T) }

// WallStrict builds a Zoned from a wall-clock reading and reports what
// the zone did with it.
//
// Two readings a year are not one instant. In a spring-forward gap the
// reading does not exist; in an autumn fold it happens twice. Go's
// time.Date resolves both without saying so — the gap by rolling
// forward, the fold by taking the first. Silence is the wrong answer
// when a user asks for 02:30 on a morning that has no 02:30, so this
// reports which case it hit and lets the caller decide whether to say
// something.
func WallStrict(year int, month time.Month, day, hour, minute int, loc *time.Location) (Zoned, Ambiguity) {
	t := time.Date(year, month, day, hour, minute, 0, 0, loc)

	// A gap: the reading Go produced is not the reading we asked for,
	// because it rolled forward over the missing hour.
	if t.Hour() != hour || t.Minute() != minute || t.Day() != day {
		return Zoned{T: t, Loc: loc}, Skipped
	}

	// A fold: the same wall clock an hour earlier and an hour later
	// carry different offsets, and both render the reading we asked for.
	_, offNow := t.Zone()
	_, offLater := t.Add(time.Hour).Zone()
	_, offEarlier := t.Add(-time.Hour).Zone()
	if offNow != offLater || offNow != offEarlier {
		alt := t.Add(time.Hour)
		if alt.Hour() == hour && alt.Minute() == minute && alt.Day() == day {
			return Zoned{T: t, Loc: loc}, Repeated
		}
		alt = t.Add(-time.Hour)
		if alt.Hour() == hour && alt.Minute() == minute && alt.Day() == day {
			return Zoned{T: t, Loc: loc}, Repeated
		}
	}
	return Zoned{T: t, Loc: loc}, Unique
}

// Ambiguity says what a zone made of a wall-clock reading.
type Ambiguity int

// Ambiguity values.
const (
	// Unique: the reading names exactly one instant.
	Unique Ambiguity = iota
	// Skipped: the reading does not exist, because the clock jumped
	// forward over it. The instant returned is the one the clock moved
	// to.
	Skipped
	// Repeated: the reading happens twice, because the clock went back.
	// The instant returned is the first of the two.
	Repeated
)

func (a Ambiguity) String() string {
	switch a {
	case Skipped:
		return "skipped"
	case Repeated:
		return "repeated"
	default:
		return "unique"
	}
}

// Note is what a result should say about this reading, or "" when there
// is nothing to say.
func (a Ambiguity) Note() string {
	switch a {
	case Skipped:
		return "that wall-clock time does not exist on that day in that zone — the clock jumps forward over it; the next valid time was used"
	case Repeated:
		return "that wall-clock time happens twice on that day in that zone — the clock goes back; the first was used"
	default:
		return ""
	}
}

// -------------------------------------------------------------- Window

// Window is a half-open interval [Start, End) on the timeline, carrying
// the zone it should be read in.
//
// Half-open because that is how calendars compose: an event ending at
// 10:00 and one starting at 10:00 do not overlap.
type Window struct {
	Start Zoned
	End   Zoned
	// Loc is the zone the window should be rendered in. It may differ
	// from Start.Loc when a caller asks to see another calendar's day in
	// their own zone.
	Loc *time.Location
}

// NewWindow builds a window and validates its order.
func NewWindow(start, end Zoned, loc *time.Location) (Window, error) {
	if loc == nil {
		loc = start.Loc
	}
	if loc == nil {
		loc = time.UTC
	}
	if !end.T.After(start.T) {
		return Window{}, fmt.Errorf("%w: window ends at %s, which is not after its start %s",
			ErrInvalid, end.In(loc), start.In(loc))
	}
	return Window{Start: start.In(loc), End: end.In(loc), Loc: loc}, nil
}

// DayWindow is the window covering one calendar day in loc.
//
// It is built from the Date's own start and the following day's start,
// so a day containing a daylight-saving transition is 23 or 25 hours
// long, which is correct and is what a naive start.Add(24*time.Hour)
// gets wrong.
func DayWindow(d Date, loc *time.Location) Window {
	start := d.StartIn(loc)
	end := d.EndExclusiveIn(loc)
	return Window{Start: NewZoned(start, loc), End: NewZoned(end, loc), Loc: loc}
}

// Duration is the window's length on the timeline.
func (w Window) Duration() time.Duration { return w.End.T.Sub(w.Start.T) }

// Contains reports whether z falls inside the half-open window.
func (w Window) Contains(z Zoned) bool {
	return !z.T.Before(w.Start.T) && z.T.Before(w.End.T)
}

// Overlaps reports whether two windows share any instant.
func (w Window) Overlaps(other Window) bool {
	return w.Start.T.Before(other.End.T) && other.Start.T.Before(w.End.T)
}

// String renders the window the way a result should state it: absolute,
// zoned, and naming the zone (§4.5).
func (w Window) String() string {
	return fmt.Sprintf("%s to %s (%s)", w.Start.String(), w.End.String(), w.ZoneName())
}

// ZoneName is the IANA name the window is rendered in.
func (w Window) ZoneName() string {
	if w.Loc == nil {
		return ""
	}
	return w.Loc.String()
}
