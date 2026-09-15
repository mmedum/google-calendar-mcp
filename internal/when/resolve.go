package when

import (
	"fmt"
	"time"
)

// ZoneSource says where a resolved zone came from. Every read names it
// (§4.1), because "which Thursday" and "whose Thursday" are the two ways
// a schedule answer goes wrong, and a caller cannot check the second
// unless the server says what it assumed.
type ZoneSource string

// ZoneSource values, in the order Resolve tries them.
const (
	// ZoneFromCall: the caller named a zone on the tool call.
	ZoneFromCall ZoneSource = "call"
	// ZoneFromCalendar: the target calendar's own timeZone.
	ZoneFromCalendar ZoneSource = "calendar"
	// ZoneFromSettings: the user's timezone setting.
	ZoneFromSettings ZoneSource = "settings"
)

// Zone is a resolved zone and the story of how it was chosen.
type Zone struct {
	Loc    *time.Location
	Source ZoneSource
}

// Name is the IANA name.
func (z Zone) Name() string {
	if z.Loc == nil {
		return ""
	}
	return z.Loc.String()
}

// Explain is the sentence a result uses to say which zone it used and
// why, so a caller who meant a different one can see it.
func (z Zone) Explain() string {
	switch z.Source {
	case ZoneFromCall:
		return fmt.Sprintf("times are in %s, as requested", z.Name())
	case ZoneFromCalendar:
		return fmt.Sprintf("times are in %s, the calendar's own time zone", z.Name())
	case ZoneFromSettings:
		return fmt.Sprintf("times are in %s, from your Google Calendar settings", z.Name())
	default:
		return fmt.Sprintf("times are in %s", z.Name())
	}
}

// ErrNoZone is returned when none of the three sources produced a zone.
//
// This is a refusal rather than a fallback on purpose. The obvious
// fallbacks are the process's zone, which is UTC in a container and the
// maintainer's zone on the maintainer's laptop, and UTC itself, which is
// nobody's working day. Either would answer a scheduling question in a
// zone the user never chose and never sees, which is §3's last row.
var ErrNoZone = fmt.Errorf("%w: no time zone available — none was given on the call, the calendar "+
	"has none, and your Google Calendar settings have none. Pass an IANA zone such as Europe/Copenhagen", ErrInvalid)

// Resolve picks the zone, in the order §4.1 fixes: the call, then the
// calendar, then the user's settings. Empty strings are skipped, so a
// caller can pass through whatever it has.
//
// It never falls back to the process's zone. See ErrNoZone.
func Resolve(fromCall, fromCalendar, fromSettings string) (Zone, error) {
	for _, c := range []struct {
		name   string
		source ZoneSource
	}{
		{fromCall, ZoneFromCall},
		{fromCalendar, ZoneFromCalendar},
		{fromSettings, ZoneFromSettings},
	} {
		if c.name == "" {
			continue
		}
		loc, err := LoadLocation(c.name)
		if err != nil {
			// A zone the caller named explicitly is an error worth
			// reporting; a broken one stored on a calendar or in the
			// user's settings is not something the caller can fix, so
			// fall through to the next source and let the result say
			// which zone was used.
			if c.source == ZoneFromCall {
				return Zone{}, err
			}
			continue
		}
		return Zone{Loc: loc, Source: c.source}, nil
	}
	return Zone{}, ErrNoZone
}
