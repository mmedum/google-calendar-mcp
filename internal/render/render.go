// Package render turns the model into the text a person and a model
// read.
//
// Two rules run through everything here:
//
//   - Every read states its window, the zone it is rendered in and where
//     that zone came from, and whether the list is complete (§4.5). A
//     schedule that does not say which Thursday it is showing can be
//     read as any Thursday.
//   - An all-day event is rendered as a date, never as 00:00 (§4.1).
//     Printing midnight for an all-day event is how a reader concludes
//     it starts at midnight, and how the next implementer concludes it
//     is an instant.
package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// Schedule is everything a read of events produced.
type Schedule struct {
	Window    when.Window
	Zone      when.Zone
	Calendars []string
	Events    []model.Event
	// Matched is how many the query found; len(Events) is how many are
	// shown. They differ when the budget truncated the read.
	Matched   int
	Truncated bool
	// NextPageToken lets the caller continue.
	NextPageToken string
	// Requests is how many API calls this cost (§4.7).
	Requests int
	// Expanded says whether recurring series were expanded into
	// instances (§2.9). The two return different things and a reader
	// must know which they got.
	Expanded bool
}

// Text renders a schedule.
func (s Schedule) Text() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n", s.Window)
	fmt.Fprintf(&b, "%s\n", s.Zone.Explain())
	if len(s.Calendars) > 0 {
		fmt.Fprintf(&b, "calendars: %s\n", strings.Join(s.Calendars, ", "))
	}
	if s.Expanded {
		b.WriteString("recurring events are expanded into their occurrences\n")
	} else {
		b.WriteString("recurring events are shown once, as a series with its rule\n")
	}
	b.WriteString("\n")

	if len(s.Events) == 0 {
		b.WriteString("No events in that window.\n")
		b.WriteString(s.footer())
		return b.String()
	}

	// Group by local day so a reader sees a schedule rather than a list.
	byDay := map[string][]model.Event{}
	var order []string
	for _, e := range s.Events {
		k := dayKey(e)
		if _, seen := byDay[k]; !seen {
			order = append(order, k)
		}
		byDay[k] = append(byDay[k], e)
	}
	sort.Strings(order)

	for _, day := range order {
		fmt.Fprintf(&b, "%s\n", day)
		for _, e := range byDay[day] {
			fmt.Fprintf(&b, "  %s\n", EventLine(e, s.Zone))
		}
		b.WriteString("\n")
	}
	b.WriteString(s.footer())
	return b.String()
}

func (s Schedule) footer() string {
	var b strings.Builder
	if s.Truncated {
		fmt.Fprintf(&b, "Showing %d of %d events — the read hit its budget.", len(s.Events), s.Matched)
		if s.NextPageToken != "" {
			b.WriteString(" Pass page_token to continue.")
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "%d event%s.\n", len(s.Events), plural(len(s.Events)))
	}
	if s.Requests > 1 {
		fmt.Fprintf(&b, "(%d API requests)\n", s.Requests)
	}
	return b.String()
}

// EventLine is one event on one line.
func EventLine(e model.Event, z when.Zone) string {
	var b strings.Builder
	b.WriteString(TimeRange(e, z))
	b.WriteString("  ")
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	b.WriteString(title)

	var tags []string
	if e.Cancelled() {
		tags = append(tags, "cancelled")
	}
	if e.Transparent {
		// Worth saying: it is on the calendar and does not make the
		// person busy, which is the distinction §4.6 turns on.
		tags = append(tags, "free")
	}
	if e.IsSeries() {
		tags = append(tags, "series: "+Recurrence(e.Recurrence))
	}
	if e.IsInstance() {
		tags = append(tags, "one occurrence")
	}
	if t := eventTypeTag(e.Type); t != "" {
		tags = append(tags, t)
	}
	if e.EndInvented {
		tags = append(tags, "no end time set")
	}
	if n := e.GuestCount(); n > 0 {
		tags = append(tags, fmt.Sprintf("%d guest%s", n, plural(n)))
	}
	if e.AttendeesTruncated {
		tags = append(tags, "guest list truncated by Google")
	}
	if e.Location != "" {
		tags = append(tags, "at "+e.Location)
	}
	if len(tags) > 0 {
		fmt.Fprintf(&b, "  [%s]", strings.Join(tags, "; "))
	}
	return b.String()
}

// TimeRange renders an event's span.
//
// The all-day branch prints dates. It must never print a time: an
// all-day event has no instant, and "00:00" is the sentence a reader
// turns into a bug (§4.1).
func TimeRange(e model.Event, z when.Zone) string {
	if e.Start.AllDay {
		// Google's end date is exclusive, so a one-day event ends on the
		// following day. Showing the raw end reads as two days.
		last := e.End.Date.AddDays(-1)
		if e.End.Date.IsZero() || !last.After(e.Start.Date) {
			return fmt.Sprintf("%s  all day", e.Start.Date)
		}
		return fmt.Sprintf("%s to %s  all day", e.Start.Date, last)
	}
	if e.Start.At.IsZero() {
		return "(no start)"
	}
	start := e.Start.At.T.Format("15:04")
	if e.End.At.IsZero() {
		return start
	}
	// A span crossing local midnight needs the date, or it reads as
	// ending before it began.
	if e.End.At.Date() != e.Start.At.Date() {
		return fmt.Sprintf("%s to %s %s", start, e.End.At.Date(), e.End.At.T.Format("15:04"))
	}
	return fmt.Sprintf("%s-%s", start, e.End.At.T.Format("15:04"))
}

// Recurrence explains an RRULE in prose, falling back to the rule itself.
//
// It explains rather than reformats: a caller who wrote the rule should
// see their rule, and a reader who did not should see what it means.
func Recurrence(rules []string) string {
	for _, r := range rules {
		if !strings.HasPrefix(strings.ToUpper(r), "RRULE:") {
			continue
		}
		if s := explainRRule(r); s != "" {
			return s
		}
		return r
	}
	if len(rules) > 0 {
		return rules[0]
	}
	return "repeats"
}

func explainRRule(rule string) string {
	body := rule[strings.Index(rule, ":")+1:]
	parts := map[string]string{}
	for _, kv := range strings.Split(body, ";") {
		if i := strings.Index(kv, "="); i > 0 {
			parts[strings.ToUpper(kv[:i])] = kv[i+1:]
		}
	}
	freq := strings.ToUpper(parts["FREQ"])
	var out string
	interval := parts["INTERVAL"]
	switch freq {
	case "DAILY":
		out = "every day"
		if interval != "" && interval != "1" {
			out = "every " + interval + " days"
		}
	case "WEEKLY":
		out = "every week"
		if interval != "" && interval != "1" {
			out = "every " + interval + " weeks"
		}
		if d := parts["BYDAY"]; d != "" {
			out += " on " + weekdays(d)
		}
	case "MONTHLY":
		out = "every month"
		if interval != "" && interval != "1" {
			out = "every " + interval + " months"
		}
	case "YEARLY":
		out = "every year"
	default:
		return ""
	}
	if c := parts["COUNT"]; c != "" {
		out += ", " + c + " times"
	}
	if u := parts["UNTIL"]; u != "" {
		out += ", until " + u
	}
	return out
}

var dayNames = map[string]string{
	"MO": "Monday", "TU": "Tuesday", "WE": "Wednesday", "TH": "Thursday",
	"FR": "Friday", "SA": "Saturday", "SU": "Sunday",
}

func weekdays(byday string) string {
	var out []string
	for _, d := range strings.Split(byday, ",") {
		d = strings.ToUpper(strings.TrimSpace(d))
		// A BYDAY can carry an ordinal, as in "2TU".
		key := d
		if len(d) > 2 {
			key = d[len(d)-2:]
		}
		if name, ok := dayNames[key]; ok {
			out = append(out, name)
		} else {
			out = append(out, d)
		}
	}
	return strings.Join(out, ", ")
}

// eventTypeTag names the event types that are not ordinary meetings
// (§2.12), so a model does not treat a birthday as something it can
// reschedule.
func eventTypeTag(t string) string {
	switch t {
	case gcal.EventTypeBirthday:
		return "birthday"
	case gcal.EventTypeFocusTime:
		return "focus time"
	case gcal.EventTypeOutOfOffice:
		return "out of office"
	case gcal.EventTypeWorkingLocation:
		return "working location"
	case gcal.EventTypeFromGmail:
		return "from Gmail; cannot be edited here"
	default:
		return ""
	}
}

// dayKey groups events by their LOCAL day. It takes no zone: an event's
// times were already read in the schedule's zone by the time they reach
// here, so projecting again would be a second chance to get it wrong.
func dayKey(e model.Event) string {
	if e.Start.AllDay {
		return fmt.Sprintf("%s (%s)", e.Start.Date, e.Start.Date.Weekday())
	}
	if e.Start.At.IsZero() {
		return "(undated)"
	}
	d := e.Start.At.Date()
	return fmt.Sprintf("%s (%s)", d, d.Weekday())
}

// ----------------------------------------------------------- calendars

// CalendarList renders the subscribed calendars.
func CalendarList(cals []model.Calendar) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d calendar%s.\n\n", len(cals), plural(len(cals)))
	for _, c := range cals {
		b.WriteString(CalendarLine(c))
		b.WriteString("\n")
	}
	return b.String()
}

// CalendarLine is one calendar.
func CalendarLine(c model.Calendar) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", c.Title)
	if c.Original != "" {
		fmt.Fprintf(&b, "  (you renamed this; others see %q)\n", c.Original)
	}
	fmt.Fprintf(&b, "  id: %s\n", c.ID)
	fmt.Fprintf(&b, "  time zone: %s\n", c.TimeZone)
	fmt.Fprintf(&b, "  your access: %s — %s\n", c.Role, gcal.RoleMeans(c.Role))
	var flags []string
	if c.Primary {
		flags = append(flags, "your primary calendar")
	}
	if c.Hidden {
		flags = append(flags, "hidden in the Calendar UI")
	}
	if !c.Selected {
		flags = append(flags, "not shown in the Calendar UI")
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, "  %s\n", strings.Join(flags, "; "))
	}
	return b.String()
}

// ---------------------------------------------------------- availability

// AvailabilityReport is what check_availability returns.
type AvailabilityReport struct {
	Window   when.Window
	Zone     when.Zone
	Answers  []model.Availability
	Requests int
}

// Text renders availability, keeping "unknown" distinct from "free".
func (r AvailabilityReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n\n", r.Window, r.Zone.Explain())

	for _, a := range r.Answers {
		fmt.Fprintf(&b, "%s\n", a.CalendarID)
		switch {
		case a.Unknown:
			// Never "free". §4.6: a model that cannot tell these apart
			// will book over somebody.
			fmt.Fprintf(&b, "  UNKNOWN — this calendar could not be read (%s). "+
				"Do not treat this as free.\n", a.Reason)
		case len(a.Busy) == 0:
			b.WriteString("  free for the whole window\n")
		default:
			for _, busy := range a.Busy {
				fmt.Fprintf(&b, "  busy %s-%s\n",
					busy.Start.T.Format("2006-01-02 15:04"), busy.End.T.Format("15:04"))
			}
		}
	}
	if r.Requests > 1 {
		fmt.Fprintf(&b, "\n(%d API requests)\n", r.Requests)
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Duration renders a span the way a person says it.
func Duration(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%dm", h, m)
	}
}
