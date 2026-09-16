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

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

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

	// ConferenceData is read here and written by create_event (§17.3).
	// It stays raw JSON: the union has a third-party arm this package
	// does not model, and a read that round-trips the value whole cannot
	// lose what it did not understand. ReadConference takes out the
	// three fields a result needs.
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
	// ConferenceProperties says which conference types this calendar
	// accepts, which is what create_event checks before asking for a
	// Meet link (§17.3).
	ConferenceProperties *ConferenceProperties `json:"conferenceProperties,omitempty"`
	ETag                 string                `json:"etag,omitempty"`
}

// ConferenceProperties is a calendar's conference capability.
type ConferenceProperties struct {
	AllowedConferenceSolutionTypes []string `json:"allowedConferenceSolutionTypes,omitempty"`
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
	// NotificationSettings is what THIS user is emailed about on this
	// calendar, and is a per-user override like the colour: changing it
	// changes nothing for anybody else.
	NotificationSettings *NotificationSettings `json:"notificationSettings,omitempty"`
	// ConferenceProperties is carried here too, so the calendar list
	// this server already holds answers "can this calendar have a Meet
	// link" without a second request.
	ConferenceProperties *ConferenceProperties `json:"conferenceProperties,omitempty"`
	ETag                 string                `json:"etag,omitempty"`
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

// AclPatch is the body of an acl.patch call: a role change on a rule
// that already exists.
//
// Its own type rather than an AclRule with one field set, because
// AclRule's Scope has no `omitempty` — it is required on insert — and a
// patch carrying an empty scope object asks Google to read a field the
// caller did not mean to send. acl.update, which replaces the rule whole
// and could therefore move it to another person, is never called (§4.4).
type AclPatch struct {
	Role *string `json:"role,omitempty"`
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

// ------------------------------------------------------------ event ids

// An event id is base32hex: the digits and the lowercase letters a
// through v, 5 to 1024 characters (§2.11). w, x, y and z are refused,
// which is not obvious and cost a live run — Google answers "Invalid
// resource id value", naming neither the field nor the constraint
// (§18 row 21).
//
// The rule lives here rather than in the one caller that first needed
// it. Three places depend on it: the live driver generates ids, the
// server refuses an occurrence id by splitting on a character the
// grammar excludes, and §2.11 lets a client supply an id on insert.
const (
	// EventIDMinLen and EventIDMaxLen bound a client-supplied id.
	EventIDMinLen = 5
	EventIDMaxLen = 1024
)

// ValidEventID reports why an id is not a legal event id, or nil.
func ValidEventID(id string) error {
	if len(id) < EventIDMinLen || len(id) > EventIDMaxLen {
		return fmt.Errorf("event id %q is %d characters; Google requires %d to %d",
			id, len(id), EventIDMinLen, EventIDMaxLen)
	}
	for i, r := range id {
		if !isBase32HexDigit(r) {
			return fmt.Errorf("event id %q has %q at position %d; base32hex allows only a-v and 0-9 "+
				"(not w, x, y or z)", id, r, i)
		}
	}
	return nil
}

func isBase32HexDigit(r rune) bool {
	return (r >= 'a' && r <= 'v') || (r >= '0' && r <= '9')
}

// occurrenceSuffix is the start Google appends to a series id to name
// one occurrence: `20260324T130000Z`, or a bare date on an all-day
// series. Matched case-insensitively, because a model that lowercased
// the whole id is making the same mistake.
var occurrenceSuffix = regexp.MustCompile(`^[0-9]{8}([Tt][0-9]{6}[Zz])?$`)

// SplitOccurrenceID reports whether an id names one occurrence of a
// series, and if so which series and which start.
//
// The split on "_" is safe because ValidEventID's grammar has no
// underscore in it, so an id carrying one is either an occurrence id or
// not an event id at all (§18 row 35).
//
// This is a grammar, not a policy. get_event must keep ACCEPTING an
// occurrence id — that is how a single occurrence is addressed — so the
// decision to refuse one belongs to the caller that has a reason to.
func SplitOccurrenceID(id string) (series, start string, ok bool) {
	i := strings.LastIndex(id, "_")
	if i <= 0 || !occurrenceSuffix.MatchString(id[i+1:]) {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// NewEventID returns a random, legal event id (§2.11).
//
// A client-supplied id is what makes an insert nearly idempotent: a
// retry after a failure nobody saw the answer to either lands or
// collides with a 409, where an id Google chose would quietly create a
// second event. Twenty-six base32hex characters is 130 bits, which is
// more than enough for an id that only has to be unique on one calendar.
//
// The masking is exact rather than a modulo: 256 is eight times 32, so
// the low five bits of a uniform byte are uniform over the alphabet.
func NewEventID() (string, error) {
	const alphabet = "0123456789abcdefghijklmnopqrstuv"
	const n = 26
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate an event id: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[b&31]
	}
	return string(out), nil
}

// ------------------------------------------------------------ the patch

// EventPatch is the body of an events.patch call (§4.4).
//
// Every field is a pointer, and that is the whole point of the type: a
// nil field is absent from the JSON and Google leaves it alone, while a
// non-nil field pointing at an empty value is sent as empty and clears
// it. Event itself cannot express the difference — `omitempty` drops an
// empty string — so patching with Event would make "remove the
// location" unsayable while looking like it worked.
type EventPatch struct {
	Summary     *string        `json:"summary,omitempty"`
	Description *string        `json:"description,omitempty"`
	Location    *string        `json:"location,omitempty"`
	Start       *EventDateTime `json:"start,omitempty"`
	End         *EventDateTime `json:"end,omitempty"`
	// Recurrence points at the whole list because RFC 5545 lines are
	// replaced together: an empty non-nil slice ends the repetition.
	Recurrence *[]string `json:"recurrence,omitempty"`
	// Attendees is read-modify-write, never a blind replacement (§4.4).
	// The service builds it from the guest list it just read, under the
	// etag from that read.
	Attendees    *[]EventAttendee `json:"attendees,omitempty"`
	Status       *string          `json:"status,omitempty"`
	Transparency *string          `json:"transparency,omitempty"`
	ColorID      *string          `json:"colorId,omitempty"`
}

// ApplyTo folds a patch into an event: what the resource looks like once
// Google has applied it.
//
// It lives here, beside the type, because two callers need exactly this
// and had a copy each — the service, building the body a
// this_and_following insert sends, and the in-memory Calendar the tests
// run against. Two copies of a field list is one field away from a fake
// that silently does not apply what the server sent, which would make a
// test green over behaviour that never happened.
func (p EventPatch) ApplyTo(e *Event) {
	if p.Summary != nil {
		e.Summary = *p.Summary
	}
	if p.Description != nil {
		e.Description = *p.Description
	}
	if p.Location != nil {
		e.Location = *p.Location
	}
	if p.Start != nil {
		e.Start = p.Start
	}
	if p.End != nil {
		e.End = p.End
	}
	if p.Recurrence != nil {
		e.Recurrence = *p.Recurrence
	}
	if p.Attendees != nil {
		e.Attendees = *p.Attendees
	}
	if p.Status != nil {
		e.Status = *p.Status
	}
	if p.Transparency != nil {
		e.Transparency = *p.Transparency
	}
	if p.ColorID != nil {
		e.ColorID = *p.ColorID
	}
}

// sendUpdates values, the wire spelling of §4.3's notify.
//
// The server never sends one it was not given. Google's own default
// differs between events and ACL rules (§2.5), so "leave it out" is not
// a decision this server is willing to make on a caller's behalf.
const (
	SendUpdatesAll          = "all"
	SendUpdatesExternalOnly = "externalOnly"
	SendUpdatesNone         = "none"
)

// OccurrenceID is the id Google gives one occurrence of a series: the
// series id, an underscore, and the occurrence's SCHEDULED start in UTC
// (§6.2).
//
// The inverse of SplitOccurrenceID, and a test asserts it round-trips
// through it, because the two would otherwise be two opinions about the
// same grammar. It is what lets a caller address an instance as "this
// series, that start" — the stable address, since originalStartTime
// identifies the instance even after somebody moves it.
//
// The shape is not inferred: phase 1's live run read occurrence ids of
// exactly this form back from Google, which is how it found that an
// occurrence id reaching events.instances answers 200 (§18 row 35).
//
// The series id is NOT held to ValidEventID. That grammar is §2.11's
// rule for an id a CLIENT supplies on insert; whether every id Google
// itself issues obeys it is unverified, and refusing an id Google gave
// out would be this server inventing a constraint. An id that is wrong
// comes back as a 404 naming what to check.
func OccurrenceID(series string, start EventDateTime) (string, error) {
	if strings.TrimSpace(series) == "" {
		return "", fmt.Errorf("an occurrence needs the id of the series it belongs to")
	}
	switch {
	case start.IsAllDay():
		d, err := time.Parse("2006-01-02", start.Date)
		if err != nil {
			return "", fmt.Errorf("%q is not a yyyy-mm-dd date", start.Date)
		}
		return series + "_" + d.Format("20060102"), nil
	case start.DateTime != "":
		t, err := time.Parse(time.RFC3339, start.DateTime)
		if err != nil {
			return "", fmt.Errorf("%q is not an RFC3339 timestamp", start.DateTime)
		}
		return series + "_" + t.UTC().Format("20060102T150405Z"), nil
	default:
		return "", fmt.Errorf("an occurrence needs a start: pass the scheduled date or timestamp")
	}
}

// -------------------------------------------- calendar and ACL patches

// CalendarPatch is the body of a calendars.patch call (§4.4).
//
// Pointers for the same reason EventPatch uses them: a nil field is
// absent from the JSON and Google leaves it alone, while a non-nil field
// pointing at an empty string clears it. calendars.update is a PUT and
// is never called.
//
// This is the calendar ITSELF — what everybody subscribed to it sees.
// The per-user overrides are CalendarListPatch below, and confusing the
// two is how one person's colour change renames a shared calendar for
// the whole team.
type CalendarPatch struct {
	Summary     *string `json:"summary,omitempty"`
	Description *string `json:"description,omitempty"`
	Location    *string `json:"location,omitempty"`
	TimeZone    *string `json:"timeZone,omitempty"`
}

// ApplyTo folds a patch into a calendar: what the resource looks like
// once Google has applied it. Lives beside the type so the service and
// the in-memory Calendar cannot drift, as EventPatch.ApplyTo does.
func (p CalendarPatch) ApplyTo(c *Calendar) {
	if p.Summary != nil {
		c.Summary = *p.Summary
	}
	if p.Description != nil {
		c.Description = *p.Description
	}
	if p.Location != nil {
		c.Location = *p.Location
	}
	if p.TimeZone != nil {
		c.TimeZone = *p.TimeZone
	}
}

// CalendarListPatch is the body of a calendarList.patch call: the
// overrides that belong to THIS user's subscription and nobody else's.
type CalendarListPatch struct {
	// SummaryOverride is the name this user gives the calendar. Google
	// keeps the calendar's own title untouched, so a colleague sees no
	// change at all.
	SummaryOverride *string `json:"summaryOverride,omitempty"`
	ColorID         *string `json:"colorId,omitempty"`
	Hidden          *bool   `json:"hidden,omitempty"`
	Selected        *bool   `json:"selected,omitempty"`
	// NotificationSettings replaces the whole notification list, because
	// that is what Google does with it: the object is written whole.
	NotificationSettings *NotificationSettings `json:"notificationSettings,omitempty"`
}

// ApplyTo folds a patch into a subscription entry.
func (p CalendarListPatch) ApplyTo(e *CalendarListEntry) {
	if p.SummaryOverride != nil {
		e.SummaryOverride = *p.SummaryOverride
	}
	if p.ColorID != nil {
		e.ColorID = *p.ColorID
	}
	if p.Hidden != nil {
		e.Hidden = *p.Hidden
	}
	if p.Selected != nil {
		e.Selected = *p.Selected
	}
	if p.NotificationSettings != nil {
		e.NotificationSettings = p.NotificationSettings
	}
}

// NotificationSettings is what this user is emailed about on one
// calendar.
type NotificationSettings struct {
	// Notifications is the whole list: Google replaces it rather than
	// merging, so an empty non-nil slice turns them all off.
	Notifications []CalendarNotification `json:"notifications"`
}

// CalendarNotification is one notification this user receives.
type CalendarNotification struct {
	Type string `json:"type,omitempty"`
	// Method is always "email". The discovery document lists exactly one
	// possible value, so a parameter for it would be a choice with one
	// option — the server fills it in and says so.
	Method string `json:"method,omitempty"`
}

// NotificationMethodEmail is the only delivery method Google publishes.
const NotificationMethodEmail = "email"

// The notification types, in the order a refusal lists them.
const (
	NotifyEventCreation     = "eventCreation"
	NotifyEventChange       = "eventChange"
	NotifyEventCancellation = "eventCancellation"
	NotifyEventResponse     = "eventResponse"
	NotifyAgenda            = "agenda"
)

// NotificationTypes is the published vocabulary.
var NotificationTypes = []string{
	NotifyEventCreation, NotifyEventChange, NotifyEventCancellation,
	NotifyEventResponse, NotifyAgenda,
}

// NotificationMeans says what one notification type is, in the words a
// person uses. Google's own names are camel-case API spellings.
func NotificationMeans(t string) string {
	switch t {
	case NotifyEventCreation:
		return "a new event is put on this calendar"
	case NotifyEventChange:
		return "an event on it changes"
	case NotifyEventCancellation:
		return "an event on it is cancelled"
	case NotifyEventResponse:
		return "a guest answers an invitation"
	case NotifyAgenda:
		return "the day's agenda, sent each morning"
	default:
		return "a notification type this server does not recognise"
	}
}

// Roles is the ACL vocabulary, weakest first.
var Roles = []string{
	RoleFreeBusyReader, RoleReader, RoleWriterWithoutPrivateData, RoleWriter, RoleOwner,
}

// ScopeTypes is the ACL scope vocabulary.
var ScopeTypes = []string{ScopeTypeUser, ScopeTypeGroup, ScopeTypeDomain, ScopeTypeDefault}

// ScopeMeans says who a scope type covers.
func ScopeMeans(t string) string {
	switch t {
	case ScopeTypeUser:
		return "one person, by address"
	case ScopeTypeGroup:
		return "a group, by its address"
	case ScopeTypeDomain:
		return "everybody in a domain"
	case ScopeTypeDefault:
		return "anybody at all, signed in or not"
	default:
		return "a scope type this server does not recognise"
	}
}

// SameScope reports whether two ACL scopes name the same audience.
//
// Addresses and domains are compared case-insensitively, because Google
// returns them in whatever case the rule was written in and a caller
// typing the other case means the same person. The public scope carries
// no value, so its type alone decides.
func SameScope(a, b AclScope) bool {
	if !strings.EqualFold(a.Type, b.Type) {
		return false
	}
	if a.Type == ScopeTypeDefault {
		return true
	}
	return strings.EqualFold(a.Value, b.Value)
}

// ----------------------------------------------------- conference data

// ConferenceSolutionMeet is the only conference a client may create.
// The discovery document (revision 20260826) deprecates both hangout
// types for new conferences and reserves "addOn" for third parties, so
// the type is a constant here rather than a parameter offering choices
// that cannot be taken.
const ConferenceSolutionMeet = "hangoutsMeet"

// The three states a create request can be in.
//
// Google documents the data as generated ASYNCHRONOUSLY, and a live run
// answered the insert with "success" and the link already in it (§18 row
// 60). Both are true: the link is usually there at once, and "pending"
// is a published value this server must not report as a link. The status
// is carried rather than assumed either way.
const (
	ConferencePending = "pending"
	ConferenceSuccess = "success"
	ConferenceFailure = "failure"
)

// conferenceEntryVideo is the entry point a person clicks. A conference
// has at most one.
const conferenceEntryVideo = "video"

// conferenceData is the shape this package reads out of the raw field.
// It is deliberately partial: the wire value is kept whole as
// json.RawMessage so a round trip cannot lose a field this struct does
// not know about.
type conferenceData struct {
	CreateRequest *conferenceCreateRequest `json:"createRequest,omitempty"`
	EntryPoints   []conferenceEntryPoint   `json:"entryPoints,omitempty"`
}

type conferenceCreateRequest struct {
	RequestID             string                 `json:"requestId,omitempty"`
	Status                *conferenceStatus      `json:"status,omitempty"`
	ConferenceSolutionKey *conferenceSolutionKey `json:"conferenceSolutionKey,omitempty"`
}

type conferenceStatus struct {
	StatusCode string `json:"statusCode,omitempty"`
}

type conferenceSolutionKey struct {
	Type string `json:"type,omitempty"`
}

type conferenceEntryPoint struct {
	EntryPointType string `json:"entryPointType,omitempty"`
	URI            string `json:"uri,omitempty"`
}

// Conference is what a caller needs to know about a meeting's video
// link: whether there is one, where it is, and whether Google is still
// making it.
//
// Three fields, because three are read. The solution's name, the entry
// point's label and the domain administrator's notes are all in the
// wire value and none of them is printed anywhere — a field parsed and
// never shown is one a reader has to chase to find out it does nothing.
// The raw JSON is kept on the event either way, so adding one later
// costs a line here and nothing at the boundary.
type Conference struct {
	// Present is false when the event carries no conference data at all.
	Present bool
	// Status is the create request's state, empty on a conference that
	// was not created through a request.
	Status string
	// URI is the video entry point, empty while a request is pending or
	// after one failed.
	URI string
}

// ConferenceState is what a caller can do about an event's conference,
// which is not the same as the create request's status: an event can
// carry conference data with no video entry point and no request at all,
// and that is neither ready nor pending nor failed.
type ConferenceState int

// The four states, and there are only four.
const (
	// NoConference: the event has none.
	NoConference ConferenceState = iota
	// ConferenceReady: there is a link to join.
	ConferenceReady
	// ConferenceComing: Google is still making it.
	ConferenceComing
	// ConferenceRefused: the create request failed, and the event exists
	// without a link.
	ConferenceRefused
	// ConferenceOther: conference data this server cannot turn into a
	// link — a phone-only conference, or a third-party provider. Its own
	// state rather than a silent "none", because a result that reported
	// it as no conference would be wrong about an event that has one.
	ConferenceOther
)

// State is the one reading of the union, so the text, the structured
// result and the write note cannot disagree about an event. They did:
// the text said "conference data with no video link in it" while the
// JSON reported no conference at all.
func (c Conference) State() ConferenceState {
	switch {
	case !c.Present:
		return NoConference
	case c.URI != "":
		return ConferenceReady
	case c.Status == ConferencePending:
		return ConferenceComing
	case c.Status == ConferenceFailure:
		return ConferenceRefused
	default:
		return ConferenceOther
	}
}

// Pending reports whether Google is still making the conference, so a
// result must not promise a link yet.
func (c Conference) Pending() bool { return c.State() == ConferenceComing }

// Failed reports whether the create request failed. The event exists;
// the meeting link does not.
func (c Conference) Failed() bool { return c.State() == ConferenceRefused }

// Ready reports whether there is a link to join.
func (c Conference) Ready() bool { return c.State() == ConferenceReady }

// NewConferenceRequest is the body that asks Google to attach a Meet
// conference to an event.
//
// requestId is the caller's, and this server passes the event id: the
// discovery document says a request repeating an id is IGNORED, so a
// retry of a create that may have landed (§2.11) cannot produce a second
// conference. An id regenerated per attempt would.
//
// The request only asks, and Conference carries the status rather than a
// promise: the answer may be "success" with the link in it, which is
// what one live run saw, or "pending" with no entry point yet.
func NewConferenceRequest(requestID string) json.RawMessage {
	raw, err := json.Marshal(conferenceData{CreateRequest: &conferenceCreateRequest{
		RequestID:             requestID,
		ConferenceSolutionKey: &conferenceSolutionKey{Type: ConferenceSolutionMeet},
	}})
	if err != nil {
		// Unreachable: the value is this package's own struct of
		// strings. Returning nil rather than panicking means a marshal
		// that somehow failed creates an event without a conference
		// instead of killing the process mid-write.
		return nil
	}
	return raw
}

// ReadConference reads what a result needs out of an event's raw
// conference data.
//
// A shape this package does not recognise reports Present without a URI
// rather than an error: the field is a union Google extends, the caller
// asked about an event rather than about a conference, and failing a
// read because a third-party provider nested something unexpectedly
// would lose the whole event over the least important part of it.
func ReadConference(raw json.RawMessage) Conference {
	if len(raw) == 0 {
		return Conference{}
	}
	out := Conference{Present: true}
	var data conferenceData
	if err := json.Unmarshal(raw, &data); err != nil {
		return out
	}
	if cr := data.CreateRequest; cr != nil && cr.Status != nil {
		out.Status = cr.Status.StatusCode
	}
	for _, ep := range data.EntryPoints {
		if ep.EntryPointType != conferenceEntryVideo {
			continue
		}
		out.URI = ep.URI
		break
	}
	return out
}

// AllowsMeet reports whether a calendar accepts a Meet conference.
//
// An EMPTY list is not a refusal. Google documents the field as optional
// and it is absent on calendars that do create conferences, so treating
// absence as "forbidden" would refuse a write that works. The check is
// only for the case the list is present and says no.
func (p *ConferenceProperties) AllowsMeet() bool {
	if p == nil || len(p.AllowedConferenceSolutionTypes) == 0 {
		return true
	}
	for _, t := range p.AllowedConferenceSolutionTypes {
		if t == ConferenceSolutionMeet {
			return true
		}
	}
	return false
}
