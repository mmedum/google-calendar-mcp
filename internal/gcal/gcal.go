// Package gcal holds the wire types for the Google Calendar API v3.
//
// Hand-written, for the fields this server actually uses. The generated
// client (google.golang.org/api/calendar/v3) drags in gRPC and telemetry
// for a subset we could type out in an afternoon, so §4.8 forbids it.
//
// Every field here is a decision, and §8b's api-fields gate holds the
// list: a published field of Event, Calendar, CalendarListEntry or
// AclRule is either modelled below or written off by name in
// testdata/api-fields.tsv with a reason. Without that gate, "we support
// events" quietly means "we support the twelve fields somebody happened
// to need".
//
// Checked against the discovery document, revision 20260826.
package gcal

import "encoding/json"

// ---------------------------------------------------------- EventDateTime

// EventDateTime is the start or end of an event.
//
// Date and DateTime are mutually exclusive: Date means an all-day event
// and carries no instant at all. This is the API shape that §4.1 is
// built to preserve, so the two stay separate strings here rather than
// being merged into one time.Time on the way in. internal/when turns
// them into the types that cannot be confused.
type EventDateTime struct {
	// Date is "yyyy-mm-dd" for an all-day event.
	Date string `json:"date,omitempty"`
	// DateTime is RFC3339 with an offset.
	DateTime string `json:"dateTime,omitempty"`
	// TimeZone is an IANA name. Required on a recurring event's start
	// and end (§2.2); this server sends it on every write so the same
	// path is exercised every time rather than only on the rare one.
	TimeZone string `json:"timeZone,omitempty"`
}

// IsAllDay reports whether this is the date half of the union.
func (e EventDateTime) IsAllDay() bool { return e.Date != "" && e.DateTime == "" }

// ---------------------------------------------------------------- Event

// Event is a calendar event. 44 properties are published; the ones here
// are modelled and the rest are written off in testdata/api-fields.tsv.
type Event struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	HTMLLink string `json:"htmlLink,omitempty"`
	Created  string `json:"created,omitempty"`
	Updated  string `json:"updated,omitempty"`
	Summary  string `json:"summary,omitempty"`
	// Description and Location are content. They reach a renderer and
	// never a log (§9).
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	ColorID     string `json:"colorId,omitempty"`

	Creator   *EventPerson `json:"creator,omitempty"`
	Organizer *EventPerson `json:"organizer,omitempty"`

	Start *EventDateTime `json:"start,omitempty"`
	End   *EventDateTime `json:"end,omitempty"`
	// EndTimeUnspecified means Google invented the end; the renderer
	// says so rather than showing a duration nobody set.
	EndTimeUnspecified bool `json:"endTimeUnspecified,omitempty"`

	// Recurrence holds RRULE/EXRULE/RDATE/EXDATE lines, RFC 5545. Sent
	// as the caller's strings (§6.4).
	Recurrence []string `json:"recurrence,omitempty"`
	// RecurringEventID is set on an instance and names its series.
	RecurringEventID string `json:"recurringEventId,omitempty"`
	// OriginalStartTime is the instance's scheduled start. It identifies
	// the instance even after it has been moved (§6.2).
	OriginalStartTime *EventDateTime `json:"originalStartTime,omitempty"`

	Transparency string `json:"transparency,omitempty"`
	Visibility   string `json:"visibility,omitempty"`
	ICalUID      string `json:"iCalUID,omitempty"`
	Sequence     int    `json:"sequence,omitempty"`

	Attendees []EventAttendee `json:"attendees,omitempty"`
	// AttendeesOmitted means the list is truncated. A renderer that
	// ignores this shows a meeting as smaller than it is.
	AttendeesOmitted bool `json:"attendeesOmitted,omitempty"`

	GuestsCanInviteOthers   *bool `json:"guestsCanInviteOthers,omitempty"`
	GuestsCanModify         bool  `json:"guestsCanModify,omitempty"`
	GuestsCanSeeOtherGuests *bool `json:"guestsCanSeeOtherGuests,omitempty"`

	Reminders *EventReminders `json:"reminders,omitempty"`

	// EventType is one of default, birthday, focusTime, fromGmail,
	// outOfOffice, workingLocation. It cannot be changed after creation
	// and fromGmail cannot be created at all (§2.12).
	EventType string `json:"eventType,omitempty"`

	// ConferenceData is read in phase 0 and written in a later phase
	// (§17.3). Kept as raw JSON until then so a read round-trips it
	// without this package pretending to model a union it does not.
	ConferenceData json.RawMessage `json:"conferenceData,omitempty"`

	// ETag backs If-Match on every write (§4.4).
	ETag string `json:"etag,omitempty"`
}

// EventPerson is the creator or organizer of an event.
type EventPerson struct {
	ID          string `json:"id,omitempty"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Self        bool   `json:"self,omitempty"`
}

// EventAttendee is one guest.
//
// Email is personal data about somebody who never agreed to this
// server existing, so it is redacted in logs and in the live driver's
// transcript without exception (§9).
type EventAttendee struct {
	ID               string `json:"id,omitempty"`
	Email            string `json:"email,omitempty"`
	DisplayName      string `json:"displayName,omitempty"`
	Organizer        bool   `json:"organizer,omitempty"`
	Self             bool   `json:"self,omitempty"`
	Resource         bool   `json:"resource,omitempty"`
	Optional         bool   `json:"optional,omitempty"`
	ResponseStatus   string `json:"responseStatus,omitempty"`
	Comment          string `json:"comment,omitempty"`
	AdditionalGuests int    `json:"additionalGuests,omitempty"`
}

// Attendee response statuses.
const (
	ResponseNeedsAction = "needsAction"
	ResponseDeclined    = "declined"
	ResponseTentative   = "tentative"
	ResponseAccepted    = "accepted"
)

// Event statuses (§2.13).
const (
	StatusConfirmed = "confirmed"
	StatusTentative = "tentative"
	StatusCancelled = "cancelled"
)

// Event types (§2.12).
const (
	EventTypeDefault         = "default"
	EventTypeBirthday        = "birthday"
	EventTypeFocusTime       = "focusTime"
	EventTypeFromGmail       = "fromGmail"
	EventTypeOutOfOffice     = "outOfOffice"
	EventTypeWorkingLocation = "workingLocation"
)

// Transparency values. "transparent" means the event does not make the
// person busy, which is why availability cannot be computed from an
// event list (§4.6).
const (
	TransparencyOpaque      = "opaque"
	TransparencyTransparent = "transparent"
)

// EventReminders is the per-event override of the calendar's defaults.
type EventReminders struct {
	UseDefault bool            `json:"useDefault"`
	Overrides  []EventReminder `json:"overrides,omitempty"`
}

// EventReminder is one reminder.
type EventReminder struct {
	Method  string `json:"method,omitempty"`
	Minutes int    `json:"minutes"`
}

// EventList is the events.list response.
type EventList struct {
	Kind             string          `json:"kind,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	TimeZone         string          `json:"timeZone,omitempty"`
	AccessRole       string          `json:"accessRole,omitempty"`
	DefaultReminders []EventReminder `json:"defaultReminders,omitempty"`
	NextPageToken    string          `json:"nextPageToken,omitempty"`
	NextSyncToken    string          `json:"nextSyncToken,omitempty"`
	Items            []Event         `json:"items,omitempty"`
}

// ------------------------------------------------------------- Calendar

// Calendar is the calendar resource: the calendar itself, as opposed to
// one user's subscription to it.
type Calendar struct {
	ID          string `json:"id,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	// TimeZone is the calendar's own IANA zone and the second source in
	// §4.1's resolution order.
	TimeZone string `json:"timeZone,omitempty"`
	ETag     string `json:"etag,omitempty"`
}

// CalendarListEntry is one user's subscription to a calendar, carrying
// the per-user overrides the calendar resource does not have.
type CalendarListEntry struct {
	ID          string `json:"id,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	// SummaryOverride is the name this user gave the calendar. The
	// renderer shows it and says the original, because a title that
	// matches for one user may not for another (§6.1).
	SummaryOverride string `json:"summaryOverride,omitempty"`
	ColorID         string `json:"colorId,omitempty"`
	BackgroundColor string `json:"backgroundColor,omitempty"`
	ForegroundColor string `json:"foregroundColor,omitempty"`
	Hidden          bool   `json:"hidden,omitempty"`
	Selected        bool   `json:"selected,omitempty"`
	// AccessRole is what this user may do here; it decides whether a
	// write will be refused before it is attempted (§7.1).
	AccessRole       string          `json:"accessRole,omitempty"`
	DefaultReminders []EventReminder `json:"defaultReminders,omitempty"`
	Primary          bool            `json:"primary,omitempty"`
	Deleted          bool            `json:"deleted,omitempty"`
	ETag             string          `json:"etag,omitempty"`
}

// CalendarList is the calendarList.list response.
type CalendarList struct {
	Kind          string              `json:"kind,omitempty"`
	NextPageToken string              `json:"nextPageToken,omitempty"`
	NextSyncToken string              `json:"nextSyncToken,omitempty"`
	Items         []CalendarListEntry `json:"items,omitempty"`
}

// Access roles, weakest first. Order matters: AtLeast compares by it.
const (
	RoleNone                     = "none"
	RoleFreeBusyReader           = "freeBusyReader"
	RoleReader                   = "reader"
	RoleWriterWithoutPrivateData = "writerWithoutPrivateAccess"
	RoleWriter                   = "writer"
	RoleOwner                    = "owner"
)

var roleRank = map[string]int{
	RoleNone: 0, RoleFreeBusyReader: 1, RoleReader: 2,
	RoleWriterWithoutPrivateData: 3, RoleWriter: 4, RoleOwner: 5,
}

// AtLeast reports whether role is at least as strong as want. An
// unknown role ranks lowest, so a role Google adds later is refused
// rather than silently treated as sufficient.
func AtLeast(role, want string) bool {
	return roleRank[role] >= roleRank[want] && roleRank[want] > 0
}

// RoleMeans explains a role in the terms a person uses, because
// "writerWithoutPrivateAccess" is not self-explanatory (§7.6).
func RoleMeans(role string) string {
	switch role {
	case RoleOwner:
		return "can see and change everything, including who else has access"
	case RoleWriter:
		return "can see and change events, including private ones"
	case RoleWriterWithoutPrivateData:
		return "can change events, but private events show without their details"
	case RoleReader:
		return "can see events; private events show without their details"
	case RoleFreeBusyReader:
		return "can see only whether the time is busy, never what the event is"
	case RoleNone:
		return "no access"
	default:
		return "an access level this server does not recognise"
	}
}

// -------------------------------------------------------------- AclRule

// AclRule is one sharing rule on a calendar.
type AclRule struct {
	ID    string   `json:"id,omitempty"`
	Role  string   `json:"role,omitempty"`
	Scope AclScope `json:"scope"`
	ETag  string   `json:"etag,omitempty"`
}

// AclScope is who a rule applies to.
type AclScope struct {
	// Type is user, group, domain or default. "default" means the whole
	// public internet and is the one value that cannot be undone from
	// the other side, so §7.6 puts it behind an explicit flag.
	Type string `json:"type,omitempty"`
	// Value is an address or a domain; empty for type "default".
	Value string `json:"value,omitempty"`
}

// ACL scope types.
const (
	ScopeTypeDefault = "default"
	ScopeTypeUser    = "user"
	ScopeTypeGroup   = "group"
	ScopeTypeDomain  = "domain"
)

// IsPublic reports whether this rule exposes the calendar to anyone.
func (s AclScope) IsPublic() bool { return s.Type == ScopeTypeDefault }

// Acl is the acl.list response.
type Acl struct {
	Kind          string    `json:"kind,omitempty"`
	NextPageToken string    `json:"nextPageToken,omitempty"`
	NextSyncToken string    `json:"nextSyncToken,omitempty"`
	Items         []AclRule `json:"items,omitempty"`
}

// ------------------------------------------------------------- FreeBusy

// FreeBusyRequest is the freebusy.query body.
type FreeBusyRequest struct {
	TimeMin string `json:"timeMin"`
	TimeMax string `json:"timeMax"`
	// TimeZone defaults to UTC at the API. This server always sends one
	// so a result never renders in a zone nobody chose.
	TimeZone string `json:"timeZone,omitempty"`
	// CalendarExpansionMax is capped at 50 by the API (§2.10).
	CalendarExpansionMax int                   `json:"calendarExpansionMax,omitempty"`
	GroupExpansionMax    int                   `json:"groupExpansionMax,omitempty"`
	Items                []FreeBusyRequestItem `json:"items"`
}

// FreeBusyRequestItem names one calendar to query.
type FreeBusyRequestItem struct {
	ID string `json:"id"`
}

// FreeBusyResponse is the freebusy.query response.
type FreeBusyResponse struct {
	Kind      string                      `json:"kind,omitempty"`
	TimeMin   string                      `json:"timeMin,omitempty"`
	TimeMax   string                      `json:"timeMax,omitempty"`
	Calendars map[string]FreeBusyCalendar `json:"calendars,omitempty"`
	Groups    map[string]FreeBusyGroup    `json:"groups,omitempty"`
}

// FreeBusyCalendar is one calendar's answer. Errors is why §4.6 reports
// "unknown" rather than folding a failure into "free".
type FreeBusyCalendar struct {
	Busy   []TimePeriod    `json:"busy,omitempty"`
	Errors []FreeBusyError `json:"errors,omitempty"`
}

// FreeBusyGroup is one group's expansion.
type FreeBusyGroup struct {
	Calendars []string        `json:"calendars,omitempty"`
	Errors    []FreeBusyError `json:"errors,omitempty"`
}

// FreeBusyError is a per-calendar failure inside a successful response.
type FreeBusyError struct {
	Domain string `json:"domain,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// TimePeriod is a busy interval, RFC3339 at both ends.
type TimePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// ------------------------------------------------------ Settings, colors

// Setting is one entry from settings.list.
type Setting struct {
	ID    string `json:"id,omitempty"`
	Value string `json:"value,omitempty"`
	ETag  string `json:"etag,omitempty"`
}

// Settings is the settings.list response.
type Settings struct {
	Kind          string    `json:"kind,omitempty"`
	NextPageToken string    `json:"nextPageToken,omitempty"`
	NextSyncToken string    `json:"nextSyncToken,omitempty"`
	Items         []Setting `json:"items,omitempty"`
}

// Setting ids this server reads by name.
const (
	// SettingTimezone is the user's own zone: the last resort in §4.1's
	// resolution order.
	SettingTimezone = "timezone"
	// SettingWeekStart is 0 for Sunday, 1 for Monday, 6 for Saturday.
	SettingWeekStart = "weekStart"
	// SettingFormat24Hour is "true" or "false".
	SettingFormat24Hour = "format24HourTime"
	// SettingLocale is the user's locale.
	SettingLocale = "locale"
)

// Lookup returns a setting's value by id.
func (s Settings) Lookup(id string) (string, bool) {
	for _, it := range s.Items {
		if it.ID == id {
			return it.Value, true
		}
	}
	return "", false
}

// Colors is the colors.get response. The API returns colour ids; a
// result that says "colorId 5" tells a person nothing, so the renderer
// resolves them to names.
type Colors struct {
	Kind     string               `json:"kind,omitempty"`
	Updated  string               `json:"updated,omitempty"`
	Calendar map[string]ColorPair `json:"calendar,omitempty"`
	Event    map[string]ColorPair `json:"event,omitempty"`
}

// ColorPair is one colour's background and foreground.
type ColorPair struct {
	Background string `json:"background,omitempty"`
	Foreground string `json:"foreground,omitempty"`
}
