package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/internal/service"
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
			"Cancelled events are hidden unless show_cancelled is set. " +
			"Use search_events to find an event by text; use check_availability to find free time, because a list " +
			"of events is not the same as being free.",
		Kind: Read,
		Handle: func(ctx context.Context, in listEventsIn) (service.ScheduleResult, error) {
			sched, err := d.Service.ListEvents(ctx, service.ListOptions{
				Calendars: in.Calendars, TimeZone: in.TimeZone,
				From: in.From, To: in.To,
				Expand:        !in.NoExpand,
				ShowCancelled: in.ShowCancelled,
				MaxEvents:     in.MaxEvents, PageToken: in.PageToken,
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
				MaxEvents: in.MaxEvents,
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

	add(s, d, Def[getSettingsIn, service.SettingsResult]{
		Name: "get_settings",
		Description: "The account's own Calendar settings: its time zone, which day the week starts on, and the " +
			"colour palette. The time zone here is the last fallback for every other tool, so read it when you " +
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
	NoExpand      bool   `json:"no_expand,omitempty" jsonschema:"Return repeating events once as a series with its rule, instead of as each occurrence."`
	ShowCancelled bool   `json:"show_cancelled,omitempty" jsonschema:"Include cancelled events, which are hidden by default."`
	MaxEvents     int    `json:"max_events,omitempty" jsonschema:"Cap on events returned. The server has its own budget and says when it truncated."`
	PageToken     string `json:"page_token,omitempty" jsonschema:"Continue a truncated read, from next_page_token."`
}

type searchEventsIn struct {
	Query     string   `json:"query" jsonschema:"Free text. Google decides which fields this matches; there is no field syntax."`
	Calendars []string `json:"calendars,omitempty" jsonschema:"Calendar ids or titles. Defaults to the primary calendar."`
	From      string   `json:"from" jsonschema:"Start of the window: yyyy-mm-dd or RFC3339. Required."`
	To        string   `json:"to" jsonschema:"End of the window: yyyy-mm-dd or RFC3339. Required."`
	TimeZone  string   `json:"time_zone,omitempty" jsonschema:"IANA zone to read the window and show the times in."`
	MaxEvents int      `json:"max_events,omitempty" jsonschema:"Cap on events returned."`
}

type getEventIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar the event is on. An event id is unique per calendar, not globally."`
	EventID  string `json:"event_id" jsonschema:"The event id, from list_events or search_events."`
	TimeZone string `json:"time_zone,omitempty" jsonschema:"IANA zone to show the times in."`
}

type getSettingsIn struct{}
