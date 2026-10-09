package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v3/internal/service"
)

// The three sentences every write tool has to say, written once here
// because they are the rules a model most needs before its first write
// and the ones it will otherwise learn from a refusal.

const notifyHelp = "`notify` is REQUIRED whenever the write can reach another person, and this server has no " +
	"default in either direction: `none` asks Google to email nobody, `external_only` emails the guests " +
	"outside your own organization, `all` emails every guest. `none` is refused outright when a guest is " +
	"outside your organization, because such a guest may have no Google Calendar for the event to appear " +
	"in and email is then the only way they can learn of it. Whatever you pass, the result says what was " +
	"ASKED FOR — Google reports nothing about what arrived, and `none` is not a promise of silence."

const scopeHelp = "`scope` is REQUIRED when the event repeats, and there is no default because the same words " +
	"mean three different things: `instance` changes one occurrence, `series` changes all of them, and " +
	"`this_and_following` changes this occurrence and every later one. Name the occurrence with the " +
	"occurrence's own id, or with the series id plus `original_start` — the scheduled start, which " +
	"list_instances reports, and which still identifies an occurrence somebody has moved."

const dryRunHelp = "`dry_run: true` reports exactly what would change and how many guests would be emailed, " +
	"without writing anything. Use it when you are not certain which event or which occurrence you have."

const addressHelp = "An address may be bare or a mailbox such as \"Sample Person\" <person@example.com>, the way a " +
	"mail server hands one over; only the address is used, and one entry holds one address."

const remindersHelp = "`popup_reminders` and `email_reminders` are minutes before the start, 0 to 40320, at most " +
	"five in all. Either one replaces the whole set, so pass both to keep both kinds, and an empty list given " +
	"alone means no reminders. `default_reminders: true` goes back to the calendar's own. Reminders are " +
	"yours alone: Google keeps them per person, so they never notify a guest."

const visibilityHelp = "`visibility` is default, public or private: private shows the details only to the " +
	"guests, and public to everybody who can see the calendar. `guests_can_modify`, " +
	"`guests_can_invite_others` and `guests_can_see_other_guests` say what a guest may do."

const statusHelp = "`event_type` makes a status event, which only your primary calendar can hold: outOfOffice, " +
	"focusTime or workingLocation. Out of office and focus time are timed, never all day, and always show you " +
	"busy. `auto_decline` is REQUIRED for them, with no default: none declines nothing, new declines " +
	"invitations that arrive for that time, and all also declines the meetings you already accepted. Google " +
	"shows each organizer the decline, with `decline_message` if you give one, and all is put to the person " +
	"first. Focus time takes `chat_status`: available or do_not_disturb. A working location takes " +
	"`working_location` (home, office or custom) and `working_location_label` for the office or place; it is " +
	"timed or exactly one whole day, always public, and shows you free. A status event takes no guests, rooms " +
	"or Meet link."

const etagHelp = "Every write is a patch under If-Match, so it is refused as `[stale]` rather than " +
	"overwriting somebody who changed this first. Pass `etag` from the get_event you decided on to be held " +
	"to that exact version; pass `force: true` only when you genuinely mean \"whatever it says now\"."

func registerWrite(s *mcp.Server, d Deps) {
	add(s, d, Def[createEventIn, service.WriteResult]{
		Name: "create_event",
		Description: "Create an event. " +
			"`start` and `end` are either two yyyy-mm-dd dates for an all-day event, where `end` is the LAST " +
			"day and is included, or two RFC3339 timestamps for a timed one — never one of each, because a " +
			"date is not a time. Times are written in `time_zone` if you pass one, otherwise the calendar's " +
			"own zone; the result says which it used, and the zone is always sent alongside the timestamp so " +
			"a repeating event keeps its wall-clock time across a daylight-saving change. " +
			"`recurrence` takes RFC 5545 lines such as RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10. " +
			"`conference: true` asks Google for a Meet link. The link normally comes back with the event, " +
			"but Google may still be making it — the result says which, and when it says the link is " +
			"still being made, read the event again to get it rather than promising anybody a link. " +
			"update_event's `add_conference` adds one to an event later. " +
			"`optional_guests` invites people as optional; they are emailed like any guest, so they make " +
			"notify required too. A room cannot be optional. " + addressHelp + " " +
			remindersHelp + " " + visibilityHelp + " " + statusHelp + " " + notifyHelp + " " + dryRunHelp,
		Kind: Write,
		// Asks only before a status event that declines every meeting it
		// overlaps (§9a).
		Asks: true,
		Handle: func(ctx context.Context, in createEventIn) (service.WriteResult, error) {
			out, err := d.Service.CreateEvent(ctx, service.CreateOptions{
				Calendar: in.Calendar, Title: in.Title, Start: in.Start, End: in.End,
				TimeZone: in.TimeZone, Description: in.Description, Location: in.Location,
				Guests: in.Guests, OptionalGuests: in.OptionalGuests, Rooms: in.Rooms,
				Recurrence: in.Recurrence, Transparent: in.FreeNotBusy,
				PopupReminders: in.PopupReminders, EmailReminders: in.EmailReminders,
				DefaultReminders: in.DefaultReminders, Visibility: in.Visibility,
				GuestsCanModify: in.GuestsCanModify, GuestsCanInviteOthers: in.GuestsCanInviteOthers,
				GuestsCanSeeOtherGuests: in.GuestsCanSeeOtherGuests, Conference: in.Conference,
				EventType: in.EventType, AutoDecline: in.AutoDecline, DeclineMessage: in.DeclineMessage,
				ChatStatus: in.ChatStatus, WorkingLocation: in.WorkingLocation,
				WorkingLocationLabel: in.WorkingLocationLabel, Notify: in.Notify, DryRun: in.DryRun,
			})
			if err != nil {
				return service.WriteResult{}, err
			}
			return service.NewWriteResult(out), nil
		},
	})

	add(s, d, Def[updateEventIn, service.WriteResult]{
		Name: "update_event",
		Description: "Change an event. Only the fields you pass are touched; everything else is left exactly " +
			"as it is, so this can never drop a guest list or flatten a series the way a whole-resource " +
			"write does. " +
			"Guests are added and removed one at a time with `add_guests` and `remove_guests`, optional " +
			"guests added with `add_optional_guests`, and rooms booked with `add_rooms`, applied to " +
			"the list as it is read, so somebody else's RSVP arriving in between is reported rather than " +
			"overwritten. An address already on the event keeps the role it has, and the result names it: " +
			"this server does not make a guest optional or required. " + addressHelp + " " + scopeHelp + " " +
			"`this_and_following` is two calls: the original series is ended before this occurrence and a " +
			"NEW series starts at it with a new id, and any exception after this occurrence is reset. The " +
			"result says so. " + remindersHelp + " A write that changes only reminders needs no `notify`. " +
			visibilityHelp + " On one occurrence, Google ignores a less restrictive visibility, so it is " +
			"refused, and applies a more restrictive one to the whole series, which the result says. " +
			notifyHelp + " " + etagHelp + " " + dryRunHelp + " " +
			"`add_conference: true` asks Google for a Meet link on an event that has no conference; one " +
			"that has a conference is refused, because Google would replace it, and the result says " +
			"whether the link came back or is still being made. It cannot go with `this_and_following`. " +
			"Use respond_to_event to answer an invitation and move_event to change which calendar it is on.",
		Kind: IdempotentWrite,
		Handle: func(ctx context.Context, in updateEventIn) (service.WriteResult, error) {
			out, err := d.Service.UpdateEvent(ctx, service.UpdateOptions{
				Calendar: in.Calendar, EventID: in.EventID, OriginalStart: in.OriginalStart,
				Scope: in.Scope, TimeZone: in.TimeZone,
				Title: in.Title, Description: in.Description, Location: in.Location,
				Start: in.Start, End: in.End, Recurrence: in.Recurrence,
				AddGuests: in.AddGuests, RemoveGuests: in.RemoveGuests, AddRooms: in.AddRooms,
				AddOptionalGuests: in.AddOptionalGuests, Transparent: in.FreeNotBusy,
				PopupReminders: in.PopupReminders, EmailReminders: in.EmailReminders,
				DefaultReminders: in.DefaultReminders, Visibility: in.Visibility,
				GuestsCanModify: in.GuestsCanModify, GuestsCanInviteOthers: in.GuestsCanInviteOthers,
				GuestsCanSeeOtherGuests: in.GuestsCanSeeOtherGuests, AddConference: in.AddConference,
				Notify: in.Notify, ETag: in.ETag, Force: in.Force, DryRun: in.DryRun,
			})
			if err != nil {
				return service.WriteResult{}, err
			}
			return service.NewWriteResult(out), nil
		},
	})

	add(s, d, Def[cancelEventIn, service.WriteResult]{
		Name: "cancel_event",
		Description: "Cancel an event, or one occurrence of a repeating one. " +
			"A whole event is deleted; one occurrence is marked canceled, which is how a single date leaves " +
			"a series — the result says which of the two happened. Google keeps the record either way, and " +
			"list_events with show_canceled still shows it. " + scopeHelp + " " +
			"With `this_and_following` the series simply ends before this occurrence, which is one call " +
			"rather than the two an update takes. " + notifyHelp + " " +
			"Read that rule twice here: canceling with no notification removes the meeting from YOUR " +
			"calendar and leaves it on your guests'. They will still turn up. " + etagHelp + " " + dryRunHelp,
		Kind: Canceling,
		Asks: true,
		Handle: func(ctx context.Context, in cancelEventIn) (service.WriteResult, error) {
			out, err := d.Service.CancelEvent(ctx, service.CancelOptions{
				Calendar: in.Calendar, EventID: in.EventID, OriginalStart: in.OriginalStart,
				Scope: in.Scope, TimeZone: in.TimeZone, Notify: in.Notify,
				ETag: in.ETag, Force: in.Force, DryRun: in.DryRun,
			})
			if err != nil {
				return service.WriteResult{}, err
			}
			return service.NewWriteResult(out), nil
		},
	})

	add(s, d, Def[moveEventIn, service.WriteResult]{
		Name: "move_event",
		Description: "Move an event to another calendar, which changes who organizes it. " +
			"The event keeps its id but is addressed on the new calendar from then on. This does NOT change " +
			"the time — use update_event for that. " + scopeHelp + " " +
			"`this_and_following` is not available here: there is nothing to split when the event is simply " +
			"changing calendars. " + notifyHelp + " " + etagHelp + " " + dryRunHelp,
		Kind: Write,
		Handle: func(ctx context.Context, in moveEventIn) (service.WriteResult, error) {
			out, err := d.Service.MoveEvent(ctx, service.MoveOptions{
				Calendar: in.Calendar, EventID: in.EventID, ToCalendar: in.ToCalendar,
				OriginalStart: in.OriginalStart, Scope: in.Scope, TimeZone: in.TimeZone,
				Notify: in.Notify, ETag: in.ETag, Force: in.Force, DryRun: in.DryRun,
			})
			if err != nil {
				return service.WriteResult{}, err
			}
			return service.NewWriteResult(out), nil
		},
	})

	add(s, d, Def[respondToEventIn, service.WriteResult]{
		Name: "respond_to_event",
		Description: "Answer an invitation: accepted, declined or tentative, with an optional comment. " +
			"This changes only THIS account's response and nothing else on the event — which is why it is a " +
			"separate tool rather than an update. Do not RSVP by patching the guest list; that overwrites " +
			"everybody else's answer. " + scopeHelp + " " +
			"`this_and_following` is not available here: answering some occurrences and not others would " +
			"mean rewriting somebody else's series. " + notifyHelp + " " + dryRunHelp,
		Kind: IdempotentWrite,
		Handle: func(ctx context.Context, in respondToEventIn) (service.WriteResult, error) {
			out, err := d.Service.RespondToEvent(ctx, service.RespondOptions{
				Calendar: in.Calendar, EventID: in.EventID, OriginalStart: in.OriginalStart,
				Scope: in.Scope, TimeZone: in.TimeZone, Response: in.Response, Comment: in.Comment,
				Notify: in.Notify, ETag: in.ETag, Force: in.Force, DryRun: in.DryRun,
			})
			if err != nil {
				return service.WriteResult{}, err
			}
			return service.NewWriteResult(out), nil
		},
	})
}

// Inputs. Flat, snake_case, no unions — and a pointer wherever the
// difference between "leave it alone" and "empty it" has to survive the
// schema, which is what makes update_event a patch.

type createEventIn struct {
	Calendar    string   `json:"calendar,omitempty" jsonschema:"The calendar id or title to create it on. Defaults to the account's primary calendar."`
	Title       string   `json:"title" jsonschema:"What the event is called. Required."`
	Start       string   `json:"start" jsonschema:"Start: yyyy-mm-dd for an all-day event, or RFC3339 for a timed one. Required."`
	End         string   `json:"end" jsonschema:"End: for an all-day event the LAST day, included; for a timed one an RFC3339 timestamp. Must be the same kind as start. Required."`
	TimeZone    string   `json:"time_zone,omitempty" jsonschema:"IANA zone the times are written in, such as Europe/Copenhagen. Defaults to the calendar's own."`
	Description string   `json:"description,omitempty" jsonschema:"Longer text on the event."`
	Location    string   `json:"location,omitempty" jsonschema:"Where it is."`
	Guests      []string `json:"guests,omitempty" jsonschema:"Email addresses to invite. Passing any of these makes notify required."`
	// OptionalGuests are reached like any guest, so they count toward
	// notify; only their role differs.
	OptionalGuests []string `json:"optional_guests,omitempty" jsonschema:"Email addresses to invite as optional guests. They are emailed like any guest, so passing any makes notify required. A room cannot be optional."`
	Rooms          []string `json:"rooms,omitempty" jsonschema:"Addresses of rooms or other resources to book. A room is not a guest, so it does not make notify required; an address here that is not a room's still counts as one."`
	Recurrence     []string `json:"recurrence,omitempty" jsonschema:"RFC 5545 lines, such as RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10."`
	FreeNotBusy    bool     `json:"free_not_busy,omitempty" jsonschema:"Mark the time as free rather than busy, so it does not block availability."`
	// The reminder lists are told apart from absent by nil, so an empty
	// list given alone means no reminders.
	PopupReminders          []int  `json:"popup_reminders,omitempty" jsonschema:"Popup reminders for you, in minutes before the start, 0 to 40320. Replaces the calendar's default reminders; an empty list given alone means none."`
	EmailReminders          []int  `json:"email_reminders,omitempty" jsonschema:"Email reminders for you, in minutes before the start, 0 to 40320. At most five reminders in all."`
	DefaultReminders        bool   `json:"default_reminders,omitempty" jsonschema:"Use the calendar's own reminders, which is also what happens when no list is given."`
	Visibility              string `json:"visibility,omitempty" jsonschema:"default, public or private. Private shows the details only to the guests."`
	GuestsCanModify         *bool  `json:"guests_can_modify,omitempty" jsonschema:"Whether guests may change the event. Google's default is false."`
	GuestsCanInviteOthers   *bool  `json:"guests_can_invite_others,omitempty" jsonschema:"Whether guests may invite others. Google's default is true."`
	GuestsCanSeeOtherGuests *bool  `json:"guests_can_see_other_guests,omitempty" jsonschema:"Whether guests see the guest list. Google's default is true."`
	Conference              bool   `json:"conference,omitempty" jsonschema:"Ask Google for a Google Meet link. Usually in the answer; if it says the link is still being made, read the event again for it."`
	// The status event inputs (§7.4). Each value is spelled as a read
	// reports it.
	EventType            string `json:"event_type,omitempty" jsonschema:"Make a status event on your primary calendar: outOfOffice, focusTime or workingLocation. Leave it out for an ordinary event."`
	AutoDecline          string `json:"auto_decline,omitempty" jsonschema:"Required for outOfOffice and focusTime, with no default: none, new (invitations that arrive for that time) or all (also the meetings you already accepted, which asks the person first)."`
	DeclineMessage       string `json:"decline_message,omitempty" jsonschema:"The message each organizer gets with a decline. outOfOffice and focusTime only, and not with auto_decline none."`
	ChatStatus           string `json:"chat_status,omitempty" jsonschema:"focusTime only: available or do_not_disturb, what Google Chat shows during it."`
	WorkingLocation      string `json:"working_location,omitempty" jsonschema:"Required for workingLocation: home, office or custom."`
	WorkingLocationLabel string `json:"working_location_label,omitempty" jsonschema:"The office's name, or the place for custom, as Calendar shows it. Not with home."`
	Notify               string `json:"notify,omitempty" jsonschema:"Who Google is asked to email: none, external_only or all. Required when the event has guests."`
	DryRun               bool   `json:"dry_run,omitempty" jsonschema:"Report what would be created and who would be emailed, without writing."`
}

type updateEventIn struct {
	Calendar      string `json:"calendar" jsonschema:"The calendar the event is on. An event id is unique per calendar, not globally."`
	EventID       string `json:"event_id" jsonschema:"The event id, or the series id when original_start names the occurrence."`
	OriginalStart string `json:"original_start,omitempty" jsonschema:"The occurrence's SCHEDULED start, from list_instances. Use it with the series id to name one occurrence."`
	Scope         string `json:"scope,omitempty" jsonschema:"Required when the event repeats: instance, series or this_and_following."`
	TimeZone      string `json:"time_zone,omitempty" jsonschema:"IANA zone the new times are written in."`

	Title        *string   `json:"title,omitempty" jsonschema:"New title. Omit to leave it; pass an empty string to clear it."`
	Description  *string   `json:"description,omitempty" jsonschema:"New description. Omit to leave it; pass an empty string to clear it."`
	Location     *string   `json:"location,omitempty" jsonschema:"New location. Omit to leave it; pass an empty string to clear it."`
	Start        string    `json:"start,omitempty" jsonschema:"New start: yyyy-mm-dd or RFC3339, matching what the event already is."`
	End          string    `json:"end,omitempty" jsonschema:"New end. For an all-day event this is the last day, included."`
	Recurrence   *[]string `json:"recurrence,omitempty" jsonschema:"New RFC 5545 lines. An empty list stops the event repeating."`
	AddGuests    []string  `json:"add_guests,omitempty" jsonschema:"Email addresses to invite, added to the guests already there."`
	RemoveGuests []string  `json:"remove_guests,omitempty" jsonschema:"Email addresses to uninvite, rooms included."`
	AddRooms     []string  `json:"add_rooms,omitempty" jsonschema:"Addresses of rooms or other resources to book. A room is not a guest; an address here that is not a room's still counts as one."`
	// AddOptionalGuests is add only. Changing an existing guest's role
	// is not something this server does.
	AddOptionalGuests []string `json:"add_optional_guests,omitempty" jsonschema:"Email addresses to invite as optional guests, added to the guests already there. An address already on the event keeps the role it has, and the result says so. A room cannot be optional."`
	FreeNotBusy       *bool    `json:"free_not_busy,omitempty" jsonschema:"Mark the time free rather than busy."`

	PopupReminders          *[]int  `json:"popup_reminders,omitempty" jsonschema:"Your popup reminders, in minutes before the start, 0 to 40320. Replaces all your reminders on the event; an empty list given alone removes them."`
	EmailReminders          *[]int  `json:"email_reminders,omitempty" jsonschema:"Your email reminders, in minutes before the start, 0 to 40320. At most five reminders in all."`
	DefaultReminders        bool    `json:"default_reminders,omitempty" jsonschema:"Go back to the calendar's own reminders."`
	Visibility              *string `json:"visibility,omitempty" jsonschema:"default, public or private. On one occurrence a less restrictive value is refused, because Google ignores it."`
	GuestsCanModify         *bool   `json:"guests_can_modify,omitempty" jsonschema:"Whether guests may change the event."`
	GuestsCanInviteOthers   *bool   `json:"guests_can_invite_others,omitempty" jsonschema:"Whether guests may invite others."`
	GuestsCanSeeOtherGuests *bool   `json:"guests_can_see_other_guests,omitempty" jsonschema:"Whether guests see the guest list."`
	AddConference           bool    `json:"add_conference,omitempty" jsonschema:"Ask Google for a Google Meet link. Refused when the event already has a conference."`

	Notify string `json:"notify,omitempty" jsonschema:"Who Google is asked to email: none, external_only or all. Required when the event has guests or the call adds one."`
	ETag   string `json:"etag,omitempty" jsonschema:"The etag from the get_event you decided on. The write is refused as stale if it moved since."`
	Force  bool   `json:"force,omitempty" jsonschema:"Write with If-Match: * — overwrite whatever it says now, rather than being refused if somebody changed it."`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"Report what would change and who would be emailed, without writing."`
}

type cancelEventIn struct {
	Calendar      string `json:"calendar" jsonschema:"The calendar the event is on."`
	EventID       string `json:"event_id" jsonschema:"The event id, or the series id when original_start names the occurrence."`
	OriginalStart string `json:"original_start,omitempty" jsonschema:"The occurrence's SCHEDULED start, from list_instances."`
	Scope         string `json:"scope,omitempty" jsonschema:"Required when the event repeats: instance, series or this_and_following."`
	TimeZone      string `json:"time_zone,omitempty" jsonschema:"IANA zone to read original_start and show the times in."`
	Notify        string `json:"notify,omitempty" jsonschema:"Who Google is asked to email: none, external_only or all. Required when the event has guests — with none, the guests keep the meeting."`
	ETag          string `json:"etag,omitempty" jsonschema:"The etag from the get_event you decided on."`
	Force         bool   `json:"force,omitempty" jsonschema:"Cancel with If-Match: * rather than being refused if somebody changed it first."`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"Report what would be canceled and who would be emailed, without writing."`
}

type moveEventIn struct {
	Calendar      string `json:"calendar" jsonschema:"The calendar the event is on now."`
	EventID       string `json:"event_id" jsonschema:"The event id."`
	ToCalendar    string `json:"to_calendar" jsonschema:"The calendar id or title to move it to. Required."`
	OriginalStart string `json:"original_start,omitempty" jsonschema:"The occurrence's SCHEDULED start, when moving one occurrence of a series."`
	Scope         string `json:"scope,omitempty" jsonschema:"Required when the event repeats: instance or series."`
	TimeZone      string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the times in."`
	Notify        string `json:"notify,omitempty" jsonschema:"Who Google is asked to email: none, external_only or all. Required when the event has guests."`
	ETag          string `json:"etag,omitempty" jsonschema:"The etag from the get_event you decided on. The move is refused as stale if it moved since."`
	Force         bool   `json:"force,omitempty" jsonschema:"Move with If-Match: * rather than being refused if somebody changed it first."`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"Report what would move and who would be emailed, without writing."`
}

type respondToEventIn struct {
	Calendar      string `json:"calendar" jsonschema:"The calendar the invitation is on."`
	EventID       string `json:"event_id" jsonschema:"The event id, or the series id when original_start names the occurrence."`
	Response      string `json:"response" jsonschema:"accepted, declined or tentative. Required."`
	Comment       string `json:"comment,omitempty" jsonschema:"A note that goes alongside the answer."`
	OriginalStart string `json:"original_start,omitempty" jsonschema:"The occurrence's SCHEDULED start, when answering for one occurrence."`
	Scope         string `json:"scope,omitempty" jsonschema:"Required when the event repeats: instance or series."`
	TimeZone      string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the times in."`
	Notify        string `json:"notify,omitempty" jsonschema:"Who Google is asked to email: none, external_only or all. Required when the event has guests."`
	ETag          string `json:"etag,omitempty" jsonschema:"The etag from the get_event you decided on."`
	Force         bool   `json:"force,omitempty" jsonschema:"Answer with If-Match: * rather than being refused if somebody changed it first."`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"Report what would change and who would be emailed, without writing."`
}
