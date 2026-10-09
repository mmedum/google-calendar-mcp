//go:build live

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-calendar-mcp/v3/internal/redact"
)

// The one write this driver makes on the account's primary calendar.
//
// Only the primary calendar can hold a status event, and §9.1 keeps the
// driver off it. On 2026-10-09 the owner allowed one narrow exception:
// one status event per run, about a year ahead, with an invented title,
// declining nothing, and deleted by its id in the same run. primaryGuard
// is what holds the driver to that, on both of its paths to Google: the
// server's tools and its own REST calls. Every write naming the primary
// calendar is refused except that one create and the delete of the id it
// made.

// statusTitle is the invented title of the one status event.
const statusTitle = "Livecal status probe"

// statusKind is which status type this run makes, from -status-type. One
// per run, so the three shapes are checked over three runs.
var statusKind = "outOfOffice"

// primaryCal guards every write the driver makes.
var primaryCal = &primaryGuard{}

// primaryGuard refuses every write on the primary calendar but the one
// §9.1 allows.
type primaryGuard struct {
	mu sync.Mutex
	// self is the account's address, which is its primary calendar's id.
	self string
	// asked is set once the one status event has been asked for; a
	// second create on the primary calendar is refused.
	asked bool
	// made is the status event's id, the only one the cleanup may delete.
	made string
}

// readTools change nothing. §9.1's print rule decides what of theirs may
// be shown.
var readTools = map[string]bool{
	"list_calendars": true, "get_calendar": true, "list_events": true, "get_event": true,
	"list_instances": true, "list_changes": true, "search_events": true, "check_availability": true,
	"get_settings": true, "list_sharing": true,
}

// calendarArgs are the arguments each write names a calendar in. A write
// not listed here is refused, so a tool added later is held until
// somebody says where it writes.
var calendarArgs = map[string][]string{
	"create_event": {"calendar"}, "update_event": {"calendar"}, "cancel_event": {"calendar"},
	"move_event": {"calendar", "to_calendar"}, "respond_to_event": {"calendar"},
	"create_calendar": nil, "manage_calendar": {"calendar"}, "share_calendar": {"calendar"},
	"unshare_calendar": {"calendar"}, "delete_calendar": {"calendar"}, "clear_calendar": {"calendar"},
}

// setSelf records the account's address, once it has been read.
func (g *primaryGuard) setSelf(self string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.self = self
}

// names reports whether a calendar reference reaches the primary
// calendar. A missing or empty one does, because the server resolves it
// to the primary; anything but a string is taken as one, so an argument
// shape this does not know fails closed.
func (g *primaryGuard) names(v any) bool {
	s, ok := v.(string)
	if !ok {
		return true
	}
	s = strings.TrimSpace(s)
	return s == "" || strings.EqualFold(s, "primary") || (g.self != "" && strings.EqualFold(s, g.self))
}

// tool decides whether a tool call may go out.
func (g *primaryGuard) tool(name string, args map[string]any) error {
	if readTools[name] {
		return nil
	}
	keys, known := calendarArgs[name]
	if !known {
		return fmt.Errorf("the driver does not know which calendar %s writes, so it does not call it (§9.1)", name)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	onPrimary := false
	for _, k := range keys {
		if g.names(args[k]) {
			onPrimary = true
		}
	}
	if !onPrimary {
		return nil
	}
	if name == "create_event" && !g.asked && theStatusEvent(args) {
		g.asked = true
		return nil
	}
	return fmt.Errorf("%s on the primary calendar was not called: the driver writes nothing there but "+
		"one status event that declines nothing (§9.1)", name)
}

// theStatusEvent reports whether create_event arguments are the one
// status event §9.1 allows: the invented title, a status type, declining
// nothing, and nobody and nothing else on it.
func theStatusEvent(args map[string]any) bool {
	decline, _ := args["auto_decline"].(string)
	for _, k := range []string{"dry_run", "guests", "optional_guests", "rooms", "conference", "recurrence"} {
		if args[k] != nil {
			return false
		}
	}
	return args["title"] == statusTitle && args["event_type"] != nil && (decline == "" || decline == "none")
}

// rest decides whether one of the driver's own REST calls may go out.
func (g *primaryGuard) rest(method, path string) error {
	if method == http.MethodGet {
		return nil
	}
	cal, rest := "", ""
	switch {
	case strings.HasPrefix(path, "/calendars/"):
		cal, rest, _ = strings.Cut(strings.TrimPrefix(path, "/calendars/"), "/")
	case strings.HasPrefix(path, "/users/me/calendarList/"):
		cal, rest, _ = strings.Cut(strings.TrimPrefix(path, "/users/me/calendarList/"), "/")
	default:
		// A new calendar, or a free/busy query: neither is on the
		// primary calendar.
		return nil
	}
	cal, _, _ = strings.Cut(cal, "?")
	if unescaped, err := url.PathUnescape(cal); err == nil {
		cal = unescaped
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.names(cal) {
		return nil
	}
	event, _, _ := strings.Cut(strings.TrimPrefix(rest, "events/"), "?")
	if method == http.MethodDelete && g.made != "" && strings.HasPrefix(rest, "events/") && event == g.made {
		return nil
	}
	return fmt.Errorf("%s on the primary calendar was not sent: the driver deletes nothing there but the "+
		"status event it made (§9.1)", method)
}

// remember records the status event's id, from a create result or from an
// ambiguous one that names the id it used.
func (g *primaryGuard) remember(text string) string {
	id := field(text, "id: ")
	if id == "" {
		if _, after, ok := strings.Cut(text, "It used the id "); ok {
			if f := strings.Fields(after); len(f) > 0 {
				id = f[0]
			}
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if id != "" {
		g.made = id
	}
	return g.made
}

// madeID is the status event's id, empty until it is known.
func (g *primaryGuard) madeID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.made
}

// cleanUp deletes the status event by its id. It is deferred before the
// steps run, so it runs however the run ends. Its output names no title
// or calendar: the primary calendar's title is the person's.
func (g *primaryGuard) cleanUp(api *liveAPI, out *redact.Printer) {
	g.mu.Lock()
	asked, id := g.asked, g.made
	g.mu.Unlock()
	switch {
	case !asked:
		return
	case id == "":
		out.Printf("\nWARNING: the status event on the primary calendar was asked for and its id is not known.\n")
		out.Printf("Look on your primary calendar around %s for %q and delete it by hand.\n",
			statusDay(), statusTitle)
		return
	}
	err := api.do(context.Background(), http.MethodDelete,
		"/calendars/primary/events/"+id+"?sendUpdates=none", nil, nil)
	if err != nil && !strings.Contains(err.Error(), "returned 410") && !strings.Contains(err.Error(), "returned 404") {
		out.Printf("\nWARNING: could not delete the status event %s from the primary calendar: %v\n",
			redact.ID(id), redact.String(err.Error()))
		out.Printf("Delete %q on %s by hand; this driver must leave nothing behind.\n", statusTitle, statusDay())
		return
	}
	out.Printf("\nthe status event on the primary calendar was deleted by its id\n")
}

// statusStart is when the status event starts: about a year ahead, at
// 10:00 in the scratch zone, so it is in nobody's way and its date is
// computed, not chosen.
func statusStart() time.Time {
	loc, err := time.LoadLocation(scratchZone)
	if err != nil {
		loc = time.UTC
	}
	d := time.Now().In(loc).AddDate(1, 0, 0)
	return time.Date(d.Year(), d.Month(), d.Day(), 10, 0, 0, 0, loc)
}

// statusDay is the status event's date.
func statusDay() string { return statusStart().Format("2006-01-02") }

// statusArgs is the create_event call for this run's status event, on
// cal. Nothing it carries declines anything.
func statusArgs(cal string) map[string]any {
	start := statusStart()
	args := map[string]any{
		"calendar": cal, "title": statusTitle, "event_type": statusKind,
		"start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339),
	}
	switch statusKind {
	case "workingLocation":
		args["working_location"], args["working_location_label"] = "custom", "Livecal probe place"
	case "focusTime":
		args["auto_decline"], args["chat_status"] = "none", "available"
	default:
		args["auto_decline"] = "none"
	}
	return args
}

// statusTag is the tag a read of this run's status event carries.
func statusTag() string {
	switch statusKind {
	case "workingLocation":
		return "working from Livecal probe place"
	case "focusTime":
		return "focus time"
	default:
		return "out of office"
	}
}

// statusSteps make the one status event, read it back, and check that a
// secondary calendar is refused one. The primary steps' notes are fixed
// sentences: a result names the calendar, and the primary calendar's
// title is the person's own, which no redactor can recognize.
func statusSteps(scratch string) []step {
	return []step{
		{
			name: "status event on a secondary",
			tool: "create_event",
			args: statusArgs(scratch),
			check: func(r callResult) (verdict, string) {
				if !r.isError || !strings.Contains(r.text, "[unsupported]") {
					return fail, "a status event on a secondary calendar was not refused as unsupported: " +
						truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "primary calendar") {
					return fail, "the refusal does not say a status event goes on the primary calendar"
				}
				return pass, "refused before a request, naming the primary calendar"
			},
		},
		{
			name: "status event (§9.1 exception)",
			tool: "create_event",
			args: statusArgs("primary"),
			check: func(r callResult) (verdict, string) {
				id := primaryCal.remember(r.text)
				switch {
				case r.isError:
					return fail, "Google or the server refused the status event; the body is withheld (§9.1)"
				case id == "":
					return fail, "the result names no id, so the cleanup cannot delete it by id"
				case !strings.Contains(r.text, statusTag()):
					return fail, "the result does not carry the " + statusKind + " details back"
				}
				return pass, statusKind + " created a year ahead, declining nothing; deleted by id at the end"
			},
		},
		{
			name: "status event reads back",
			tool: "get_event",
			argsFn: func() map[string]any {
				return map[string]any{"calendar": "primary", "event_id": primaryCal.madeID()}
			},
			skip: func() string {
				if primaryCal.madeID() == "" {
					return "no status event was made"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "the status event could not be read back; the body is withheld (§9.1)"
				}
				if !strings.Contains(r.text, statusTag()) {
					return fail, "the read does not carry the " + statusKind + " details"
				}
				return pass, "read back with its details"
			},
		},
	}
}

// spikeP — does Google refuse a status event on a secondary calendar?
//
// The guide says "Secondary calendars can't have status events", and
// create_event refuses one before a request, so no tool call can tell
// whether Google agrees. This asks the API directly, on the scratch
// calendar, with an out-of-office event that declines nothing. An event
// Google accepts is left on the scratch calendar, which the next run
// empties (§18 row 99).
func spikeP(ctx context.Context, _ *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if scratch == "" {
		return undetermined, "no scratch calendar this run"
	}
	start := statusStart()
	err := api.insertEvent(ctx, scratch, map[string]any{
		"summary": statusTitle, "eventType": "outOfOffice",
		"outOfOfficeProperties": map[string]any{"autoDeclineMode": "declineNone"},
		"start":                 map[string]any{"dateTime": start.Format(time.RFC3339), "timeZone": scratchZone},
		"end":                   map[string]any{"dateTime": start.Add(time.Hour).Format(time.RFC3339), "timeZone": scratchZone},
	})
	switch {
	case err == nil:
		return fail, "Google ACCEPTED a status event on a secondary calendar, so create_event's refusal is " +
			"stricter than Google; record it in §18 row 99"
	case strings.Contains(err.Error(), "returned 4"):
		return pass, "Google refuses it: " + firstLine(truncate(redact.String(err.Error()), 200))
	default:
		return undetermined, "the request failed without an answer: " + truncate(redact.String(err.Error()), 200)
	}
}
