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
// type change after it is made; a patch or a split of one is held to the
// same rules by statusGuests and statusPatch.
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

// eventType is the status type asked for, "" for an ordinary event.
func (s Status) eventType() (string, error) {
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
	kind, err := s.eventType()
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
	if err := d.statusGuests(kind); err != nil {
		return err
	}
	// Held as the caller sent it, before the server sets the busy status
	// and visibility Google requires: a value the caller gave that Google
	// refuses is refused rather than overridden.
	e.EventType = kind
	if err := shape(*e); err != nil {
		return err
	}
	if kind == gcal.EventTypeWorkingLocation {
		return s.working(e)
	}
	return s.away(e, kind, what)
}

// statusGuests refuses guests, rooms and a Meet link on a status event of
// eventType, new or existing, at any scope and on a split's new series.
// Google documents nothing about them there, so nothing is sent until a
// live probe shows what it does (§18 row 99). Removing a guest is not
// refused. Any other type passes.
func (d Draft) statusGuests(eventType string) error {
	what, ok := statusNames[eventType]
	if !ok {
		return nil
	}
	if len(d.Invites()) > 0 || d.Conference {
		return fmt.Errorf("%w: this server does not put guests, rooms or a Meet link on %s: what Google "+
			"does with them there is not yet checked. Create an ordinary event for a meeting", ErrBlocked, what)
	}
	return nil
}

// shape refuses a status event as it would stand after a write, in a
// shape Google's guide refuses: made by create_event, or changed by a
// patch or a split. It reads the event, not the call, so a split's new
// series is held to the same rules as a new event. An event of any other
// type passes.
func shape(e gcal.Event) error {
	what, ok := statusNames[e.EventType]
	if !ok {
		return nil
	}
	allDay := e.Start != nil && e.Start.IsAllDay()
	switch e.EventType {
	case gcal.EventTypeOutOfOffice, gcal.EventTypeFocusTime:
		// "Out of office events cannot be all-day events", and "Focus
		// times cannot be all-day events."
		if allDay {
			return fmt.Errorf("%w: %s cannot be all day; Google refuses one. Pass start and end as times, "+
				"such as the whole working day", ErrUnsupported, what)
		}
		// Both need transparency opaque.
		if e.Transparency == gcal.TransparencyTransparent {
			return fmt.Errorf("%w: %s always shows you as busy; Google requires it. Leave out free_not_busy",
				ErrInvalid, what)
		}
	case gcal.EventTypeWorkingLocation:
		// "An all-day event (with start and end dates specified) which
		// spans exactly one day." Google's end is the day after.
		if allDay && e.End != nil {
			from, ferr := when.ParseDate(e.Start.Date)
			to, terr := when.ParseDate(e.End.Date)
			if ferr == nil && terr == nil && to != from.AddDays(1) {
				return fmt.Errorf("%w: an all-day working location covers exactly one day; Google refuses more. "+
					"Pass the same date as start and end, or pass times, which may span days", ErrUnsupported)
			}
		}
		// It needs visibility public and transparency transparent.
		if e.Visibility != "" && e.Visibility != gcal.VisibilityPublic {
			return fmt.Errorf("%w: a working location is always public; Google requires it. Leave out "+
				"visibility", ErrInvalid)
		}
		if e.Transparency == gcal.TransparencyOpaque {
			return fmt.Errorf("%w: a working location always shows you free; Google requires it. Leave out "+
				"free_not_busy", ErrInvalid)
		}
	}
	return nil
}

// statusPatch holds a patch of a status event to the shapes a new one is
// held to, since Google says an update "must maintain the required
// fields". A rule the event already broke when it was read is left to
// Google, so a write that does not cause the break is not refused for
// it.
func statusPatch(before gcal.Event, p gcal.EventPatch) error {
	if _, ok := statusNames[before.EventType]; !ok {
		return nil
	}
	after := before
	p.ApplyTo(&after)
	if err := shape(after); err != nil && shape(before) == nil {
		return err
	}
	return nil
}

// away fills in an out-of-office or focus-time event.
func (s Status) away(e *gcal.Event, kind, what string) error {
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
func (s Status) working(e *gcal.Event) error {
	switch {
	case s.AutoDecline != "" || s.DeclineMessage != "":
		return fmt.Errorf("%w: a working location declines nothing, so auto_decline and decline_message "+
			"do not go with it", ErrInvalid)
	case s.ChatStatus != "":
		return fmt.Errorf("%w: chat_status belongs to focus time, not to a working location", ErrInvalid)
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
