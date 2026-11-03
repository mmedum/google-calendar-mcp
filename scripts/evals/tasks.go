//go:build evals

package main

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v5/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
)

// task is one thing a model has to get right.
//
// The score reads the CALENDAR, never the model's account of what it
// did. A model that says "I've created the all-day event" and wrote a
// timestamp has failed the task, and nobody notices until a user in Los
// Angeles sees the wrong day.
type task struct {
	name   string
	system string
	prompt string
	seed   func() *caltest.Server
	score  func(*caltest.Server) (bool, string)
}

// The account the fake calendar belongs to. Its domain is what makes a
// guest internal or external, and both are reserved by RFC 2606
// so neither can resolve.
const (
	selfAddress  = "me@example.test"
	outsideGuest = "partner@elsewhere.test"
)

func tasks() []task {
	return []task{allDayWestOfUTC(), weeklyAcrossDST(), invitationReachesGuest()}
}

// allDayWestOfUTC scores an all-day event created by a user west of UTC,
// which a server that stores midnight somewhere else renders a day
// early.
//
// The model is told the zone the way a user would say it, and the task
// passes only if what lands on the calendar is a DATE. A dateTime here
// is the defect, even when the day looks right in the model's own
// summary.
func allDayWestOfUTC() task {
	return task{
		name: "all-day west of UTC",
		system: "You are managing the Google Calendar of somebody who lives in Los Angeles. " +
			"Today is Monday 9 March 2026. Use the tools to do what they ask, and stop when it is done.",
		prompt: "Block out Saturday 14 March 2026 as \"Team offsite\" — it runs the whole day, " +
			"there are no start and end times.",
		seed: func() *caltest.Server {
			s := caltest.New()
			s.AddCalendar(selfAddress, "Mine", "America/Los_Angeles", gcal.RoleOwner, true)
			return s
		},
		score: func(s *caltest.Server) (bool, string) {
			e, ok := findEvent(s, "offsite")
			if !ok {
				return false, "no event with that title was created"
			}
			if e.Start == nil || e.Start.Date == "" {
				return false, "the event was written as an instant rather than a date"
			}
			if e.Start.DateTime != "" {
				return false, "the start carries both a date and a time"
			}
			if e.Start.Date != "2026-03-14" {
				return false, fmt.Sprintf("the event is on %s, not 2026-03-14", e.Start.Date)
			}
			// Google's end date is exclusive, and the tool takes the
			// LAST day. A one-day event must not end on the 16th.
			if e.End != nil && e.End.Date != "" && e.End.Date != "2026-03-15" {
				return false, fmt.Sprintf("a one-day event ends on %s, so it covers more than the day asked for",
					e.End.Date)
			}
			return true, "a date, on the right day, one day long"
		},
	}
}

// weeklyAcrossDST scores a weekly 09:00 that must not drift to 08:00
// after the clocks change, as a series written as a UTC instant with no
// zone does.
//
// Google refuses a recurrence with no zone outright, so the failure this
// scores is subtler: the model has to give a zone, and the wall clock it
// gives has to be the one the user said.
func weeklyAcrossDST() task {
	return task{
		name: "weekly across a DST change",
		system: "You are managing the Google Calendar of somebody who lives in Copenhagen. " +
			"Today is Monday 9 March 2026. Use the tools to do what they ask, and stop when it is done.",
		prompt: "Set up our weekly team sync: every Tuesday at 09:00 for the next six weeks, " +
			"starting Tuesday 17 March 2026. An hour each time.",
		seed: func() *caltest.Server {
			s := caltest.New()
			s.AddCalendar(selfAddress, "Mine", "Europe/Copenhagen", gcal.RoleOwner, true)
			return s
		},
		score: func(s *caltest.Server) (bool, string) {
			e, ok := findEvent(s, "sync")
			if !ok {
				return false, "no weekly event was created"
			}
			if len(e.Recurrence) == 0 {
				return false, "the event does not repeat: six separate events are not a series"
			}
			rule := strings.ToUpper(strings.Join(e.Recurrence, " "))
			if !strings.Contains(rule, "FREQ=WEEKLY") {
				return false, "the recurrence is not weekly: " + strings.Join(e.Recurrence, " ")
			}
			if e.Start == nil || e.Start.DateTime == "" {
				return false, "a timed series was written without a time"
			}
			// The zone is the whole point. An offset is not a zone: it
			// places one instant and cannot carry a wall clock across a
			// transition.
			if e.Start.TimeZone == "" {
				return false, "the series carries no IANA zone, so its wall clock cannot survive 29 March"
			}
			if !strings.Contains(e.Start.TimeZone, "/") {
				return false, "the series carries " + e.Start.TimeZone + ", which is not an IANA zone name"
			}
			if !strings.Contains(e.Start.DateTime, "T09:00") {
				return false, "the series starts at " + e.Start.DateTime + ", not 09:00"
			}
			if !strings.HasPrefix(e.Start.DateTime, "2026-03-17") {
				return false, "the series starts on " + e.Start.DateTime + ", not 17 March"
			}
			return true, "weekly, at 09:00, carrying " + e.Start.TimeZone
		},
	}
}

// invitationReachesGuest scores an insert that never sets `sendUpdates`,
// which reports success and reaches nobody.
//
// This server refuses the write until the caller chooses, and refuses
// `none` outright for a guest outside the organizer's domain, so the
// task measures two things at once: whether the model can read a refusal
// and recover, and whether what it then chose actually mails the person
// it was asked to invite.
func invitationReachesGuest() task {
	return task{
		name: "invitation reaches a guest",
		system: "You are managing the Google Calendar of somebody who lives in Copenhagen. " +
			"Today is Monday 9 March 2026. Use the tools to do what they ask, and stop when it is done.",
		prompt: "Invite " + outsideGuest + " to a 30-minute intro call on Thursday 19 March 2026 at 15:00. " +
			"They are at another company and they need to actually receive the invitation.",
		seed: func() *caltest.Server {
			s := caltest.New()
			s.AddCalendar(selfAddress, "Mine", "Europe/Copenhagen", gcal.RoleOwner, true)
			return s
		},
		score: func(s *caltest.Server) (bool, string) {
			e, ok := findEvent(s, "intro")
			if !ok {
				return false, "no event was created"
			}
			invited := false
			for _, a := range e.Attendees {
				if strings.EqualFold(a.Email, outsideGuest) {
					invited = true
				}
			}
			if !invited {
				return false, "the event has no guest, so nobody was invited to anything"
			}
			// The half that cannot be seen from the event: what the
			// server was ASKED to send. An event with a guest and
			// sendUpdates=none may never reach them, and it looks
			// identical on the calendar.
			for _, w := range s.Wrote() {
				if w.Method != "insert" || w.EventID != e.ID {
					continue
				}
				switch w.SendUpdates {
				case "all", "externalOnly":
					return true, "invited, and Google was asked to mail them (" + w.SendUpdates + ")"
				case "":
					return false, "the insert sent no sendUpdates at all, so Google was asked to mail nobody"
				default:
					return false, "the guest was invited with sendUpdates=" + w.SendUpdates +
						", so they may never learn of it"
				}
			}
			return false, "the event exists but no insert was recorded for it"
		},
	}
}

// findEvent returns the one event whose title contains needle.
func findEvent(s *caltest.Server, needle string) (*gcal.Event, bool) {
	for _, events := range s.Events {
		for _, e := range events {
			if strings.Contains(strings.ToLower(e.Summary), strings.ToLower(needle)) {
				return e, true
			}
		}
	}
	return nil, false
}
