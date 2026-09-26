package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/service"
)

// The window arguments every schedule read shares. Said once here and
// referenced from each description, because the rule that a relative
// expression is the caller's to resolve is the one a model most needs to
// read before its first call.
const windowHelp = "`from` and `to` are required and take either a yyyy-mm-dd date or an RFC3339 timestamp. " +
	"A bare date means that whole day in the resolved zone, so from and to on the same date is that one day. " +
	"Resolve relative expressions like \"next Tuesday\" yourself before calling: this server does not guess, " +
	"and the result always echoes the absolute window and the zone it used so you can check the resolution."

const zoneHelp = "Times are shown in `time_zone` if you pass one, otherwise the calendar's own zone, " +
	"otherwise the zone in the account's Calendar settings. The result names which of the three it used. " +
	"This server never falls back to the machine's own clock zone."

func registerRead(s *mcp.Server, d Deps) {
	add(s, d, Def[listCalendarsIn, service.CalendarsResult]{
		Name: "list_calendars",
		Description: "List the calendars this account can see, with each one's id, time zone and your access level. " +
			"Start here: every other tool takes a calendar, the ids come from this list, and the time zone it " +
			"reports is what dates and times elsewhere are read against. It is cheap. " +
			"Use get_calendar for one calendar's description and who it is shared with.",
		Kind: Read,
		Handle: func(ctx context.Context, in listCalendarsIn) (service.CalendarsResult, error) {
			cals, err := d.Service.Calendars(ctx, in.IncludeHidden)
			if err != nil {
				return service.CalendarsResult{}, err
			}
			return service.NewCalendarsResult(cals), nil
		},
	})

	add(s, d, Def[getCalendarIn, service.CalendarResult]{
		Name: "get_calendar",
		Description: "One calendar in full: its description, time zone, your access level, and who else it is " +
			"shared with. Use list_calendars to see every calendar; use this when you need the sharing list or " +
			"are checking whether you may write somewhere.",
		Kind: Read,
		Handle: func(ctx context.Context, in getCalendarIn) (service.CalendarResult, error) {
			return d.Service.CalendarDetail(ctx, in.Calendar)
		},
	})

	add(s, d, Def[listEventsIn, service.ScheduleResult]{
		Name: "list_events",
		Description: "Read a schedule: the events on one or more calendars in a window. " + windowHelp + " " + zoneHelp + " " +
			"`expand` decides which shape you get, and the two are genuinely different: with expand=true (the " +
			"default) a repeating event appears as each of its occurrences, which is what you want to answer " +
			"\"what is on this week\". With expand=false it appears once, as a series with its recurrence rule, " +
			"which is what you want before changing the whole series. " +
			"Canceled events are hidden unless show_canceled is set. " +
			"Use search_events to find an event by text; use check_availability to find free time, because a list " +
			"of events is not the same as being free.",
		Kind: Read,
		Handle: func(ctx context.Context, in listEventsIn) (service.ScheduleResult, error) {
			sched, err := d.Service.ListEvents(ctx, service.ListOptions{
				Calendars: in.Calendars, TimeZone: in.TimeZone,
				From: in.From, To: in.To,
				Expand:       !in.NoExpand,
				ShowCanceled: in.ShowCanceled,
				MaxEvents:    in.MaxEvents, PageToken: in.PageToken,
			})
			if err != nil {
				return service.ScheduleResult{}, err
			}
			return service.NewScheduleResult(sched), nil
		},
	})

	add(s, d, Def[searchEventsIn, service.ScheduleResult]{
		Name: "search_events",
		Description: "Find events matching free text in a window. " + windowHelp + " " +
			"Important: Google's event search is undocumented free text with no field scoping — you cannot search " +
			"\"attendee:someone\" or restrict it to titles, and Google does not say which fields it reads. " +
			"Treat an empty result as \"this search found nothing\", not as \"there is no such event\", and fall " +
			"back to list_events over the window when you need certainty. " +
			"Searching several calendars costs one request each.",
		Kind: Read,
		Handle: func(ctx context.Context, in searchEventsIn) (service.ScheduleResult, error) {
			sched, err := d.Service.ListEvents(ctx, service.ListOptions{
				Calendars: in.Calendars, TimeZone: in.TimeZone,
				From: in.From, To: in.To, Query: in.Query,
				Expand:    true,
				MaxEvents: in.MaxEvents, PageToken: in.PageToken,
			})
			if err != nil {
				return service.ScheduleResult{}, err
			}
			return service.NewScheduleResult(sched), nil
		},
	})

	add(s, d, Def[getEventIn, service.EventResult]{
		Name: "get_event",
		Description: "One event in full, including its guests and their responses, its recurrence rule if it has " +
			"one, and its etag. " + zoneHelp + " " +
			"An event id is unique per calendar and not globally, so `calendar` is required alongside it. " +
			"Ids come from list_events or search_events.",
		Kind: Read,
		Handle: func(ctx context.Context, in getEventIn) (service.EventResult, error) {
			e, z, err := d.Service.GetEvent(ctx, in.Calendar, in.EventID, in.TimeZone)
			if err != nil {
				return service.EventResult{}, err
			}
			return service.NewEventResult(e, z), nil
		},
	})

	add(s, d, Def[listInstancesIn, service.InstancesResult]{
		Name: "list_instances",
		Description: "The occurrences of one repeating event. " +
			"`event_id` is the SERIES id, which list_events reports as series_id on each occurrence — an " +
			"occurrence's own id is not a series id. " +
			"Pass both from and to for a window, or neither for the whole series. " + zoneHelp + " " +
			"Each occurrence reports its own id, and says when it was moved from its scheduled time. " +
			"Canceled occurrences are hidden unless show_canceled is set, and a canceled occurrence is " +
			"how one date is removed from a series — so pass it when you need to know which dates are gone. " +
			"Use get_event on the series id to see the recurrence rule itself.",
		Kind: Read,
		Handle: func(ctx context.Context, in listInstancesIn) (service.InstancesResult, error) {
			out, err := d.Service.Instances(ctx, service.InstanceOptions{
				Calendar: in.Calendar, EventID: in.EventID, TimeZone: in.TimeZone,
				From: in.From, To: in.To, ShowCanceled: in.ShowCanceled,
				MaxEvents: in.MaxEvents, PageToken: in.PageToken,
			})
			if err != nil {
				return service.InstancesResult{}, err
			}
			return service.NewInstancesResult(out), nil
		},
	})

	add(s, d, Def[listChangesIn, service.ChangesResult]{
		Name: "list_changes",
		Description: "What changed on a calendar since you last looked. " +
			"This is the only read that reports DELETIONS: a deleted event stops matching a window, so " +
			"list_events cannot tell you it is gone, and this returns its id under deleted. " +
			"Call it once with no sync_token to get a baseline and a token; pass that token next time and " +
			"you get only what has changed since. " +
			"The token comes back with the LAST page only. A baseline therefore pages all the way to the " +
			"end to fetch one, and says how many rows it passed over on the way — they are covered by the " +
			"token, not lost. An INCREMENTAL read stops at its budget instead and hands back no token, " +
			"because every row there is a change you have not seen yet; continue with page_token until " +
			"the token arrives, and never store one you did not get. " +
			"It takes no window, no search and no ordering: Google forbids all of them alongside a sync " +
			"token, and deleted events are always included. " +
			"If the token has expired the call fails [stale]; ask again with no sync_token and start over.",
		Kind: Read,
		Handle: func(ctx context.Context, in listChangesIn) (service.ChangesResult, error) {
			out, err := d.Service.ListChanges(ctx, service.ChangesOptions{
				Calendar: in.Calendar, SyncToken: in.SyncToken,
				PageToken: in.PageToken, TimeZone: in.TimeZone,
				MaxEvents: in.MaxEvents,
			})
			if err != nil {
				return service.ChangesResult{}, err
			}
			return service.NewChangesResult(out), nil
		},
	})

	add(s, d, Def[checkAvailabilityIn, service.AvailabilityResult]{
		Name: "check_availability",
		Description: "When people are busy, and when they are free, in a window. " + windowHelp + " " + zoneHelp + " " +
			"This asks Google's free/busy service rather than listing events, which matters: it sees busy time " +
			"on calendars whose events you cannot read, and it respects events marked \"free\", so a list of " +
			"events is not the same answer. " +
			"A calendar that could not be read comes back as UNKNOWN, never as free — do not book over it. " +
			"The result also reports the gaps when nobody is busy; min_minutes drops the ones too short to " +
			"use. " +
			"working_from, working_to and working_days mask the gaps to a working week: a window is one " +
			"interval, so \"next week, 09:00 to 17:00\" cannot be asked for as a window and would otherwise " +
			"come back with a fifteen-hour gap every night. There is no default — without them every hour of " +
			"the window counts, and the result always says which was used. " +
			"Calendars are asked in batches of 50, and the result says how many requests that took.",
		Kind: Read,
		Handle: func(ctx context.Context, in checkAvailabilityIn) (service.AvailabilityResult, error) {
			out, err := d.Service.Availability(ctx, service.AvailabilityOptions{
				Calendars: in.Calendars, From: in.From, To: in.To,
				TimeZone: in.TimeZone, MinMinutes: in.MinMinutes,
				WorkingFrom: in.WorkingFrom, WorkingTo: in.WorkingTo, WorkingDays: in.WorkingDays,
			})
			if err != nil {
				return service.AvailabilityResult{}, err
			}
			return service.NewAvailabilityResult(out), nil
		},
	})

	add(s, d, Def[getSettingsIn, service.SettingsResult]{
		Name: "get_settings",
		Description: "The account's own Calendar settings: its time zone, which day the week starts on, and the " +
			"color palette. The time zone here is the last fallback for every other tool, so read it when you " +
			"need to know what \"today\" or \"9am\" means for this person.",
		Kind: Read,
		Handle: func(ctx context.Context, _ getSettingsIn) (service.SettingsResult, error) {
			return d.Service.SettingsSummary(ctx)
		},
	})
}

// Inputs. Flat structs, snake_case, no unions — a model fills in a flat
// schema reliably (the standard's §2).

type listCalendarsIn struct {
	IncludeHidden bool `json:"include_hidden,omitempty" jsonschema:"Include calendars hidden in the Calendar UI."`
}

type getCalendarIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar id, its title, or \"primary\" for the account's own calendar."`
}

type listEventsIn struct {
	Calendars []string `json:"calendars,omitempty" jsonschema:"Calendar ids or titles. Defaults to the account's primary calendar."`
	From      string   `json:"from" jsonschema:"Start of the window: yyyy-mm-dd or RFC3339. Required."`
	To        string   `json:"to" jsonschema:"End of the window: yyyy-mm-dd or RFC3339. Required. A bare date means the end of that day."`
	TimeZone  string   `json:"time_zone,omitempty" jsonschema:"IANA zone to read the window and show the times in, such as Europe/Copenhagen."`
	// Spelled as the negative so the default (expand) is the zero value.
	// A model that omits it gets occurrences, which is what "what is on
	// this week" means.
	NoExpand     bool   `json:"no_expand,omitempty" jsonschema:"Return repeating events once as a series with its rule, instead of as each occurrence."`
	ShowCanceled bool   `json:"show_canceled,omitempty" jsonschema:"Include canceled events, which are hidden by default."`
	MaxEvents    int    `json:"max_events,omitempty" jsonschema:"Cap on events returned. The server has its own budget and says when it truncated."`
	PageToken    string `json:"page_token,omitempty" jsonschema:"Continue a truncated read, from next_page_token."`
}

type searchEventsIn struct {
	Query     string   `json:"query" jsonschema:"Free text. Google decides which fields this matches; there is no field syntax."`
	Calendars []string `json:"calendars,omitempty" jsonschema:"Calendar ids or titles. Defaults to the primary calendar."`
	From      string   `json:"from" jsonschema:"Start of the window: yyyy-mm-dd or RFC3339. Required."`
	To        string   `json:"to" jsonschema:"End of the window: yyyy-mm-dd or RFC3339. Required."`
	TimeZone  string   `json:"time_zone,omitempty" jsonschema:"IANA zone to read the window and show the times in."`
	MaxEvents int      `json:"max_events,omitempty" jsonschema:"Cap on events returned."`
	PageToken string   `json:"page_token,omitempty" jsonschema:"Continue a truncated search, from next_page_token."`
}

type getEventIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar the event is on. An event id is unique per calendar, not globally."`
	EventID  string `json:"event_id" jsonschema:"The event id, from list_events or search_events."`
	TimeZone string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the times in."`
}

type listInstancesIn struct {
	Calendar     string `json:"calendar" jsonschema:"The calendar the series is on."`
	EventID      string `json:"event_id" jsonschema:"The repeating event's id. On an occurrence from list_events this is its series_id, not its own id."`
	From         string `json:"from,omitempty" jsonschema:"Start of the window: yyyy-mm-dd or RFC3339. Pass both from and to, or neither."`
	To           string `json:"to,omitempty" jsonschema:"End of the window: yyyy-mm-dd or RFC3339. Pass both from and to, or neither."`
	TimeZone     string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the occurrences in."`
	ShowCanceled bool   `json:"show_canceled,omitempty" jsonschema:"Include occurrences that were canceled, which is how single dates are removed from a series."`
	MaxEvents    int    `json:"max_events,omitempty" jsonschema:"Cap on occurrences returned. The server has its own budget and says when it truncated."`
	PageToken    string `json:"page_token,omitempty" jsonschema:"Continue a truncated read, from next_page_token."`
}

type listChangesIn struct {
	Calendar  string `json:"calendar" jsonschema:"The calendar to check for changes."`
	SyncToken string `json:"sync_token,omitempty" jsonschema:"A token from a previous call's sync_token. Leave it out the first time to get a baseline and a token. Opaque: never build or edit one."`
	PageToken string `json:"page_token,omitempty" jsonschema:"Continue a read that did not finish, from next_page_token. The sync token arrives with the last page."`
	TimeZone  string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the changed events in."`
	MaxEvents int    `json:"max_events,omitempty" jsonschema:"Cap on events returned. The server has its own budget and says when it truncated."`
}

type checkAvailabilityIn struct {
	Calendars   []string `json:"calendars,omitempty" jsonschema:"Calendar ids, email addresses or titles. Defaults to the account's primary calendar. An address works even for a calendar you cannot read."`
	From        string   `json:"from" jsonschema:"Start of the window: yyyy-mm-dd or RFC3339. Required."`
	To          string   `json:"to" jsonschema:"End of the window: yyyy-mm-dd or RFC3339. Required."`
	TimeZone    string   `json:"time_zone,omitempty" jsonschema:"IANA zone to read the window and show the times in."`
	MinMinutes  int      `json:"min_minutes,omitempty" jsonschema:"Ignore free gaps shorter than this many minutes."`
	WorkingFrom string   `json:"working_from,omitempty" jsonschema:"Start of the working day as hh:mm local, e.g. 09:00. Pass it with working_to. No default: without it the whole window counts."`
	WorkingTo   string   `json:"working_to,omitempty" jsonschema:"End of the working day as hh:mm local, e.g. 17:00. Must be later in the day than working_from; a shift crossing midnight is refused."`
	WorkingDays []string `json:"working_days,omitempty" jsonschema:"Weekdays to keep, as mon tue wed thu fri sat sun. Empty means every day; the server does not assume anybody's working week."`
}

type getSettingsIn struct{}
