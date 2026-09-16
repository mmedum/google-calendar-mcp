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
	"github.com/mmedum/google-calendar-mcp/internal/recur"
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
		// "Showing 1 of 1 events" is what this said when the read
		// stopped exactly at its budget: the total is not known, because
		// not knowing it is the point of a budget. Say what is true —
		// there are more — rather than a ratio that reads as complete.
		if s.Matched > len(s.Events) {
			fmt.Fprintf(&b, "Showing %d of %d events — the read hit its budget.", len(s.Events), s.Matched)
		} else {
			fmt.Fprintf(&b, "%d event%s — the read hit its budget, and there are more.",
				len(s.Events), plural(len(s.Events)))
		}
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
	b.WriteString(Title(e))

	tags := commonTags(e)
	if e.IsSeries() {
		tags = append(tags, "series: "+Recurrence(e.Recurrence))
	}
	if e.IsInstance() {
		tags = append(tags, "one occurrence")
	}
	if len(tags) > 0 {
		fmt.Fprintf(&b, "  [%s]", strings.Join(tags, "; "))
	}
	return b.String()
}

// Title is the event's title, or a stand-in. An empty title is a real
// thing on a real calendar and rendering nothing for it loses the row.
func Title(e model.Event) string {
	if e.Title == "" {
		return "(no title)"
	}
	return e.Title
}

// commonTags are the marks every view of an event carries.
//
// One list, because the two line renderers each had their own and the
// instances view had quietly stopped showing four of them: an
// out-of-office occurrence, an invented end time, a guest list Google
// truncated, and an event that does not make anybody busy. A tag added
// to one view and not the other is the same defect waiting to happen.
func commonTags(e model.Event) []string {
	var tags []string
	if e.Cancelled() {
		tags = append(tags, "cancelled")
	}
	if e.Transparent {
		// Worth saying: it is on the calendar and does not make the
		// person busy, which is the distinction §4.6 turns on.
		tags = append(tags, "free")
	}
	if t := eventTypeTag(e.Type); t != "" {
		tags = append(tags, t)
	}
	if e.EndInvented {
		tags = append(tags, "no end time set")
	}
	if n := e.GuestCount(""); n > 0 {
		tags = append(tags, fmt.Sprintf("%d guest%s", n, plural(n)))
	}
	if e.AttendeesTruncated {
		tags = append(tags, "guest list truncated by Google")
	}
	if e.Location != "" {
		tags = append(tags, "at "+e.Location)
	}
	return tags
}

// ConferenceLine is how an event says where to join it, or that there
// is nowhere to join it yet.
//
// A pending conference is said out loud rather than left blank: a blank
// reads as "this meeting has no video link", and the difference between
// that and "Google has not finished making it" is somebody sitting in
// an empty room.
func ConferenceLine(c gcal.Conference) string {
	switch c.State() {
	case gcal.NoConference:
		return ""
	case gcal.ConferenceReady:
		return "join: " + c.URI
	case gcal.ConferenceComing:
		return "join: Google is still creating the meeting link — read this event again in a moment"
	case gcal.ConferenceRefused:
		return "join: the meeting link could not be created, and this event has none"
	default:
		return "join: this event has conference data with no video link in it"
	}
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

// Recurrence explains a recurrence in prose, falling back to the rule
// itself.
//
// The parsing lives in internal/recur, which is also what validates a
// rule on a write and what expands one. One parser, so a rule cannot
// read one way in a result and another way in a guard.
func Recurrence(rules []string) string {
	set, err := recur.Parse(rules)
	if err != nil {
		// An unreadable rule is shown as itself. A caller who wrote it
		// recognises their own line, and a wrong explanation is worse
		// than none.
		if len(rules) > 0 {
			return rules[0]
		}
		return "repeats"
	}
	return set.Explain()
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
	// Two different facts, and the first version of these lines said
	// both of them in nearly the same words. Google's meanings: hidden
	// is hidden from the LIST of calendars; selected is whether its
	// events are drawn in the grid. A calendar can be either without
	// being the other.
	if c.Hidden {
		flags = append(flags, "hidden from your calendar list")
	}
	if !c.Selected {
		flags = append(flags, "its events are not drawn in the Calendar UI")
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, "  %s\n", strings.Join(flags, "; "))
	}
	return b.String()
}

// ---------------------------------------------------------- instances

// Instances is one series expanded into its occurrences.
type Instances struct {
	SeriesID   string
	Title      string
	CalendarID string
	// Window is the span the caller asked about, nil when they asked
	// about the whole series.
	Window *when.Window
	Zone   when.Zone
	Events []model.Event

	Truncated     bool
	NextPageToken string
	Requests      int
	ShowCancelled bool
}

// Text renders the occurrences of a series.
func (i Instances) Text() string {
	var b strings.Builder

	title := i.Title
	if title == "" {
		title = "(no title)"
	}
	fmt.Fprintf(&b, "%s\n", title)
	fmt.Fprintf(&b, "series %s on calendar %s\n", i.SeriesID, i.CalendarID)
	if i.Window != nil {
		fmt.Fprintf(&b, "%s\n", *i.Window)
	} else {
		b.WriteString("the whole series\n")
	}
	fmt.Fprintf(&b, "%s\n\n", i.Zone.Explain())

	if len(i.Events) == 0 {
		b.WriteString("No occurrences.\n")
		return b.String()
	}

	for _, e := range i.Events {
		fmt.Fprintf(&b, "  %s\n", InstanceLine(e, i.Zone))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "%d occurrence%s", len(i.Events), plural(len(i.Events)))
	if i.Truncated {
		b.WriteString(" — the read hit its budget")
		if i.NextPageToken != "" {
			b.WriteString("; pass page_token to continue")
		}
	}
	b.WriteString(".\n")
	if !i.ShowCancelled {
		// A cancelled occurrence is how one date is removed from a
		// series, so its absence is a fact about the series rather than
		// a detail. A caller who does not know it is hidden reads this
		// list as "these are the dates" when one of them is gone.
		b.WriteString("Cancelled occurrences are hidden; pass show_cancelled to see which dates were removed.\n")
	}
	if i.Requests > 1 {
		fmt.Fprintf(&b, "(%d API requests)\n", i.Requests)
	}
	return b.String()
}

// InstanceLine is one occurrence, marked with what makes it differ from
// the rest of the series.
//
// Every line carries its date. A schedule groups by day and can leave
// the date off each row; a series is a list of dates, and "14:00-15:00"
// three times over says nothing about which occurrence is which.
func InstanceLine(e model.Event, z when.Zone) string {
	var b strings.Builder
	if !e.Start.AllDay && !e.Start.At.IsZero() {
		d := e.Start.At.Date()
		fmt.Fprintf(&b, "%s %s  ", d, d.Weekday().String()[:3])
	}
	b.WriteString(TimeRange(e, z))
	b.WriteString("  ")
	b.WriteString(Title(e))

	// The id first, because addressing one occurrence is what a caller
	// comes here for. "cancelled" is replaced with a longer line: in a
	// series it does not mean the meeting was called off, it means this
	// date was taken out.
	tags := []string{"id " + e.ID}
	for _, t := range commonTags(e) {
		if t == "cancelled" {
			t = "CANCELLED — this date was removed from the series"
		}
		tags = append(tags, t)
	}
	if moved := movedFrom(e); moved != "" {
		tags = append(tags, "moved from "+moved)
	}
	fmt.Fprintf(&b, "  [%s]", strings.Join(tags, "; "))
	return b.String()
}

// movedFrom says where an occurrence used to be. Whether it moved at all
// is model.Event.Moved; this only formats the answer, and shows the date
// as well as the time when the move crossed a day.
func movedFrom(e model.Event) string {
	if !e.Moved() {
		return ""
	}
	if e.Start.AllDay {
		return e.OriginalStart.Date.String()
	}
	if e.OriginalStart.At.Date() != e.Start.At.Date() {
		return e.OriginalStart.At.T.Format("2006-01-02 15:04")
	}
	return e.OriginalStart.At.T.Format("15:04")
}

// ---------------------------------------------------------- availability

// AvailabilityReport is what check_availability returns.
type AvailabilityReport struct {
	Window  when.Window
	Zone    when.Zone
	Answers []model.Availability
	// Gaps are the intervals nobody is busy, already filtered by MinGap.
	Gaps []when.Window
	// GapsFrom is how many calendars the gaps were computed from. Zero
	// with answers present means nothing could be read, and the service
	// leaves Gaps empty rather than offering a window it knows nothing
	// about (§4.6).
	GapsFrom int
	// MinGap is the shortest gap reported, zero when the caller set none.
	MinGap time.Duration
	// Hours is the working-hours mask the gaps were filtered by, empty
	// when the caller asked for none (§17.2). It is stated in the result
	// because a gap list that silently hid the evenings would read as
	// "nobody is free then", which is a different claim.
	Hours    when.Hours
	Requests int
}

// Unknown is how many calendars could not be read.
func (r AvailabilityReport) Unknown() int {
	n := 0
	for _, a := range r.Answers {
		if a.Unknown {
			n++
		}
	}
	return n
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
				// A block crossing local midnight needs the end's date
				// too. An all-day event comes back as a busy block from
				// midnight to midnight, and printing the end time alone
				// rendered it "2026-03-20 00:00-00:00" — which reads as
				// a block of no length, on the one kind of event that
				// occupies the whole day. The free gaps below already
				// carried this rule; the busy list did not, and only a
				// live transcript showed it.
				if busy.End.Date() != busy.Start.Date() {
					fmt.Fprintf(&b, "  busy %s %s to %s %s\n",
						busy.Start.Date(), busy.Start.T.Format("15:04"),
						busy.End.Date(), busy.End.T.Format("15:04"))
					continue
				}
				fmt.Fprintf(&b, "  busy %s %s-%s\n",
					busy.Start.Date(), busy.Start.T.Format("15:04"), busy.End.T.Format("15:04"))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(r.gaps())
	if r.Requests > 1 {
		fmt.Fprintf(&b, "\n(%d API requests)\n", r.Requests)
	}
	return b.String()
}

func (r AvailabilityReport) gaps() string {
	var b strings.Builder
	unknown := r.Unknown()

	switch {
	case r.GapsFrom == 0 && len(r.Answers) > 0:
		// Nothing was read, so there is no free time to report. Printing
		// the whole window as free here is the defect §4.6 exists to
		// prevent, and it is worth refusing to print rather than
		// qualifying.
		b.WriteString("No free time can be computed: not one of these calendars could be read.\n")
		return b.String()
	case len(r.Gaps) == 0 && r.MinGap > 0:
		fmt.Fprintf(&b, "No free gap of %s or more %s.\n", Duration(r.MinGap), r.scope())
	case len(r.Gaps) == 0:
		fmt.Fprintf(&b, "No free time %s.\n", r.scope())
	default:
		if r.MinGap > 0 {
			fmt.Fprintf(&b, "Free%s, %s or longer:\n", r.within(), Duration(r.MinGap))
		} else {
			fmt.Fprintf(&b, "Free%s:\n", r.within())
		}
		for _, g := range r.Gaps {
			// A gap that crosses midnight needs the end's date too, or
			// "2026-03-20 17:00-09:00" reads as ending before it began.
			// Availability windows are routinely several days long.
			if g.End.Date() != g.Start.Date() {
				fmt.Fprintf(&b, "  %s %s to %s %s (%s)\n",
					g.Start.Date(), g.Start.T.Format("15:04"),
					g.End.Date(), g.End.T.Format("15:04"), Duration(g.Duration()))
				continue
			}
			fmt.Fprintf(&b, "  %s %s-%s (%s)\n",
				g.Start.Date(), g.Start.T.Format("15:04"), g.End.T.Format("15:04"),
				Duration(g.Duration()))
		}
	}

	if r.Hours.Set() {
		// Said after the list as well as in its heading: the gaps are
		// the free time INSIDE the mask, so a caller reading only the
		// rows would take an empty evening for a busy one.
		fmt.Fprintf(&b, "\nOnly working hours are shown: %s, %s. "+
			"Free time outside them is not listed.\n", r.Hours, r.Zone.Name())
	}
	if unknown > 0 {
		// The gaps were computed from the calendars that answered, so
		// they are an upper bound on free time rather than an answer.
		fmt.Fprintf(&b, "\nThese gaps come from %d of %d calendars: %d could not be read, "+
			"so somebody may be busy in them.\n", len(r.Answers)-unknown, len(r.Answers), unknown)
	}
	return b.String()
}

// within names the mask in the heading over the free gaps, and nothing
// when the caller asked for no mask.
//
// scope is the same fact for a sentence that must say what it was empty
// OVER: "No free time in this window" is a different claim from "none
// between 09:00 and 17:00", and a caller acts on the difference. One
// derives from the other, so the mask is worded in one place.
func (r AvailabilityReport) within() string {
	if !r.Hours.Set() {
		return ""
	}
	return " within " + r.Hours.String()
}

func (r AvailabilityReport) scope() string {
	if within := r.within(); within != "" {
		return strings.TrimPrefix(within, " ")
	}
	return "in this window"
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
