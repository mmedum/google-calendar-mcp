package service

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
	"github.com/mmedum/google-calendar-mcp/internal/render"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// Rendered is the contract every tool result satisfies.
//
// It exists to make the standard's "send both halves" structural. A tool
// whose output type has no Render does not compile, so there is no way
// to register a tool that returns only structured content — and a client
// that shows only `content` would otherwise see nothing.
type Rendered interface {
	Render() string
}

// The two halves are never the same bytes: Render is the readable
// presentation and the struct is the machine one.

// CalendarsResult is list_calendars.
type CalendarsResult struct {
	Calendars []CalendarOut `json:"calendars"`
	Count     int           `json:"count"`
}

// CalendarOut is one calendar in a structured reply.
type CalendarOut struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Original  string `json:"original_title,omitempty"`
	TimeZone  string `json:"time_zone"`
	Role      string `json:"access_role"`
	RoleMeans string `json:"access_role_means"`
	Primary   bool   `json:"primary,omitempty"`
	Selected  bool   `json:"selected"`
	Hidden    bool   `json:"hidden,omitempty"`
	CanWrite  bool   `json:"can_write"`
	// ColorID is this user's own colour for the calendar, which
	// manage_calendar sets. The palette is in get_settings.
	//
	// No etag here, unlike an event: a calendar is two resources with
	// two of them, no calendar tool takes one back, and publishing one
	// on every list_calendars row would be offering a value with nothing
	// to do (§7.5).
	ColorID string `json:"color_id,omitempty"`
}

// Render implements Rendered.
func (r CalendarsResult) Render() string {
	cals := make([]model.Calendar, 0, len(r.Calendars))
	for _, c := range r.Calendars {
		cals = append(cals, model.Calendar{
			ID: c.ID, Title: c.Title, Original: c.Original, TimeZone: c.TimeZone,
			Role: c.Role, Primary: c.Primary, Selected: c.Selected, Hidden: c.Hidden,
		})
	}
	return render.CalendarList(cals)
}

// NewCalendarsResult builds the reply.
func NewCalendarsResult(cals []model.Calendar) CalendarsResult {
	out := CalendarsResult{Count: len(cals)}
	for _, c := range cals {
		out.Calendars = append(out.Calendars, newCalendarOut(c))
	}
	return out
}

// newCalendarOut is the one place a calendar becomes a structured row.
func newCalendarOut(c model.Calendar) CalendarOut {
	return CalendarOut{
		ID: c.ID, Title: c.Title, Original: c.Original, TimeZone: c.TimeZone,
		Role: c.Role, RoleMeans: gcal.RoleMeans(c.Role), Primary: c.Primary,
		Selected: c.Selected, Hidden: c.Hidden, CanWrite: c.CanWrite(),
		ColorID: c.ColorID,
	}
}

// newSharingOut is the one place an ACL rule becomes a structured row.
func newSharingOut(r model.Sharing) SharingOut {
	return SharingOut{
		RuleID: r.RuleID, Who: r.Who(), ScopeType: r.ScopeType, Value: r.Value,
		Role: r.Role, RoleMeans: r.RoleMeans(), Public: r.Public(),
	}
}

func sharingOut(rules []model.Sharing) []SharingOut {
	out := make([]SharingOut, 0, len(rules))
	for _, r := range rules {
		out = append(out, newSharingOut(r))
	}
	return out
}

// CalendarResult is get_calendar.
type CalendarResult struct {
	Calendar    CalendarOut `json:"calendar"`
	Description string      `json:"description,omitempty"`
	// Sharing is empty when the ACL scope was not granted, and Note says
	// so rather than letting an empty list read as "shared with nobody"
	// (§2.15).
	Sharing []SharingOut `json:"sharing,omitempty"`
	Note    string       `json:"note,omitempty"`
}

// SharingOut is one ACL rule.
type SharingOut struct {
	// RuleID is Google's own id for the rule. It is reported rather than
	// built: unshare_calendar takes `who` and finds the rule itself, so
	// nothing here has to construct an id from a scope.
	RuleID string `json:"rule_id,omitempty"`
	// Who is the audience in words; Value is the raw address or domain,
	// empty on a public rule.
	Who       string `json:"who"`
	Value     string `json:"value,omitempty"`
	ScopeType string `json:"scope_type"`
	Role      string `json:"role"`
	RoleMeans string `json:"role_means"`
	Public    bool   `json:"public,omitempty"`
}

// Rendered is Render under a name that reads better at a call site
// asserting about the text a person sees.
func (r CalendarResult) Rendered() string { return r.Render() }

// Render implements Rendered.
func (r CalendarResult) Render() string {
	var b strings.Builder
	c := r.Calendar
	b.WriteString(render.CalendarLine(model.Calendar{
		ID: c.ID, Title: c.Title, Original: c.Original, TimeZone: c.TimeZone,
		Role: c.Role, Primary: c.Primary, Selected: c.Selected, Hidden: c.Hidden,
	}))
	if r.Description != "" {
		fmt.Fprintf(&b, "  %s\n", r.Description)
	}
	b.WriteString("\n")
	if r.Note != "" {
		b.WriteString(r.Note + "\n")
		return b.String()
	}
	b.WriteString("who can see it:\n")
	// The same renderer list_sharing and a share result use. Three
	// printings of "who can see this calendar" is how one result comes to
	// contradict another.
	b.WriteString(render.Exposure(sharingModel(r.Sharing)))
	return b.String()
}

// sharingModel turns the structured rows back into the model the
// renderer reads, as CalendarsResult.Render does for calendars.
func sharingModel(rows []SharingOut) []model.Sharing {
	out := make([]model.Sharing, 0, len(rows))
	for _, s := range rows {
		out = append(out, model.Sharing{
			RuleID: s.RuleID, ScopeType: s.ScopeType, Value: s.Value, Role: s.Role,
		})
	}
	return out
}

// ScheduleResult is list_events and search_events.
type ScheduleResult struct {
	Window        WindowOut  `json:"window"`
	TimeZone      string     `json:"time_zone"`
	ZoneSource    string     `json:"time_zone_source"`
	Calendars     []string   `json:"calendars"`
	Expanded      bool       `json:"recurring_expanded"`
	Events        []EventOut `json:"events"`
	Shown         int        `json:"shown"`
	Matched       int        `json:"matched"`
	Truncated     bool       `json:"truncated"`
	NextPageToken string     `json:"next_page_token,omitempty"`
	Requests      int        `json:"api_requests"`

	text string
}

// WindowOut is the absolute window a read used (§4.5).
type WindowOut struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// EventOut is one event in a structured reply.
type EventOut struct {
	ID         string `json:"id"`
	CalendarID string `json:"calendar_id"`
	Title      string `json:"title"`
	// AllDay decides which of Date or Start/End is meaningful. The two
	// are never both set, which is §4.1 carried into the wire format a
	// client sees.
	AllDay    bool   `json:"all_day"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`

	Status          string   `json:"status,omitempty"`
	EventType       string   `json:"event_type,omitempty"`
	Location        string   `json:"location,omitempty"`
	Description     string   `json:"description,omitempty"`
	Recurrence      []string `json:"recurrence,omitempty"`
	RecurrenceMeans string   `json:"recurrence_means,omitempty"`
	SeriesID        string   `json:"series_id,omitempty"`
	Transparent     bool     `json:"free_not_busy,omitempty"`
	GuestCount      int      `json:"guest_count,omitempty"`
	GuestsTruncated bool     `json:"guest_list_truncated,omitempty"`
	Organizer       string   `json:"organizer,omitempty"`
	Link            string   `json:"link,omitempty"`
	// ConferenceURI is the video meeting to join, and
	// ConferenceStatus is set when there is a conference without one:
	// "pending" while Google is still making it, "failure" when it gave
	// up, and "no_video_entry_point" for a conference this server cannot
	// turn into a link — a phone-only one, or a third-party provider. An
	// empty URI with no status means the event has no conference, and
	// that fourth value exists so an event that HAS one is never
	// reported as an event that does not.
	ConferenceURI    string `json:"conference_uri,omitempty"`
	ConferenceStatus string `json:"conference_status,omitempty"`
	ETag             string `json:"etag,omitempty"`
}

// Render implements Rendered.
func (r ScheduleResult) Render() string { return r.text }

// NewScheduleResult builds the reply from a rendered schedule.
func NewScheduleResult(s render.Schedule) ScheduleResult {
	out := ScheduleResult{
		Window:        WindowOut{From: s.Window.Start.String(), To: s.Window.End.String()},
		TimeZone:      s.Zone.Name(),
		ZoneSource:    string(s.Zone.Source),
		Calendars:     s.Calendars,
		Expanded:      s.Expanded,
		Shown:         len(s.Events),
		Matched:       s.Matched,
		Truncated:     s.Truncated,
		NextPageToken: s.NextPageToken,
		Requests:      s.Requests,
		text:          s.Text(),
	}
	for _, e := range s.Events {
		out.Events = append(out.Events, NewEventOut(e))
	}
	return out
}

// NewEventOut converts one event.
func NewEventOut(e model.Event) EventOut {
	o := EventOut{
		ID: e.ID, CalendarID: e.CalendarID, Title: e.Title,
		Status: e.Status, EventType: e.Type, Location: e.Location,
		Description: e.Description, Recurrence: e.Recurrence,
		SeriesID: e.SeriesID, Transparent: e.Transparent,
		GuestCount: e.GuestCount(""), GuestsTruncated: e.AttendeesTruncated,
		Organizer: e.Organizer, Link: e.Link, ETag: e.ETag,
		AllDay:        e.Start.AllDay,
		ConferenceURI: e.Conference.URI,
	}
	// The same four states the text renders, so a client reading only
	// this block reaches the same conclusion. "other" is a state rather
	// than a blank: an event with a phone-only conference has one, and
	// an empty uri with an empty status means it has none.
	switch e.Conference.State() {
	case gcal.NoConference, gcal.ConferenceReady:
	case gcal.ConferenceComing:
		o.ConferenceStatus = gcal.ConferencePending
	case gcal.ConferenceRefused:
		o.ConferenceStatus = gcal.ConferenceFailure
	default:
		o.ConferenceStatus = "no_video_entry_point"
	}
	if e.Start.AllDay {
		o.StartDate = e.Start.Date.String()
		if !e.End.Date.IsZero() {
			o.EndDate = e.End.Date.String()
		}
	} else {
		if !e.Start.At.IsZero() {
			o.Start = e.Start.At.String()
		}
		if !e.End.At.IsZero() {
			o.End = e.End.At.String()
		}
	}
	if len(e.Recurrence) > 0 {
		o.RecurrenceMeans = render.Recurrence(e.Recurrence)
	}
	return o
}

// EventResult is get_event.
type EventResult struct {
	Event      EventOut      `json:"event"`
	TimeZone   string        `json:"time_zone"`
	ZoneSource string        `json:"time_zone_source"`
	Attendees  []AttendeeOut `json:"attendees,omitempty"`

	text string
}

// AttendeeOut is one guest.
type AttendeeOut struct {
	Email     string `json:"email"`
	Name      string `json:"name,omitempty"`
	Response  string `json:"response"`
	Optional  bool   `json:"optional,omitempty"`
	Resource  bool   `json:"resource,omitempty"`
	Self      bool   `json:"self,omitempty"`
	Organizer bool   `json:"organizer,omitempty"`
}

// Render implements Rendered.
func (r EventResult) Render() string { return r.text }

// NewEventResult builds the reply.
func NewEventResult(e model.Event, z when.Zone) EventResult {
	out := EventResult{
		Event: NewEventOut(e), TimeZone: z.Name(), ZoneSource: string(z.Source),
	}
	for _, a := range e.Attendees {
		out.Attendees = append(out.Attendees, AttendeeOut{
			Email: a.Email, Name: a.Name, Response: a.Response,
			Optional: a.Optional, Resource: a.Resource, Self: a.Self, Organizer: a.Organizer,
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", render.EventLine(e, z))
	fmt.Fprintf(&b, "%s\n", z.Explain())
	fmt.Fprintf(&b, "id: %s on calendar %s\n", e.ID, e.CalendarID)
	if line := render.ConferenceLine(e.Conference); line != "" {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if e.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", e.Description)
	}
	if n := plan.CrowdWarning(len(e.Attendees)); n != "" {
		// The read path carries §17.5's warning too, and it is the one
		// that matters most: this is the result that prints each guest's
		// response, and above the limit those responses are not the ones
		// on the event. A warning only on writes would leave the wrong
		// list unqualified.
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if len(e.Attendees) > 0 {
		fmt.Fprintf(&b, "\nGuests (%d):\n", len(e.Attendees))
		for _, a := range e.Attendees {
			who := a.Email
			if a.Name != "" {
				who = a.Name + " <" + a.Email + ">"
			}
			extra := ""
			if a.Optional {
				extra = ", optional"
			}
			if a.Organizer {
				extra += ", organiser"
			}
			fmt.Fprintf(&b, "  %s — %s%s\n", who, a.Response, extra)
		}
		if e.AttendeesTruncated {
			b.WriteString("  (Google truncated this guest list)\n")
		}
	}
	if e.IsInstance() {
		fmt.Fprintf(&b, "\nOne occurrence of series %s.\n", e.SeriesID)
	}
	if e.IsSeries() {
		fmt.Fprintf(&b, "\nA series: %s\n", render.Recurrence(e.Recurrence))
	}
	out.text = b.String()
	return out
}

// SettingsResult is get_settings.
type SettingsResult struct {
	TimeZone     string            `json:"time_zone"`
	WeekStart    string            `json:"week_start,omitempty"`
	Format24Hour string            `json:"format_24_hour,omitempty"`
	Locale       string            `json:"locale,omitempty"`
	EventColors  map[string]string `json:"event_colors,omitempty"`
	// CalendarColors is the other palette Google publishes, and the two
	// are not interchangeable: an event colour id means nothing to
	// manage_calendar's color_id, which indexes this one.
	CalendarColors map[string]string `json:"calendar_colors,omitempty"`

	text string
}

// Render implements Rendered.
func (r SettingsResult) Render() string { return r.text }

// InstancesResult is list_instances.
type InstancesResult struct {
	SeriesID   string `json:"series_id"`
	CalendarID string `json:"calendar_id"`
	Title      string `json:"title,omitempty"`
	// Window is absent when the caller asked about the whole series.
	Window     *WindowOut      `json:"window,omitempty"`
	TimeZone   string          `json:"time_zone"`
	ZoneSource string          `json:"time_zone_source"`
	Instances  []OccurrenceOut `json:"instances"`
	Shown      int             `json:"shown"`
	Truncated  bool            `json:"truncated"`
	// CancelledHidden says a cancelled occurrence would not be in this
	// list. It is the difference between "the series has these dates"
	// and "the series has these dates plus ones somebody removed".
	CancelledHidden bool   `json:"cancelled_hidden"`
	NextPageToken   string `json:"next_page_token,omitempty"`
	Requests        int    `json:"api_requests"`

	text string
}

// OccurrenceOut is one occurrence, with what makes it differ from the
// rest of the series.
type OccurrenceOut struct {
	EventOut
	// OriginalStart is where this occurrence was scheduled before
	// anybody moved it. It identifies the instance even after the move
	// (§6.2), which is why it is reported rather than inferred.
	OriginalStart string `json:"original_start,omitempty"`
	Moved         bool   `json:"moved,omitempty"`
	Cancelled     bool   `json:"cancelled,omitempty"`
}

// Render implements Rendered.
func (r InstancesResult) Render() string { return r.text }

// NewInstancesResult builds the reply.
func NewInstancesResult(in render.Instances) InstancesResult {
	out := InstancesResult{
		SeriesID: in.SeriesID, CalendarID: in.CalendarID, Title: in.Title,
		TimeZone: in.Zone.Name(), ZoneSource: string(in.Zone.Source),
		Shown: len(in.Events), Truncated: in.Truncated,
		CancelledHidden: !in.ShowCancelled,
		NextPageToken:   in.NextPageToken, Requests: in.Requests,
		text: in.Text(),
	}
	if in.Window != nil {
		out.Window = &WindowOut{From: in.Window.Start.String(), To: in.Window.End.String()}
	}
	for _, e := range in.Events {
		occ := OccurrenceOut{EventOut: NewEventOut(e), Cancelled: e.Cancelled(), Moved: e.Moved()}
		switch {
		case e.OriginalStart.AllDay:
			occ.OriginalStart = e.OriginalStart.Date.String()
		case !e.OriginalStart.At.IsZero():
			occ.OriginalStart = e.OriginalStart.At.String()
		}
		out.Instances = append(out.Instances, occ)
	}
	return out
}

// AvailabilityResult is check_availability.
type AvailabilityResult struct {
	Window     WindowOut `json:"window"`
	TimeZone   string    `json:"time_zone"`
	ZoneSource string    `json:"time_zone_source"`
	// Calendars carries one answer each, in the order asked. An answer
	// is busy intervals or unknown — never an empty list standing in for
	// a calendar that could not be read (§4.6).
	Calendars []AvailabilityOut `json:"calendars"`
	Free      []IntervalOut     `json:"free"`
	// FreeFrom is how many calendars the free gaps were computed from.
	// Zero means none could be read, and Free is empty rather than the
	// whole window (§4.6).
	FreeFrom int `json:"free_computed_from"`
	// MinMinutes echoes the filter, so a caller can tell an empty Free
	// from one their own filter emptied.
	MinMinutes int `json:"min_minutes,omitempty"`
	// WorkingHours echoes the mask for the same reason, and matters
	// more: Free outside it was removed, so a caller reading Free as
	// "every free minute" would be wrong about the evenings (§17.2).
	WorkingHours string `json:"working_hours,omitempty"`
	// Unknown is how many calendars could not be read. A caller that
	// reads Free without reading this is booking blind.
	Unknown  int `json:"unknown_calendars"`
	Requests int `json:"api_requests"`

	text string
}

// AvailabilityOut is one calendar's answer.
type AvailabilityOut struct {
	CalendarID string        `json:"calendar_id"`
	Busy       []IntervalOut `json:"busy"`
	Unknown    bool          `json:"unknown,omitempty"`
	Reason     string        `json:"unknown_reason,omitempty"`
}

// IntervalOut is a span, absolute at both ends and with its length said
// once so a caller does not compute it.
type IntervalOut struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Minutes int    `json:"minutes"`
}

// Render implements Rendered.
func (r AvailabilityResult) Render() string { return r.text }

// NewAvailabilityResult builds the reply.
func NewAvailabilityResult(rep render.AvailabilityReport) AvailabilityResult {
	out := AvailabilityResult{
		Window:   WindowOut{From: rep.Window.Start.String(), To: rep.Window.End.String()},
		TimeZone: rep.Zone.Name(), ZoneSource: string(rep.Zone.Source),
		MinMinutes:   int(rep.MinGap.Minutes()),
		WorkingHours: rep.Hours.String(),
		FreeFrom:     rep.GapsFrom,
		Unknown:      rep.Unknown(), Requests: rep.Requests,
		text: rep.Text(),
	}
	for _, a := range rep.Answers {
		ans := AvailabilityOut{CalendarID: a.CalendarID, Unknown: a.Unknown, Reason: a.Reason}
		for _, b := range a.Busy {
			ans.Busy = append(ans.Busy, interval(b.Start, b.End))
		}
		// An unknown calendar reports no busy list at all. An empty one
		// would read as "free", which is the confusion §4.6 is about.
		if ans.Busy == nil && !a.Unknown {
			ans.Busy = []IntervalOut{}
		}
		out.Calendars = append(out.Calendars, ans)
	}
	for _, g := range rep.Gaps {
		out.Free = append(out.Free, interval(g.Start, g.End))
	}
	return out
}

func interval(start, end when.Zoned) IntervalOut {
	return IntervalOut{
		Start: start.String(), End: end.String(),
		Minutes: int(end.T.Sub(start.T).Minutes()),
	}
}

// WriteResult is what every write tool returns (§4.9).
//
// What the resource looked like before, what changed, what it looks like
// now, what the server asked Google to send, and the new etag. A write
// result that says only "ok" is a defect: a caller cannot tell a patch
// that changed one field from one that changed five, and cannot tell
// whether anybody was emailed.
type WriteResult struct {
	// Action is the verb in the past tense, or the conditional one under
	// DryRun, so the two halves of the result cannot disagree about
	// whether anything happened. Both come from one table in
	// internal/render, keyed on what the write does.
	Action   string `json:"action"`
	DryRun   bool   `json:"dry_run,omitempty"`
	Calendar string `json:"calendar"`
	// Scope is the recurrence decision, absent when the event does not
	// repeat (§4.2).
	Scope string `json:"scope,omitempty"`
	// Before is absent on a create; Event is absent on a delete.
	Before  *EventOut     `json:"before,omitempty"`
	Event   *EventOut     `json:"event,omitempty"`
	Changes []plan.Change `json:"changes,omitempty"`
	// Notification says what was ASKED FOR. Never what arrived: §2.6
	// says `none` is not silence, and spike A watched one invitation
	// reach one of two guests with nothing different in the request.
	Notification string   `json:"notification"`
	TimeZone     string   `json:"time_zone"`
	ZoneSource   string   `json:"time_zone_source"`
	Notes        []string `json:"notes,omitempty"`
	ETag         string   `json:"etag,omitempty"`
	Requests     int      `json:"api_requests"`

	text string
}

// Render implements Rendered.
func (r WriteResult) Render() string { return r.text }

// NewWriteResult builds the reply.
func NewWriteResult(w render.WriteReport) WriteResult {
	out := WriteResult{
		Action: w.Said(), DryRun: w.DryRun, Calendar: w.Calendar, Scope: w.Scope,
		Changes: w.Changes, Notification: w.Notify,
		TimeZone: w.Zone.Name(), ZoneSource: string(w.Zone.Source),
		Notes: w.Notes, Requests: w.Requests,
		text: w.Text(),
	}
	if n := w.Crowded(); n != "" {
		// Both halves carry it, from the one RULE: a client that shows
		// only the structured block would otherwise drop the sentence
		// saying the RSVPs it lists are incomplete (§17.5).
		out.Notes = append(append([]string{}, out.Notes...), n)
	}
	if w.Before != nil {
		before := NewEventOut(*w.Before)
		out.Before = &before
	}
	if w.After != nil {
		after := NewEventOut(*w.After)
		out.Event, out.ETag = &after, after.ETag
	}
	return out
}

// CalendarWriteResult is what create_calendar, manage_calendar and the
// two gated tools return (§4.9).
//
// Same shape as a write to an event: what it looked like before, what
// changed, what it looks like now, and the notes that say what the write
// did NOT do — because the mistakes §7.5 is about are all of that kind.
// Unsubscribing deletes nothing; renaming a shared calendar renames it
// for the team; a colour is yours alone.
type CalendarWriteResult struct {
	// Action is the verb in the past tense, or the conditional under
	// DryRun, from the same table every other write uses.
	Action   string        `json:"action"`
	DryRun   bool          `json:"dry_run,omitempty"`
	Calendar CalendarOut   `json:"calendar"`
	Before   *CalendarOut  `json:"before,omitempty"`
	Changes  []plan.Change `json:"changes,omitempty"`
	Notes    []string      `json:"notes,omitempty"`
	Requests int           `json:"api_requests"`

	text string
}

// Render implements Rendered.
func (r CalendarWriteResult) Render() string { return r.text }

// NewCalendarWriteResult builds the reply.
func NewCalendarWriteResult(c render.CalendarReport) CalendarWriteResult {
	out := CalendarWriteResult{
		Action: c.Said(), DryRun: c.DryRun, Calendar: newCalendarOut(c.Calendar),
		Changes: c.Changes, Notes: c.Notes, Requests: c.Requests,
		text: c.Text(),
	}
	if c.Before != nil {
		before := newCalendarOut(*c.Before)
		out.Before = &before
	}
	return out
}

// SharingResult is what list_sharing, share_calendar and
// unshare_calendar return.
//
// Sharing is the whole exposure, not the rule that changed, and Before
// carries what it was: §7.6's rule is that a share result answers "who
// can see this now", which a single changed rule does not.
type SharingResult struct {
	// Action is empty on list_sharing, which changes nothing.
	Action     string       `json:"action,omitempty"`
	DryRun     bool         `json:"dry_run,omitempty"`
	CalendarID string       `json:"calendar_id"`
	Sharing    []SharingOut `json:"sharing"`
	Before     []SharingOut `json:"sharing_before,omitempty"`
	Changed    *SharingOut  `json:"changed,omitempty"`
	// Public is the one fact worth a field of its own: a calendar
	// anybody can read is a different kind of thing from a shared one.
	Public bool `json:"public"`
	// Notification says what was ASKED FOR, never what arrived — and on
	// an unshare it says that Google offers no notification at all.
	Notification string   `json:"notification,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	Requests     int      `json:"api_requests"`

	text string
}

// Render implements Rendered.
func (r SharingResult) Render() string { return r.text }

// NewSharingResult builds the reply.
func NewSharingResult(s render.SharingReport) SharingResult {
	out := SharingResult{
		DryRun: s.DryRun, CalendarID: s.CalendarID,
		Sharing: sharingOut(s.After), Before: sharingOut(s.Before),
		Notification: s.Notify, Notes: s.Notes, Requests: s.Requests,
		text: s.Text(),
	}
	if s.Verb != "" {
		out.Action = s.Said()
	}
	if s.Changed != nil {
		changed := newSharingOut(*s.Changed)
		out.Changed = &changed
	}
	_, out.Public = model.PublicRule(s.After)
	return out
}
