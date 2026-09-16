package plan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// The calendar half of the plan package: what a calendar write changes,
// and the guards on it.
//
// One distinction runs through all of it, because the API splits across
// two resources what a person thinks of as one thing (§7.5). The
// CALENDAR is shared — its title, description, location and time zone
// are what everybody subscribed to it sees. The SUBSCRIPTION is this
// user's alone — the colour, the name they gave it, whether it is hidden
// and what they are emailed about. Writing the first when the second was
// meant renames a team's calendar for the team.

// CalendarDraft is a requested change to the calendar itself.
//
// Pointers for the same reason a Draft uses them: nil is "leave it", and
// a pointer to an empty string clears the field.
type CalendarDraft struct {
	Title       *string
	Description *string
	Location    *string
	// TimeZone is an IANA name, checked here rather than at Google: a
	// zone that does not exist will not start existing, so the refusal
	// is [invalid] and local.
	TimeZone *string
}

// Empty reports whether this draft asks for nothing.
func (d CalendarDraft) Empty() bool {
	return d.Title == nil && d.Description == nil && d.Location == nil && d.TimeZone == nil
}

// Patch builds the calendars.patch body and lists what it changes.
//
// A field whose new value equals the old one is not sent and not listed.
// The reason is the change list rather than the etag: a result that said
// it changed the title to the title it already had would be reporting a
// write that did not happen. (The etag argument this used to give was
// refuted live — Google left a calendar's etag alone when a patch wrote
// the value already there, §18 row 58.)
func (d CalendarDraft) Patch(before gcal.Calendar) (gcal.CalendarPatch, []Change, error) {
	var p gcal.CalendarPatch
	var changes []Change

	set := func(field, from, to string, dst **string) {
		setField(&changes, field, from, to, dst)
	}
	if d.Title != nil {
		if strings.TrimSpace(*d.Title) == "" {
			return gcal.CalendarPatch{}, nil, fmt.Errorf(
				"%w: a calendar cannot be given an empty title; pass the name it should have", ErrInvalid)
		}
		set("title", before.Summary, *d.Title, &p.Summary)
	}
	if d.Description != nil {
		set("description", before.Description, *d.Description, &p.Description)
	}
	if d.Location != nil {
		set("location", before.Location, *d.Location, &p.Location)
	}
	if d.TimeZone != nil {
		tz := strings.TrimSpace(*d.TimeZone)
		if _, err := when.LoadLocation(tz); err != nil {
			return gcal.CalendarPatch{}, nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		set("time_zone", before.TimeZone, tz, &p.TimeZone)
	}
	return p, changes, nil
}

// ListDraft is a requested change to THIS user's subscription. None of
// it is visible to anybody else.
type ListDraft struct {
	// MyName is summaryOverride: the name this user gives the calendar.
	// The calendar's own title is untouched, which is the difference
	// between this and CalendarDraft.Title.
	MyName  *string
	ColorID *string
	Hidden  *bool
	// Selected is whether its events are drawn in the Calendar UI, which
	// is not the same as hidden and confuses people (§7.1).
	Selected *bool
	// Notifications replaces the whole list, because that is what Google
	// does with the object. A non-nil pointer to an empty slice turns
	// every notification off.
	Notifications *[]string
}

// Empty reports whether this draft asks for nothing.
func (d ListDraft) Empty() bool {
	return d.MyName == nil && d.ColorID == nil && d.Hidden == nil &&
		d.Selected == nil && d.Notifications == nil
}

// Patch builds the calendarList.patch body and lists what it changes.
func (d ListDraft) Patch(before gcal.CalendarListEntry) (gcal.CalendarListPatch, []Change, error) {
	var p gcal.CalendarListPatch
	var changes []Change

	if d.MyName != nil {
		setField(&changes, "my_name", before.SummaryOverride, *d.MyName, &p.SummaryOverride)
	}
	if d.ColorID != nil {
		to := strings.TrimSpace(*d.ColorID)
		if to == "" {
			return gcal.CalendarListPatch{}, nil, fmt.Errorf(
				"%w: color_id cannot be emptied — a calendar always has a colour. Pass one of the "+
					"calendar colour ids get_settings reports", ErrInvalid)
		}
		setField(&changes, "color_id", before.ColorID, to, &p.ColorID)
	}
	if d.Hidden != nil {
		setFlag(&changes, "hidden", before.Hidden, *d.Hidden, &p.Hidden)
	}
	if d.Selected != nil {
		setFlag(&changes, "selected", before.Selected, *d.Selected, &p.Selected)
	}
	if d.Notifications != nil {
		want, err := notifications(*d.Notifications)
		if err != nil {
			return gcal.CalendarListPatch{}, nil, err
		}
		from := currentNotifications(before)
		if strings.Join(from, ",") != strings.Join(want, ",") {
			settings := &gcal.NotificationSettings{Notifications: []gcal.CalendarNotification{}}
			for _, t := range want {
				settings.Notifications = append(settings.Notifications,
					gcal.CalendarNotification{Type: t, Method: gcal.NotificationMethodEmail})
			}
			p.NotificationSettings = settings
			changes = append(changes, Change{
				Field: "notifications", From: listOrNone(from), To: listOrNone(want),
			})
		}
	}
	return p, changes, nil
}

// setField and setFlag apply one field of a patch: nothing is sent and
// nothing is listed when the value is already what was asked for, so the
// change list says what the write actually did (§18 row 58).
//
// Both drafts go through them, which is what keeps the change list and
// the request body describing the same write. The four copies this
// replaced each decided separately what to report as the "before", and
// three of them were the same by luck rather than by construction.
func setField(changes *[]Change, field, from, to string, dst **string) {
	if from == to {
		return
	}
	v := to
	*dst = &v
	*changes = append(*changes, Change{Field: field, From: from, To: to})
}

func setFlag(changes *[]Change, field string, from, to bool, dst **bool) {
	if from == to {
		return
	}
	v := to
	*dst = &v
	*changes = append(*changes, Change{Field: field, From: yesNo(from), To: yesNo(to)})
}

// notifications validates the requested types and puts them in the
// published order, so two callers asking for the same set produce the
// same request and the same "changed" line.
func notifications(want []string) ([]string, error) {
	seen := map[string]bool{}
	for _, t := range want {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		match := ""
		for _, known := range gcal.NotificationTypes {
			// Google's own camel-case spelling and a plainer one both
			// work: a model that read the API reference types the first,
			// and one that read this server's description types the
			// second.
			if strings.EqualFold(t, known) || strings.EqualFold(t, plainNotification(known)) {
				match = known
				break
			}
		}
		if match == "" {
			return nil, fmt.Errorf("%w: %q is not a notification type. Pass any of:%s",
				ErrInvalid, t, NotificationChoices())
		}
		seen[match] = true
	}
	var out []string
	for _, known := range gcal.NotificationTypes {
		if seen[known] {
			out = append(out, known)
		}
	}
	return out, nil
}

// plainNotification is the spelling this server's own descriptions use.
func plainNotification(t string) string {
	switch t {
	case gcal.NotifyEventCreation:
		return "creation"
	case gcal.NotifyEventChange:
		return "change"
	case gcal.NotifyEventCancellation:
		return "cancellation"
	case gcal.NotifyEventResponse:
		return "response"
	case gcal.NotifyAgenda:
		return "agenda"
	default:
		return t
	}
}

// NotificationChoices is the vocabulary and what each one means, for a
// refusal and for the tool description.
func NotificationChoices() string {
	var b strings.Builder
	for _, t := range gcal.NotificationTypes {
		fmt.Fprintf(&b, "\n  %s (%s) — emailed when %s", plainNotification(t), t, gcal.NotificationMeans(t))
	}
	return b.String()
}

// currentNotifications is the types this user has now, in the published
// order.
func currentNotifications(e gcal.CalendarListEntry) []string {
	if e.NotificationSettings == nil {
		return nil
	}
	have := map[string]bool{}
	for _, n := range e.NotificationSettings.Notifications {
		have[n.Type] = true
	}
	var out []string
	for _, known := range gcal.NotificationTypes {
		if have[known] {
			out = append(out, known)
		}
	}
	// A type Google adds later is carried through rather than dropped
	// from the "before" side, so a change list cannot claim this write
	// removed something it never saw.
	var extra []string
	for t := range have {
		if !slices.Contains(gcal.NotificationTypes, t) {
			extra = append(extra, t)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func listOrNone(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return strings.Join(v, ", ")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// Confirm holds the destructive tools to their explicit confirmation
// (§9). The flag that registers them is a deployment decision; this is
// the call's own, and both have to be made.
func Confirm(ok bool, what string) error {
	if ok {
		return nil
	}
	return fmt.Errorf("%w: %s, and Calendar cannot undo it. Pass confirm:true on this call if that is "+
		"what you mean", ErrBlocked, what)
}
