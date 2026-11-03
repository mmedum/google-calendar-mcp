package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v5/internal/server"
	"github.com/mmedum/google-calendar-mcp/v5/internal/service"
	"github.com/mmedum/google-calendar-mcp/v5/internal/tools"
)

// The rule this file holds (D-03): a log line says which call ran and
// how it ended, never what the call was about. The payload is
// somebody's calendar: titles, descriptions, places, guests and search
// terms. A log gets pasted into bug reports, so it has to be safe to
// paste by construction.
//
// No log line carries a payload. Nothing but this test stops the next
// change from adding one.

// canaryMarker starts every canary but an address, so the log is searched
// for the marker and a canary added later cannot be left off a list.
const canaryMarker = "CANARY-"

const (
	canaryTitle         = canaryMarker + "event-title"
	canaryDescription   = canaryMarker + "event-description"
	canaryLocation      = canaryMarker + "event-location"
	canaryGuest         = "janedoe@example.com"
	canaryGuestName     = canaryMarker + "guest-display-name"
	canaryColleague     = "johndoe@example.test"
	canaryOutsider      = "johndoe@example.com"
	canaryComment       = canaryMarker + "reply-comment"
	canaryDecline       = canaryMarker + "decline-message"
	canaryCalendarTitle = canaryMarker + "calendar-title"
	canaryQuery         = canaryMarker + "search-term"
	canaryDroppedQuery  = canaryMarker + "query-in-a-dropped-request"
	canaryRefusal       = canaryMarker + "refusal-detail"
	canaryBadArgument   = canaryMarker + "invalid-argument"
)

// forbidden is what no log line may hold: the marker, and the canary
// addresses, which cannot carry it.
var forbidden = []string{canaryMarker, canaryGuest, canaryColleague, canaryOutsider}

const (
	ownAddress       = "owner@example.test"
	canaryCalendar   = "canary@group.calendar.example.test"
	refusingCalendar = "refusing@group.calendar.example.test"
)

// canaryFake is a calendar set full of canaries, so whatever Google
// sends back is traceable through the logs as well as what the caller
// sends. The primary calendar's id is the account's address, as
// Google's is, so a share inside the organization goes through rather
// than being refused before it reaches the fake.
func canaryFake() *caltest.Server {
	const tz = "Europe/Copenhagen"
	fake := caltest.New()
	fake.AddCalendar(ownAddress, "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddCalendar(canaryCalendar, canaryCalendarTitle, tz, gcal.RoleOwner, false)
	fake.Calendars[canaryCalendar].Description = canaryDescription
	fake.Calendars[canaryCalendar].Location = canaryLocation
	fake.Settings = []gcal.Setting{{ID: gcal.SettingTimezone, Value: tz}}
	fake.Busy[ownAddress] = []gcal.TimePeriod{{Start: "2026-03-16T09:00:00Z", End: "2026-03-16T09:15:00Z"}}

	guests := func() []gcal.EventAttendee {
		return []gcal.EventAttendee{
			{Email: canaryGuest, DisplayName: canaryGuestName, Organizer: true, ResponseStatus: gcal.ResponseAccepted},
			{Email: ownAddress, Self: true, ResponseStatus: gcal.ResponseNeedsAction},
		}
	}
	ev := caltest.Timed("ev-canary", canaryTitle,
		"2026-03-19T10:00:00+01:00", "2026-03-19T11:00:00+01:00", tz)
	ev.Description = canaryDescription
	ev.Location = canaryLocation
	ev.Organizer = &gcal.EventPerson{Email: canaryGuest, DisplayName: canaryGuestName}
	ev.Attendees = guests()
	fake.AddEvent(ownAddress, ev)

	series := caltest.Recurring("ev-canary-weekly", canaryTitle,
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00", tz, "RRULE:FREQ=WEEKLY;COUNT=4")
	series.Location = canaryLocation
	series.Attendees = guests()
	fake.AddEvent(ownAddress, series)
	occurrence := caltest.Instance("ev-canary-weekly_20260324T130000Z", "ev-canary-weekly", canaryTitle,
		"2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00", tz, "2026-03-24T14:00:00+01:00")
	occurrence.Location = canaryLocation
	occurrence.Attendees = guests()
	fake.AddEvent(ownAddress, occurrence)

	fake.ACL[ownAddress] = []gcal.AclRule{
		{ID: "user:" + ownAddress, Role: gcal.RoleOwner,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: ownAddress}},
		{ID: "user:" + canaryGuest, Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: canaryGuest}},
	}

	// A refusal whose detail quotes the request back, which Google does.
	fake.AddCalendar(refusingCalendar, "Sample Refusing", tz, gcal.RoleOwner, false)
	fake.Fail[refusedPrefix] = http.StatusForbidden
	fake.FailMessage[refusedPrefix] = canaryRefusal
	return fake
}

// refusedPrefix matches every read under refusingCalendar's path.
const refusedPrefix = "GET /calendars/" + refusingCalendar

// dropSearch fails every request whose search term is query, before
// any answer. net/http wraps that failure in a *url.Error, which quotes
// the whole URL, query string included. Logging that error as it
// stands would log the search term; stripURL is what removes it.
type dropSearch struct {
	query string
	next  http.RoundTripper
}

func (d dropSearch) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("q") == d.query {
		return nil, errors.New("connection reset by peer")
	}
	return d.next.RoundTrip(r)
}

// syncBuffer is a log sink the server's goroutines and the test can
// share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// payloadCalls drives every tool with the canaries in its arguments,
// reads first and the writes that remove things last. A refusal is as
// interesting as a success: an error path is where a payload most
// easily reaches a log, so no result is checked here.
var payloadCalls = []struct {
	name string
	args map[string]any
}{
	{"list_calendars", map[string]any{}},
	{"get_calendar", map[string]any{"calendar": canaryCalendar}},
	{"list_sharing", map[string]any{"calendar": refusingCalendar}},
	{"get_settings", map[string]any{}},
	{"list_events", map[string]any{"from": "2026-03-16", "to": "2026-03-22"}},
	{"search_events", map[string]any{"query": canaryQuery, "from": "2026-03-16", "to": "2026-03-22"}},
	{"search_events", map[string]any{"query": canaryDroppedQuery, "from": "2026-03-16", "to": "2026-03-22"}},
	{"get_event", map[string]any{"calendar": "primary", "event_id": "ev-canary"}},
	// Arguments the SDK refuses before the handler runs: a failure with
	// no class, whose text can quote the value.
	{"get_event", map[string]any{"calendar": "primary", "event_id": []string{canaryBadArgument}}},
	{"list_instances", map[string]any{"calendar": "primary", "event_id": "ev-canary-weekly"}},
	{"check_availability", map[string]any{
		"from": "2026-03-16", "to": "2026-03-16", "calendars": []string{"primary", canaryGuest},
	}},
	{"list_sharing", map[string]any{"calendar": "primary"}},
	{"create_calendar", map[string]any{
		"title": canaryCalendarTitle, "description": canaryDescription, "location": canaryLocation,
	}},
	{"manage_calendar", map[string]any{
		"calendar": canaryCalendar, "description": canaryDescription, "location": canaryLocation,
		"my_name": canaryCalendarTitle,
	}},
	{"create_event", map[string]any{
		"title": canaryTitle, "description": canaryDescription, "location": canaryLocation,
		"start": "2026-04-01T09:00:00+02:00", "end": "2026-04-01T10:00:00+02:00",
		"guests": []string{canaryGuest}, "notify": "all",
	}},
	{"create_event_declining_all", map[string]any{
		"title": canaryTitle, "description": canaryDescription, "event_type": "outOfOffice",
		"start": "2026-04-02T09:00:00+02:00", "end": "2026-04-02T17:00:00+02:00",
		"decline_message": canaryDecline,
	}},
	{"update_event", map[string]any{
		"calendar": "primary", "event_id": "ev-canary",
		"title": canaryTitle, "description": canaryDescription, "location": canaryLocation,
		"add_guests": []string{canaryOutsider}, "notify": "none",
	}},
	{"respond_to_event", map[string]any{
		"calendar": "primary", "event_id": "ev-canary", "response": "tentative",
		"comment": canaryComment, "notify": "none",
	}},
	{"list_changes", map[string]any{"calendar": "primary", "updated_since": "2026-03-16"}},
	{"share_calendar", map[string]any{
		"calendar": "primary", "who": canaryColleague, "role": "reader", "notify": "none",
	}},
	{"unshare_calendar", map[string]any{"calendar": "primary", "who": canaryGuest}},
	{"widen_calendar_access", map[string]any{
		"calendar": canaryCalendar, "calendar_title": canaryCalendarTitle, "who": canaryOutsider,
		"role": "reader", "notify": "none", "confirm": true,
	}},
	{"publish_calendar", map[string]any{
		"calendar": canaryCalendar, "calendar_title": canaryCalendarTitle, "who": "anyone",
		"role": "freeBusyReader", "notify": "none", "confirm": true,
	}},
	{"move_event", map[string]any{
		"calendar": "primary", "event_id": "ev-canary", "to_calendar": canaryCalendar, "notify": "none",
	}},
	{"cancel_event", map[string]any{"calendar": canaryCalendar, "event_id": "ev-canary", "notify": "none"}},
	{"clear_calendar", map[string]any{"calendar": "primary", "confirm": true}},
	{"delete_calendar", map[string]any{"calendar": canaryCalendar, "confirm": true}},
}

// TestLogsNeverCarryThePayload drives every tool through the server, at
// debug level, against a fake full of canaries, and reads back every
// line logged. Both the call log and the SDK's own logger are attached
// at debug, so nothing the server logs is filtered out.
func TestLogsNeverCarryThePayload(t *testing.T) {
	fake := canaryFake()
	base := fake.Start()
	t.Cleanup(fake.Close)

	api := gapi.New(&http.Client{Transport: dropSearch{query: canaryDroppedQuery, next: http.DefaultTransport}})
	api.Base = base
	api.Sleep = func(context.Context, time.Duration) error { return nil }

	var logs syncBuffer
	cfg := tools.FullSurface(config.Config{MaxEvents: 250, MaxCalendars: 25, Concurrency: 4})
	srv := server.New(server.Deps{
		Service: service.New(api, cfg), Config: cfg, Version: "test",
		Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	ctx := context.Background()
	ct, st := newTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := newClient().Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}

	coversEveryTool(t, cs)
	// The resources are the tools' content for a client that attaches
	// rather than calls, so they carry the same payload.
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "gcal://calendars/primary/events/ev-canary"}); err != nil {
		t.Fatalf("read resource: %v", err)
	}
	var droppedResult *mcp.CallToolResult
	for _, c := range payloadCalls {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Fatalf("call %s: %v", c.name, err)
		}
		if c.args["query"] == canaryDroppedQuery {
			droppedResult = res
		}
	}
	// Closing and waiting means every line the session will ever write
	// is in the buffer before it is read.
	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	_ = ss.Wait()

	// The transport failure has to have happened, or the dropped query
	// below proves nothing.
	if droppedResult == nil || !droppedResult.IsError || !strings.Contains(resultText(droppedResult), "could not reach Google") {
		t.Fatalf("the dropped search did not fail as a transport error: %+v", droppedResult)
	}

	if !slices.ContainsFunc(fake.Served(), func(p string) bool { return strings.HasPrefix(p, refusedPrefix) }) {
		t.Fatal("no request was refused; the refusal this test needs never happened")
	}

	out := logs.String()
	for _, canary := range forbidden {
		if strings.Contains(out, canary) {
			t.Errorf("the logs carry the canary %q:\n%s", canary, linesWith(out, canary))
		}
	}

	// The floor: a server that logs nothing would pass everything above.
	// Every call leaves a line with its method, outcome and duration,
	// and a tool call names its tool.
	logged := map[string]bool{}
	methods := map[string]bool{}
	// failedClasses holds each failed call's class value, nil where the
	// line has no class key.
	failedClasses := map[string][]any{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("a log line is not JSON: %v\n%s", err, sc.Text())
		}
		method, _ := line["method"].(string)
		if method == "" {
			continue
		}
		methods[method] = true
		if _, ok := line["outcome"].(string); !ok {
			t.Errorf("a call line has no outcome: %s", sc.Text())
		}
		if _, ok := line["ms"].(float64); !ok {
			t.Errorf("a call line has no duration: %s", sc.Text())
		}
		if tool, ok := line["tool"].(string); ok {
			logged[tool] = true
			if line["outcome"] == "error" {
				failedClasses[tool] = append(failedClasses[tool], line["class"])
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// A tool's failure arrives as a result, not an error, and is still
	// logged as one, with its class and nothing of its text. A failure
	// the SDK made has no class, and its line has no class key.
	for tool, want := range map[string][]any{
		"list_sharing":  {"forbidden"},
		"search_events": {"unavailable"},
		"get_event":     {nil},
	} {
		if got := failedClasses[tool]; !slices.Equal(got, want) {
			t.Errorf("the failed %s calls logged the classes %#v, want %#v", tool, got, want)
		}
	}
	var missing []string
	for _, c := range payloadCalls {
		if !logged[c.name] && !slices.Contains(missing, c.name) {
			missing = append(missing, c.name)
		}
	}
	for _, m := range []string{"tools/list", "tools/call", "resources/read"} {
		if !methods[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Errorf("no call line names %s:\n%s", strings.Join(missing, ", "), out)
	}
}

// A call naming a tool the configuration leaves unregistered, here a
// destructive one with the flag off, is a protocol error. The SDK passes
// it on with a typed-nil result, which the call log must not read.
func TestAnUnregisteredToolIsLoggedAsAFailure(t *testing.T) {
	var logs syncBuffer
	d := deps(t)
	d.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cs := session(t, d)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "delete_calendar", Arguments: map[string]any{"calendar": "primary", "confirm": true},
	})
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) || wire.Code != jsonrpc.CodeInvalidParams || res != nil {
		t.Fatalf("calling an unregistered tool = %+v, %v; want no result and a JSON-RPC invalid-params error", res, err)
	}

	// The line is written before the reply is sent, so it is in the
	// buffer once the call returns.
	var calls []map[string]any
	for raw := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("a log line is not JSON: %v\n%s", err, raw)
		}
		if line["method"] == "tools/call" {
			delete(line, "time")
			delete(line, "ms")
			calls = append(calls, line)
		}
	}
	want := map[string]any{
		"level": "DEBUG", "msg": "mcp call failed", "method": "tools/call", "tool": "delete_calendar", "outcome": "error",
	}
	if len(calls) != 1 || !maps.Equal(calls[0], want) {
		t.Errorf("the tools/call lines are %v, want one: %v", calls, want)
	}
}

// coversEveryTool fails when a registered tool is missing from
// payloadCalls, so a new tool cannot slip past this test unexercised.
func coversEveryTool(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	registered, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	driven := map[string]bool{}
	for _, c := range payloadCalls {
		driven[c.name] = true
	}
	for _, tool := range registered.Tools {
		if !driven[tool.Name] {
			t.Errorf("%s is registered and payloadCalls does not drive it", tool.Name)
		}
	}
}

// resultText is a tool result's text content.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// linesWith returns the log lines containing s, so a failure names the
// line instead of printing the whole buffer.
func linesWith(out, s string) string {
	var hits []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, s) {
			hits = append(hits, line)
		}
	}
	return strings.Join(hits, "\n")
}
