//go:build live

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

// spikeM — what does Google actually do with a conference create
// request, and what does it do without conferenceDataVersion?
//
// §17.3 asked two things this probe can answer and one it cannot, and
// saying which is which is the whole discipline of §15.
//
// Answerable, and both change the code:
//
//   - **Version 0 is the trap.** The discovery document says
//     conferenceDataVersion defaults to 0 and that version 0 "ignores
//     conference data in the event's body". So an insert that forgets
//     the parameter answers 200, creates the event, and silently has no
//     meeting link — success with the one thing the caller asked for
//     missing. The client sets the parameter from the body; this asks
//     whether the failure it prevents is real.
//   - **What the insert's own answer carries.** The data is generated
//     asynchronously, so the answer should say pending with no entry
//     points. create_event's wording depends on that: if the link were
//     usually there, telling every caller to read the event again would
//     be noise, and if it is usually pending, promising a link would be
//     a lie.
//
// NOT answerable here, and the spike says so rather than implying
// otherwise: what a domain that FORBIDS conferencing does. That needs a
// calendar whose allowedConferenceSolutionTypes excludes hangoutsMeet,
// and this account cannot be made to have one — the field is set by the
// calendar's own domain policy. The server refuses such a write from the
// published list rather than from Google's answer, so the untested path
// is a refusal that never reaches the network. What the spike CAN do is
// record what this account's own calendar publishes, which is the input
// that refusal reads.
func spikeM(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	published, err := api.conferenceTypes(ctx, scratch)
	if err != nil {
		return undetermined, "could not read the scratch calendar's conference properties: " +
			redact.String(err.Error())
	}
	if len(published) == 0 {
		out.Printf("      the scratch calendar publishes NO allowedConferenceSolutionTypes\n")
	} else {
		out.Printf("      the scratch calendar allows: %s\n", strings.Join(published, ", "))
	}

	// A request id per half. Google IGNORES a repeated request id, so
	// reusing one would put a second variable in the probe: the halves
	// must differ in the version parameter and in nothing else.
	request := func(mark string) map[string]any {
		return map[string]any{
			"createRequest": map[string]any{
				"requestId":             "livecalspikem" + mark + runSuffix,
				"conferenceSolutionKey": map[string]any{"type": gcal.ConferenceSolutionMeet},
			},
		}
	}

	// Half one: the same body, sent WITHOUT the version parameter.
	bare := "livecalmeetbare" + runSuffix
	if err := api.insertEvent(ctx, scratch, map[string]any{
		"id": bare, "summary": "Livecal conference probe, no version",
		"start":          map[string]any{"dateTime": "2026-04-09T14:00:00+02:00", "timeZone": scratchZone},
		"end":            map[string]any{"dateTime": "2026-04-09T15:00:00+02:00", "timeZone": scratchZone},
		"conferenceData": request("bare"),
	}); err != nil {
		return undetermined, "the insert without the version failed, so what it does with the " +
			"conference is unknown: " + redact.String(err.Error())
	}
	bareConf, err := api.conferenceOf(ctx, scratch, bare)
	if err != nil {
		return undetermined, "could not read the event back: " + redact.String(err.Error())
	}

	// Half two: the version parameter set, which is what the client
	// sends. A separate event, so the two answers cannot be confused.
	versioned := "livecalmeetversion" + runSuffix
	if err := api.insertConference(ctx, scratch, map[string]any{
		"id": versioned, "summary": "Livecal conference probe, version 1",
		"start":          map[string]any{"dateTime": "2026-04-09T16:00:00+02:00", "timeZone": scratchZone},
		"end":            map[string]any{"dateTime": "2026-04-09T17:00:00+02:00", "timeZone": scratchZone},
		"conferenceData": request("ver"),
	}); err != nil {
		return undetermined, "the insert WITH conferenceDataVersion=1 failed: " + redact.String(err.Error())
	}
	onInsert, err := api.conferenceOf(ctx, scratch, versioned)
	if err != nil {
		return undetermined, "could not read the versioned event back: " + redact.String(err.Error())
	}

	out.Printf("      without the version: conference present=%v status=%q\n",
		bareConf.Present, bareConf.Status)
	out.Printf("      with    the version: conference present=%v status=%q link=%v\n",
		onInsert.Present, onInsert.Status, onInsert.Ready())

	switch {
	case bareConf.Present && !onInsert.Present:
		return fail, "the version parameter made the conference DISAPPEAR, which is the opposite of " +
			"what the discovery document says; the client's rule is wrong"
	case bareConf.Present:
		return pass, "CONVENTION REFUTED: conference data survived an insert with NO " +
			"conferenceDataVersion. Version 0 does not ignore it after all, so the client's rule is " +
			"belt and braces rather than the difference between a link and none"
	case !onInsert.Present:
		return fail, "conferenceDataVersion=1 was sent and the event came back with no conference " +
			"data at all, so a Meet link cannot be created this way"
	default:
		return pass, fmt.Sprintf("CONFIRMED both halves: without the version the conference is "+
			"dropped in silence (200, event created, no link), and with it the insert answers %q%s",
			onInsert.Status, readyNote(onInsert))
	}
}

func readyNote(c gcal.Conference) string {
	if c.Ready() {
		return " with the link already in it — create_event's \"read it again\" advice is belt and braces"
	}
	return " with no entry point yet, so create_event must not promise a link"
}

// conferenceTypes reads a calendar's allowedConferenceSolutionTypes,
// which is what create_event's refusal is built on.
func (a *liveAPI) conferenceTypes(ctx context.Context, cal string) ([]string, error) {
	var out struct {
		ConferenceProperties *gcal.ConferenceProperties `json:"conferenceProperties"`
	}
	if err := a.do(ctx, http.MethodGet, "/calendars/"+cal, nil, &out); err != nil {
		return nil, err
	}
	if out.ConferenceProperties == nil {
		return nil, nil
	}
	return out.ConferenceProperties.AllowedConferenceSolutionTypes, nil
}

// conferenceOf reads one event's conference data.
func (a *liveAPI) conferenceOf(ctx context.Context, cal, id string) (gcal.Conference, error) {
	var out struct {
		ConferenceData json.RawMessage `json:"conferenceData"`
	}
	if err := a.do(ctx, http.MethodGet, "/calendars/"+cal+"/events/"+id, nil, &out); err != nil {
		return gcal.Conference{}, err
	}
	return gcal.ReadConference(out.ConferenceData), nil
}

// insertConference posts an event with conferenceDataVersion=1, which is
// the parameter the whole spike is about. It is spelled out here rather
// than reusing insertEvent so that the two halves differ in exactly one
// thing.
func (a *liveAPI) insertConference(ctx context.Context, cal string, body map[string]any) error {
	if id, ok := body["id"].(string); ok {
		if err := gcal.ValidEventID(id); err != nil {
			return err
		}
	}
	return a.do(ctx, http.MethodPost,
		"/calendars/"+cal+"/events?sendUpdates=none&conferenceDataVersion=1", body, nil)
}
