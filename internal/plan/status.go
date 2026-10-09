package plan

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/model"
	"github.com/mmedum/google-calendar-mcp/v3/internal/when"
)

// Status asks for a status event (§7.4): out of office, focus time or a
// working location. Insert only, because Google does not let an event's
// type change after it is made.
//
// The fields are the caller's words. The rules they are held to are
// Google's, from its status-events guide, and the guards on what Google
// does not document are this server's (§18 row 99).
type Status struct {
	// Type is outOfOffice, focusTime or workingLocation, spelled as a
	// read reports it. Empty or default is an ordinary event.
	Type string
	// AutoDecline is none, new or all. Required on out of office and
	// focus time, with no default: a decline reaches the organizer of
	// every meeting it touches, so it is a choice like notify (§4.3).
	AutoDecline string
	// DeclineMessage goes with each decline.
	DeclineMessage string
	// ChatStatus is available or do_not_disturb, on focus time only.
	ChatStatus string
	// WorkingLocation is home, office or custom, and Label names the
	// office or the place.
	WorkingLocation string
	Label           string
	// OnPrimary is whether the event goes on the account's primary
	// calendar, the only one that can hold a status event.
	OnPrimary bool
}

// statusTypes are the types create_event makes, by the caller's word in
// any case. Default is an ordinary event.
var statusTypes = map[string]string{
	strings.ToLower(gcal.EventTypeOutOfOffice):     gcal.EventTypeOutOfOffice,
	strings.ToLower(gcal.EventTypeFocusTime):       gcal.EventTypeFocusTime,
	strings.ToLower(gcal.EventTypeWorkingLocation): gcal.EventTypeWorkingLocation,
	gcal.EventTypeDefault:                          "",
}

// statusNames are the types as a person says them.
var statusNames = map[string]string{
	gcal.EventTypeOutOfOffice:     "an out-of-office event",
	gcal.EventTypeFocusTime:       "focus time",
	gcal.EventTypeWorkingLocation: "a working location",
}

// EventType is the status type asked for, "" for an ordinary event.
func (s Status) EventType() (string, error) {
	v := strings.TrimSpace(s.Type)
	if v == "" {
		return "", nil
	}
	t, ok := statusTypes[strings.ToLower(v)]
	if !ok {
		return "", fmt.Errorf("%w: %q is not an event type create_event makes. Pass outOfOffice, focusTime or "+
			"workingLocation, or leave event_type out for an ordinary event", ErrInvalid, v)
	}
	return t, nil
}

// apply makes e the status event s asks for, or refuses it. e is the
// event Insert built, and d the draft it came from.
func (s Status) apply(e *gcal.Event, d Draft) error {
	kind, err := s.EventType()
	if err != nil {
		return err
	}
	if kind == "" {
		if name := s.firstGiven(); name != "" {
			return fmt.Errorf("%w: %s belongs to a status event. Pass event_type outOfOffice, focusTime or "+
				"workingLocation, or leave it out", ErrInvalid, name)
		}
		return nil
	}
	what := statusNames[kind]
	// "Secondary calendars can't have status events."
	if !s.OnPrimary {
		return fmt.Errorf("%w: %s can only go on your primary calendar; Google does not allow one on any "+
			"other. Leave calendar out, or pass primary", ErrUnsupported, what)
	}
	// Not documented either way, so not sent until a live probe shows
	// what Google does with them (§18 row 99).
	if len(d.Invites()) > 0 || d.Conference {
		return fmt.Errorf("%w: this server does not put guests, rooms or a Meet link on %s: what Google "+
			"does with them there is not yet checked. Create an ordinary event for a meeting", ErrBlocked, what)
	}
	allDay := e.Start != nil && e.Start.IsAllDay()
	switch kind {
	case gcal.EventTypeOutOfOffice, gcal.EventTypeFocusTime:
		err = s.away(e, d, kind, what, allDay)
	default:
		err = s.working(e, d, allDay)
	}
	if err != nil {
		return err
	}
	e.EventType = kind
	return nil
}

// away fills in an out-of-office or focus-time event.
func (s Status) away(e *gcal.Event, d Draft, kind, what string, allDay bool) error {
	// "Out of office events cannot be all-day events", and "Focus
	// times cannot be all-day events."
	if allDay {
		return fmt.Errorf("%w: %s cannot be all day; Google refuses one. Pass start and end as times, "+
			"such as the whole working day", ErrUnsupported, what)
	}
	// Both need transparency opaque.
	if d.Transparent != nil && *d.Transparent {
		return fmt.Errorf("%w: %s always shows you as busy; Google requires it. Leave out free_not_busy",
			ErrInvalid, what)
	}
	if s.WorkingLocation != "" || s.Label != "" {
		return fmt.Errorf("%w: working_location and working_location_label belong to a working location, "+
			"not to %s", ErrInvalid, what)
	}
	if strings.TrimSpace(s.AutoDecline) == "" {
		return fmt.Errorf("%w: auto_decline is required for %s, and there is no default. none declines "+
			"nothing; new declines invitations that arrive for this time while it stands; all also declines "+
			"the meetings you already accepted. Each organizer sees the decline",
			ErrInvalid, what)
	}
	mode, ok := model.AutoDeclineWords.Wire(strings.TrimSpace(s.AutoDecline))
	if !ok {
		return fmt.Errorf("%w: %q is not an auto_decline. Pass %s", ErrInvalid, s.AutoDecline,
			model.AutoDeclineWords.List())
	}
	if s.DeclineMessage != "" && mode == gcal.AutoDeclineNone {
		return fmt.Errorf("%w: decline_message goes with a decline, and auto_decline none declines nothing. "+
			"Leave it out, or pass new or all", ErrInvalid)
	}
	e.Transparency = gcal.TransparencyOpaque
	if kind == gcal.EventTypeOutOfOffice {
		if s.ChatStatus != "" {
			return fmt.Errorf("%w: chat_status belongs to focus time, not to %s", ErrInvalid, what)
		}
		e.OutOfOfficeProperties = gcal.Raw(gcal.EventOutOfOfficeProperties{
			AutoDeclineMode: mode, DeclineMessage: s.DeclineMessage,
		})
		return nil
	}
	chat := ""
	if v := strings.TrimSpace(s.ChatStatus); v != "" {
		if chat, ok = model.ChatStatusWords.Wire(v); !ok {
			return fmt.Errorf("%w: %q is not a chat_status. Pass %s", ErrInvalid, s.ChatStatus,
				model.ChatStatusWords.List())
		}
	}
	e.FocusTimeProperties = gcal.Raw(gcal.EventFocusTimeProperties{
		AutoDeclineMode: mode, ChatStatus: chat, DeclineMessage: s.DeclineMessage,
	})
	return nil
}

// working fills in a working-location event.
func (s Status) working(e *gcal.Event, d Draft, allDay bool) error {
	switch {
	case s.AutoDecline != "" || s.DeclineMessage != "":
		return fmt.Errorf("%w: a working location declines nothing, so auto_decline and decline_message "+
			"do not go with it", ErrInvalid)
	case s.ChatStatus != "":
		return fmt.Errorf("%w: chat_status belongs to focus time, not to a working location", ErrInvalid)
	}
	// "An all-day event (with start and end dates specified) which spans
	// exactly one day." Google's end is the day after.
	if allDay && e.End != nil {
		from, ferr := when.ParseDate(e.Start.Date)
		to, terr := when.ParseDate(e.End.Date)
		if ferr == nil && terr == nil && to != from.AddDays(1) {
			return fmt.Errorf("%w: an all-day working location covers exactly one day; Google refuses more. "+
				"Pass the same date as start and end, or pass times, which may span days", ErrUnsupported)
		}
	}
	// It needs visibility public and transparency transparent.
	if d.Visibility != nil && strings.ToLower(strings.TrimSpace(*d.Visibility)) != gcal.VisibilityPublic {
		return fmt.Errorf("%w: a working location is always public; Google requires it. Leave out "+
			"visibility", ErrInvalid)
	}
	v := strings.TrimSpace(s.WorkingLocation)
	if v == "" {
		return fmt.Errorf("%w: working_location is required for a working location: %s",
			ErrInvalid, model.WorkingLocationWords.List())
	}
	kind, ok := model.WorkingLocationWords.Wire(v)
	if !ok {
		return fmt.Errorf("%w: %q is not a working_location. Pass %s", ErrInvalid, s.WorkingLocation,
			model.WorkingLocationWords.List())
	}
	p := gcal.EventWorkingLocationProperties{Type: kind}
	switch kind {
	case gcal.WorkingHome:
		if s.Label != "" {
			return fmt.Errorf("%w: home has no label. Leave out working_location_label, or pass office or "+
				"custom", ErrInvalid)
		}
		p.HomeOffice = json.RawMessage(`{}`)
	case gcal.WorkingOffice:
		p.OfficeLocation = &gcal.WorkingLocationOffice{Label: s.Label}
	default:
		p.CustomLocation = &gcal.WorkingLocationCustom{Label: s.Label}
	}
	e.Transparency, e.Visibility = gcal.TransparencyTransparent, gcal.VisibilityPublic
	e.WorkingLocationProperties = gcal.Raw(p)
	return nil
}

// firstGiven names the first status input given, for the refusal of one
// without a status type.
func (s Status) firstGiven() string {
	for _, f := range []struct{ name, v string }{
		{"auto_decline", s.AutoDecline}, {"decline_message", s.DeclineMessage},
		{"chat_status", s.ChatStatus}, {"working_location", s.WorkingLocation},
		{"working_location_label", s.Label},
	} {
		if f.v != "" {
			return f.name
		}
	}
	return ""
}
