package tools_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v3/internal/config"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/server"
	"github.com/mmedum/google-calendar-mcp/v3/internal/service"
	"github.com/mmedum/google-calendar-mcp/v3/internal/tools"
)

// The protocols a question goes out on: before 2026-07-28 the SDK asks
// with elicitation/create inside the call; from it, the call returns the
// question and comes back with the answer (§9a).
var protocols = []string{"2025-06-18", "2025-11-25", "2026-07-28"}

// everything registers every tool.
func everything() config.Config { return tools.FullSurface(baseConfig()) }

// person answers the questions a test client is asked, and keeps them.
type person struct {
	mu        sync.Mutex
	questions []*mcp.ElicitParams
	action    string
	// fails makes the client answer with an error instead.
	fails bool
}

func (p *person) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, req.Params)
	if p.fails {
		return nil, errors.New("client-side secret text")
	}
	return &mcp.ElicitResult{Action: p.action}, nil
}

func (p *person) asked() []*mcp.ElicitParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.questions)
}

const tz = "Europe/Copenhagen"

// fixtures is the fake Calendar every asking case can reach its write
// in: an owned primary and secondary calendar, a meeting with guests on
// either side of the organizer's domain, and a weekly series with one.
func fixtures(t *testing.T) *caltest.Server {
	t.Helper()
	fake := caltest.New()
	fake.AddCalendar("me@example.test", "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddCalendar("team@group.calendar.example.test", "Sample Team", tz, gcal.RoleOwner, false)
	fake.ACL["team@group.calendar.example.test"] = []gcal.AclRule{
		{ID: "user:me@example.test", Role: gcal.RoleOwner,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "me@example.test"}},
	}
	fake.Settings = []gcal.Setting{{ID: gcal.SettingTimezone, Value: tz}}

	fake.AddEvent("me@example.test", caltest.Timed("evsolo00001", "Solo thinking",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:30:00+01:00", tz))
	meeting := caltest.Timed("evguests001", "Project review",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00", tz)
	withGuests(meeting)
	fake.AddEvent("me@example.test", meeting)

	inside := caltest.Timed("evinside001", "Team sync",
		"2026-03-19T10:00:00+01:00", "2026-03-19T11:00:00+01:00", tz)
	inside.Organizer = &gcal.EventPerson{Email: "me@example.test", Self: true}
	inside.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true, ResponseStatus: gcal.ResponseAccepted},
		{Email: "colleague@example.test", ResponseStatus: gcal.ResponseAccepted},
	}
	fake.AddEvent("me@example.test", inside)

	series := caltest.Recurring("evseries001", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00", tz, "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=8")
	withGuests(series)
	fake.AddEvent("me@example.test", series)
	occurrence := caltest.Instance("evseries001_20260324T130000Z", "evseries001", "Weekly review",
		"2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00", tz, "2026-03-24T14:00:00+01:00")
	withGuests(occurrence)
	fake.AddEvent("me@example.test", occurrence)
	return fake
}

func withGuests(e *gcal.Event) {
	e.Organizer = &gcal.EventPerson{Email: "me@example.test", Self: true}
	e.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true, ResponseStatus: gcal.ResponseAccepted},
		{Email: "colleague@example.test", ResponseStatus: gcal.ResponseAccepted},
		{Email: "partner@elsewhere.test", ResponseStatus: gcal.ResponseNeedsAction},
	}
}

// connect connects a client on protocol to a server over a fresh fake.
// A nil p declares no elicitation; opts adjust the client further.
func connect(t *testing.T, cfg config.Config, protocol string, p *person, opts ...func(*mcp.ClientOptions)) (*mcp.ClientSession, *caltest.Server) {
	t.Helper()
	srv, fake := newServer(t, cfg)
	return connectTo(t, srv, protocol, p, opts...), fake
}

// newServer is a server over a fresh fake.
func newServer(t *testing.T, cfg config.Config) (*mcp.Server, *caltest.Server) {
	t.Helper()
	fake := fixtures(t)
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base
	api.MaxRetries = 0
	return server.New(server.Deps{Service: service.New(api, cfg), Config: cfg, Version: "test"}), fake
}

// connectTo connects one more client to srv, as connect does.
func connectTo(t *testing.T, srv *mcp.Server, protocol string, p *person, opts ...func(*mcp.ClientOptions)) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	o := &mcp.ClientOptions{}
	if p != nil {
		o.ElicitationHandler = p.handle
	}
	for _, fn := range opts {
		fn(o)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, o).
		Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// askCase is a call that clears a tool's own guards and reaches its
// write, the write as the fake records it, and words its question must
// carry.
type askCase struct {
	args   map[string]any
	method string
	target string // the calendar, event or rule the write addresses
	shows  []string
}

var askCases = map[string]askCase{
	"delete_calendar": {
		args:   map[string]any{"calendar": "team@group.calendar.example.test", "confirm": true},
		method: "calendars.delete", target: "team@group.calendar.example.test",
		shows: []string{"delete the calendar `Sample Team` and every event on it, for good"},
	},
	"clear_calendar": {
		args:   map[string]any{"calendar": "primary", "confirm": true},
		method: "calendars.clear", target: "me@example.test",
		shows: []string{"delete every event on your primary calendar `Sample Primary`"},
	},
	"share_calendar": {
		args: map[string]any{"calendar": "team@group.calendar.example.test", "who": "anyone", "role": "reader",
			"notify": "none", "allow_public": true},
		method: "acl.insert", target: "team@group.calendar.example.test",
		shows: []string{"publish the calendar `Sample Team` to anyone on the internet, as reader"},
	},
	"cancel_event": {
		args:   map[string]any{"calendar": "primary", "event_id": "evguests001", "notify": "all"},
		method: "delete", target: "evguests001",
		shows: []string{"cancel `Project review` on the calendar `Sample Primary`",
			"and email all 2 guests?", "Among them, 1 is outside your organization.", "starts 2026-03-18 10:00-11:00 Europe/Copenhagen"},
	},
}

// writes counts the calls that made a case's write.
func writes(fake *caltest.Server, c askCase) int {
	n := 0
	for _, w := range fake.Wrote() {
		if w.Method == c.method && (w.CalendarID == c.target || w.EventID == c.target) {
			n++
		}
	}
	return n
}

func callTool(t *testing.T, cs *mcp.ClientSession, p *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), p)
	if err != nil {
		t.Fatalf("calling %s: %v", p.Name, err)
	}
	return res
}

// Declined, nothing is written; accepted, the write is made once. On
// every protocol, for every tool that asks, and the question says what
// the write would do.
func TestEveryAskingWriteWaitsForThePerson(t *testing.T) {
	for name, c := range askCases {
		for _, protocol := range protocols {
			for _, action := range []string{"decline", "cancel", "accept"} {
				p := &person{action: action}
				cs, fake := connect(t, everything(), protocol, p)
				res := callTool(t, cs, &mcp.CallToolParams{Name: name, Arguments: c.args})
				out := text(t, res)
				qs := p.asked()
				if len(qs) != 1 {
					t.Fatalf("%s %s %s: asked %d times: %s", name, protocol, action, len(qs), out)
				}
				for _, want := range c.shows {
					if !strings.Contains(qs[0].Message, want) {
						t.Errorf("%s: the question does not say %q:\n%s", name, want, qs[0].Message)
					}
				}
				n := writes(fake, c)
				if action != "accept" {
					if !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") || n != 0 {
						t.Errorf("%s %s %s: %d writes: %s", name, protocol, action, n, out)
					}
					continue
				}
				if res.IsError || n != 1 {
					t.Errorf("%s %s accepted: %d writes: %s", name, protocol, n, out)
				}
			}
		}
	}
}

// Every tool that takes confirm asks, as do share_calendar and
// cancel_event; the confirm half of the list is read from the published
// schemas, not typed out.
func TestEveryToolThatTakesConfirmAsks(t *testing.T) {
	want := map[string]bool{"share_calendar": true, "cancel_event": true}
	registered := map[string]bool{}
	for _, tool := range listTools(t, everything()) {
		registered[tool.Name] = true
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["confirm"]; ok {
			want[tool.Name] = true
		}
		asks := strings.Contains(tool.Description, "also asks the person")
		if _, ok := askCases[tool.Name]; ok != asks {
			t.Errorf("%s: an asking case %v, and its description says it asks %v", tool.Name, ok, asks)
		}
	}
	if len(want) < 4 {
		t.Fatalf("found %d asking tools; the schemas were not read", len(want))
	}
	for name := range want {
		if _, ok := askCases[name]; !ok {
			t.Errorf("%s takes confirm or is an asking tool and has no asking case", name)
		}
	}
	for name := range askCases {
		if !want[name] || !registered[name] {
			t.Errorf("%s has an asking case and is not an asking tool", name)
		}
	}
}

// A client that cannot ask gets no question, and the arguments are the
// guard; GCAL_REQUIRE_PROMPT refuses the write instead.
func TestAClientThatCannotAsk(t *testing.T) {
	for _, require := range []bool{false, true} {
		cfg := everything()
		cfg.RequirePrompt = require
		cs, fake := connect(t, cfg, "", nil)
		c := askCases["delete_calendar"]
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args})
		n := writes(fake, c)
		switch {
		case require && (!res.IsError || !strings.Contains(text(t, res), "GCAL_REQUIRE_PROMPT") || n != 0):
			t.Errorf("required: %d writes: %s", n, text(t, res))
		case !require && (res.IsError || n != 1):
			t.Errorf("not required: %d writes: %s", n, text(t, res))
		}
	}
}

// A share asks when it reaches a whole domain or makes an owner, and not
// when it gives a named person less than that.
func TestWhichSharesAsk(t *testing.T) {
	for _, tc := range []struct {
		who, role, want string
	}{
		{"colleague@example.test", "reader", ""},
		{"colleague@example.test", "writer", ""},
		{"example.test", "reader", "let everyone at `example.test` see the calendar `Sample Team`, as reader"},
		{"colleague@example.test", "owner", "make `colleague@example.test` an owner of the calendar `Sample Team`?"},
	} {
		p := &person{action: "decline"}
		cs, fake := connect(t, everything(), "2026-07-28", p)
		args := map[string]any{"calendar": "team@group.calendar.example.test", "who": tc.who, "role": tc.role, "notify": "all"}
		if strings.Contains(tc.who, "@") {
			args["scope_type"] = "user"
		} else {
			args["scope_type"] = "domain"
		}
		res := callTool(t, cs, &mcp.CallToolParams{Name: "share_calendar", Arguments: args})
		qs := p.asked()
		written := len(fake.Wrote()) > 0
		switch {
		case tc.want == "" && (len(qs) != 0 || res.IsError || !written):
			t.Errorf("%s as %s: asked %d, written %v: %s", tc.who, tc.role, len(qs), written, text(t, res))
		case tc.want != "" && (len(qs) != 1 || !res.IsError || written):
			t.Errorf("%s as %s: asked %d, written %v: %s", tc.who, tc.role, len(qs), written, text(t, res))
		case tc.want != "" && !strings.Contains(qs[0].Message, tc.want):
			t.Errorf("%s as %s: %s", tc.who, tc.role, qs[0].Message)
		}
	}
}

// A cancellation asks only when it emails a guest: notify none, and an
// event with nobody to email, ask nothing. Each scope's question names
// what goes.
func TestWhichCancellationsAsk(t *testing.T) {
	for _, tc := range []struct {
		args map[string]any
		want []string
	}{
		{map[string]any{"event_id": "evsolo00001", "notify": "all"}, nil},
		{map[string]any{"event_id": "evsolo00001"}, nil},
		{map[string]any{"event_id": "evinside001", "notify": "none"}, nil},
		{map[string]any{"event_id": "evinside001", "notify": "external_only"}, nil},
		{map[string]any{"event_id": "evguests001", "notify": "external_only"},
			[]string{"email the 1 of 2 guests outside your organization"}},
		{map[string]any{"event_id": "evseries001", "scope": "series", "notify": "all"},
			[]string{"cancel every occurrence of `Weekly review`", "the series starts 2026-03-17 14:00-15:00"}},
		{map[string]any{"event_id": "evseries001_20260324T130000Z", "scope": "instance", "notify": "all"},
			[]string{"cancel one occurrence of `Weekly review` on", "starts 2026-03-24 14:00-15:00"}},
		{map[string]any{"event_id": "evseries001_20260324T130000Z", "scope": "this_and_following", "notify": "all"},
			[]string{"`Weekly review` from one occurrence on", "that occurrence starts 2026-03-24 14:00 Europe/Copenhagen"}},
	} {
		p := &person{action: "decline"}
		cs, fake := connect(t, everything(), "2026-07-28", p)
		tc.args["calendar"] = "primary"
		res := callTool(t, cs, &mcp.CallToolParams{Name: "cancel_event", Arguments: tc.args})
		qs := p.asked()
		written := len(fake.Wrote()) > 0
		if tc.want == nil {
			if len(qs) != 0 || res.IsError || !written {
				t.Errorf("%v: asked %d, written %v: %s", tc.args, len(qs), written, text(t, res))
			}
			continue
		}
		if len(qs) != 1 || !res.IsError || written {
			t.Errorf("%v: asked %d, written %v: %s", tc.args, len(qs), written, text(t, res))
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(qs[0].Message, w) {
				t.Errorf("%v: the question does not say %q:\n%s", tc.args, w, qs[0].Message)
			}
		}
	}
}

// A dry run asks nothing and needs no confirm.
func TestADryRunAsksNothing(t *testing.T) {
	p := &person{action: "decline"}
	cs, fake := connect(t, everything(), "2026-07-28", p)
	for name, c := range askCases {
		args := map[string]any{"dry_run": true}
		for k, v := range c.args {
			if k != "confirm" {
				args[k] = v
			}
		}
		if res := callTool(t, cs, &mcp.CallToolParams{Name: name, Arguments: args}); res.IsError {
			t.Errorf("%s: %s", name, text(t, res))
		}
	}
	if qs := p.asked(); len(qs) != 0 {
		t.Errorf("asked %d questions: %s", len(qs), qs[0].Message)
	}
	if n := len(fake.Wrote()); n != 0 {
		t.Errorf("a dry run made %d writes", n)
	}
}

// mrtr connects a 2026-07-28 client that hands each question back
// instead of answering it, so a test can answer by hand.
func mrtr(t *testing.T) (*mcp.ClientSession, *caltest.Server) {
	t.Helper()
	return connect(t, everything(), "2026-07-28", &person{action: "accept"}, func(o *mcp.ClientOptions) {
		o.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	})
}

var accepted = mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: "accept"}}

// The first round only asks. The answer counts once, only with the state
// it was asked with, only for that call, and only while fresh.
func TestTheAnswerIsBoundToItsQuestion(t *testing.T) {
	cs, fake := mrtr(t)
	c := askCases["delete_calendar"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args})
	q, ok := first.InputRequests["confirm"].(*mcp.ElicitParams)
	if !first.NeedsInput() || !ok || q.Mode != "form" || first.RequestState == "" || writes(fake, c) != 0 {
		t.Fatalf("first round %+v; %d writes", first, writes(fake, c))
	}
	state := first.RequestState

	blocked := func(p *mcp.CallToolParams, want string) {
		t.Helper()
		res := callTool(t, cs, p)
		if out := text(t, res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, want) {
			t.Errorf("%s", out)
		}
	}
	other := map[string]any{"calendar": "primary", "confirm": true}
	blocked(&mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted},
		"answers to a question this server has not asked")
	blocked(&mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted, RequestState: state + "x"},
		"did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted,
		RequestState: "e30." + strings.Split(state, ".")[1]}, "did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_calendar", Arguments: other, InputResponses: accepted, RequestState: state},
		"another call")
	blocked(&mcp.CallToolParams{Name: "clear_calendar", Arguments: other, InputResponses: accepted,
		RequestState: state}, "another call")
	blocked(&mcp.CallToolParams{Name: "unshare_calendar", Arguments: map[string]any{
		"calendar": "team@group.calendar.example.test", "who": "me@example.test"},
		InputResponses: accepted, RequestState: state}, "not a tool here that asks the person")
	if n := len(fake.Wrote()); n != 0 {
		t.Fatalf("%d writes before the answer", n)
	}

	done := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted, RequestState: state})
	if done.IsError || writes(fake, c) != 1 {
		t.Fatalf("the verified retry: %s; %d writes", text(t, done), writes(fake, c))
	}
	blocked(&mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted, RequestState: state}, "already used")
}

// Any answer but an accept is refused before the call reads anything.
func TestARefusalIsRefusedBeforeAnyRead(t *testing.T) {
	for _, action := range []string{"decline", "cancel", "maybe"} {
		cs, fake := mrtr(t)
		c := askCases["cancel_event"]
		first := callTool(t, cs, &mcp.CallToolParams{Name: "cancel_event", Arguments: c.args})
		before := len(fake.Served())
		res := callTool(t, cs, &mcp.CallToolParams{Name: "cancel_event", Arguments: c.args, RequestState: first.RequestState,
			InputResponses: mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: action}}})
		if out := text(t, res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") {
			t.Errorf("%s: %s", action, out)
		}
		if n := len(fake.Served()); before == 0 || n != before {
			t.Errorf("%s: %d Calendar calls after the answer (%d before)", action, n-before, before)
		}
	}
}

// What the person saw is what is written: an event renamed between the
// question and the answer is refused, and the next call asks again.
func TestAChangeAfterTheQuestionIsRefused(t *testing.T) {
	cs, fake := mrtr(t)
	c := askCases["cancel_event"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "cancel_event", Arguments: c.args})
	fake.Events["me@example.test"]["evguests001"].Summary = "Something else entirely"
	res := callTool(t, cs, &mcp.CallToolParams{Name: "cancel_event", Arguments: c.args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := text(t, res); !res.IsError || !strings.Contains(out, "changed after the person was asked") || writes(fake, c) != 0 {
		t.Errorf("%s; %d writes", out, writes(fake, c))
	}
}

// A state that travels through the client expires; one that stays in
// the process, before 2026-07-28, waits as long as the request does.
func TestALateAnswerIsRefusedOnlyWhenTheStateTravels(t *testing.T) {
	t.Cleanup(tools.SetAskTTL(-time.Minute))
	cs, fake := mrtr(t)
	c := askCases["delete_calendar"]
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args})
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := text(t, res); !res.IsError || !strings.Contains(out, "expired") || writes(fake, c) != 0 {
		t.Fatalf("%s; %d writes", out, writes(fake, c))
	}
	for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
		cs, fake := connect(t, everything(), protocol, &person{action: "accept"})
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args})
		if res.IsError || writes(fake, c) != 1 {
			t.Errorf("%s: a slow accept in the process was refused: %s", protocol, text(t, res))
		}
	}
}

// A client that answers the question with an error writes nothing, and
// its error text is not repeated.
func TestAClientErrorIsNotAnAnswer(t *testing.T) {
	for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
		cs, fake := connect(t, everything(), protocol, &person{fails: true})
		c := askCases["delete_calendar"]
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_calendar", Arguments: c.args})
		out := text(t, res)
		if !res.IsError || !strings.HasPrefix(out, "[blocked]") || strings.Contains(out, "secret") || writes(fake, c) != 0 {
			t.Errorf("%s: %d writes: %s", protocol, writes(fake, c), out)
		}
	}
}

// Changing an existing rule asks when the new rule would: a person made
// an owner, a domain rule given more. The question says what it was.
func TestARoleChangeAsks(t *testing.T) {
	for _, tc := range []struct {
		rule gcal.AclRule
		args map[string]any
		want string
	}{
		{gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}},
			map[string]any{"who": "colleague@example.test", "role": "owner"}, "changes their access from reader"},
		{gcal.AclRule{ID: "domain:example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeDomain, Value: "example.test"}},
			map[string]any{"who": "example.test", "role": "writer"}, "let everyone at `example.test`"},
	} {
		p := &person{action: "decline"}
		cs, fake := connect(t, everything(), "2026-07-28", p)
		const cal = "team@group.calendar.example.test"
		fake.ACL[cal] = append(fake.ACL[cal], tc.rule)
		tc.args["calendar"], tc.args["notify"] = cal, "none"
		res := callTool(t, cs, &mcp.CallToolParams{Name: "share_calendar", Arguments: tc.args})
		qs := p.asked()
		if len(qs) != 1 || !res.IsError || len(fake.Wrote()) != 0 {
			t.Fatalf("%v: asked %d, %d writes: %s", tc.args, len(qs), len(fake.Wrote()), text(t, res))
		}
		if !strings.Contains(qs[0].Message, tc.want) {
			t.Errorf("%v: %s", tc.args, qs[0].Message)
		}
	}
}

// A tool that asks the person before every write carries Claude Code's
// requiresUserInteraction mark only for a client that cannot ask; with
// both, the person would answer twice for one call. The two are every
// tool here that both carries the mark and asks every time: a new name
// needs a look at whether it really asks every time. Each protocol lists
// on one server, the client that can ask first, so a mark dropped from
// the server's own tool rather than from a copy shows for the clients
// after it.
func TestTheMarkIsForAClientThatCannotAsk(t *testing.T) {
	marked := func(cs *mcp.ClientSession) string {
		t.Helper()
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tool := range res.Tools {
			if tool.Meta["anthropic/requiresUserInteraction"] == true {
				out = append(out, tool.Name)
			}
		}
		slices.Sort(out)
		return strings.Join(out, " ")
	}
	urlAlone := func(o *mcp.ClientOptions) {
		o.Capabilities = &mcp.ClientCapabilities{
			Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}},
		}
	}
	for _, protocol := range protocols {
		srv, _ := newServer(t, everything())
		for _, c := range []struct {
			client string
			p      *person
			opts   []func(*mcp.ClientOptions)
			want   string
		}{
			{"a client that can ask", &person{action: "accept"}, nil, ""},
			{"a client with no elicitation", nil, nil, "clear_calendar delete_calendar"},
			{"a client with URL elicitation alone", &person{action: "accept"},
				[]func(*mcp.ClientOptions){urlAlone}, "clear_calendar delete_calendar"},
		} {
			if got := marked(connectTo(t, srv, protocol, c.p, c.opts...)); got != c.want {
				t.Errorf("%s, %s: marked %q, want %q", protocol, c.client, got, c.want)
			}
		}
	}
}
