package plan_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/model"
	"github.com/mmedum/google-calendar-mcp/v3/internal/plan"
	"github.com/mmedum/google-calendar-mcp/v3/internal/recur"
	"github.com/mmedum/google-calendar-mcp/v3/internal/when"
)

func zone(t *testing.T) when.Zone {
	t.Helper()
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	return when.Zone{Loc: loc, Source: when.ZoneFromCall}
}

func ptr[T any](v T) *T { return &v }

// §4.1: a date is not a time, and a draft that mixes them is refused
// rather than resolved. This is the failure §3's first row names.
func TestADateIsNotATime(t *testing.T) {
	_, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Mixed"), Start: "2026-03-16", End: "2026-03-16T10:00:00+01:00", Zone: zone(t),
	})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("a date start with a timed end must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "all-day") {
		t.Errorf("the refusal should name the distinction: %v", err)
	}
}

// An all-day end is the LAST day, and Google's is the day after — so the
// wire value is one day later than the caller's. The renderer already
// shows it inclusive, so this is what makes the value read back the way
// it was written.
func TestAllDayEndIsInclusiveOnTheWayIn(t *testing.T) {
	e, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Holiday"), Start: "2026-03-20", End: "2026-03-20", Zone: zone(t),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if e.Start.Date != "2026-03-20" || e.End.Date != "2026-03-21" {
		t.Fatalf("got %s to %s, want 2026-03-20 to 2026-03-21 on the wire", e.Start.Date, e.End.Date)
	}
	if e.Start.TimeZone != "" || e.End.TimeZone != "" {
		t.Error("an all-day event carries no zone; there is no instant for one to act on (§4.1)")
	}
}

// §4.1: every timed write carries its IANA zone, recurring or not, so
// the same path is exercised every time rather than only on the rare one.
func TestEveryTimedWriteCarriesItsZone(t *testing.T) {
	e, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Sync"), Start: "2026-03-16T09:00:00+01:00", End: "2026-03-16T09:15:00+01:00",
		Zone: zone(t),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if e.Start.TimeZone != "Europe/Copenhagen" || e.End.TimeZone != "Europe/Copenhagen" {
		t.Fatalf("got %q/%q, want the IANA zone on both ends", e.Start.TimeZone, e.End.TimeZone)
	}
}

func TestInsertRefusesAnEndBeforeItsStart(t *testing.T) {
	_, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Backwards"), Start: "2026-03-16T10:00:00+01:00", End: "2026-03-16T09:00:00+01:00",
		Zone: zone(t),
	})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid], got %v", err)
	}
}

// A rule RFC 5545 forbids is refused here rather than at Google, where
// the answer names neither the field nor the rule.
//
// What is NOT refused is a rule this server cannot EXPAND: §6.4 sends
// the caller's string, and a FREQ Google supports and internal/recur
// does not is Google's to accept. Refusing it would be this server
// inventing a constraint.
func TestInsertRefusesARuleRFC5545Forbids(t *testing.T) {
	_, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Broken"), Start: "2026-03-16T09:00:00+01:00", End: "2026-03-16T10:00:00+01:00",
		Zone: zone(t), Recurrence: ptr([]string{"RRULE:FREQ=WEEKLY;COUNT=10;UNTIL=20260501T000000Z"}),
	})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("COUNT and UNTIL together must be refused before it is sent, got %v", err)
	}
	if !strings.Contains(err.Error(), "COUNT") {
		t.Errorf("the refusal should name what is wrong with the rule: %v", err)
	}
}

// An empty, non-nil recurrence list is how a series stops repeating.
func TestAnEmptyRecurrenceEndsTheRepetition(t *testing.T) {
	before := gcal.Event{Summary: "Weekly", Recurrence: []string{"RRULE:FREQ=WEEKLY;COUNT=10"}}
	p, changes, err := plan.Patch(before, plan.Draft{Recurrence: ptr([]string{}), Zone: zone(t)})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if p.Recurrence == nil {
		t.Fatal("clearing the recurrence must be sent, not omitted")
	}
	if len(*p.Recurrence) != 0 {
		t.Fatalf("got %v, want an empty list", *p.Recurrence)
	}
	if len(changes) != 1 || changes[0].Field != "recurrence" {
		t.Fatalf("got changes %+v, want one recurrence change", changes)
	}
}

// §4.4: a patch carries only what changed, and a field rewritten to its
// own value is not sent at all — it would move the etag under everybody
// else holding one.
func TestPatchSendsOnlyWhatChanged(t *testing.T) {
	before := gcal.Event{
		Summary: "Morning sync", Description: "as before", Location: "Room 1",
		Start: &gcal.EventDateTime{DateTime: "2026-03-16T09:00:00+01:00", TimeZone: "Europe/Copenhagen"},
		End:   &gcal.EventDateTime{DateTime: "2026-03-16T09:15:00+01:00", TimeZone: "Europe/Copenhagen"},
	}
	p, changes, err := plan.Patch(before, plan.Draft{
		Title:       ptr("Morning sync"), // unchanged
		Location:    ptr("Room 2"),
		Description: ptr(""), // cleared
		Zone:        zone(t),
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if p.Summary != nil {
		t.Error("the title was rewritten to itself and must not be sent")
	}
	if p.Location == nil || *p.Location != "Room 2" {
		t.Error("the location change is missing from the patch")
	}
	// The pointer is what makes "clear it" sayable at all: an empty
	// string on Event itself is dropped by omitempty.
	if p.Description == nil || *p.Description != "" {
		t.Error("clearing the description must be sent as an empty value, not omitted")
	}
	if p.Start != nil || p.End != nil {
		t.Error("nothing asked about the times, so they must not be in the patch")
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}
}

func TestPatchRefusesAChangeThatChangesNothing(t *testing.T) {
	before := gcal.Event{Summary: "Same"}
	_, _, err := plan.Patch(before, plan.Draft{Title: ptr("Same"), Zone: zone(t)})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid] for a no-op write, got %v", err)
	}
}

// §4.4: adding a guest is a read-modify-write on the array that was
// read, so a comment or an RSVP on somebody else's row survives.
func TestGuestsAreModifiedNotReplaced(t *testing.T) {
	before := gcal.Event{Attendees: []gcal.EventAttendee{
		{Email: "colleague@example.test", ResponseStatus: gcal.ResponseAccepted, Comment: "see you there"},
		{Email: "leaving@example.test"},
	}}
	p, changes, err := plan.Patch(before, plan.Draft{
		AddGuests: []string{"new@example.test"}, RemoveGuests: []string{"leaving@example.test"},
		Zone: zone(t),
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if p.Attendees == nil {
		t.Fatal("the guest list is missing from the patch")
	}
	got := *p.Attendees
	if len(got) != 2 {
		t.Fatalf("got %d attendees, want 2: %+v", len(got), got)
	}
	if got[0].ResponseStatus != gcal.ResponseAccepted || got[0].Comment != "see you there" {
		t.Errorf("the surviving guest lost their response or comment: %+v", got[0])
	}
	if got[1].Email != "new@example.test" {
		t.Errorf("the new guest is missing: %+v", got)
	}
	// Counts in the change line, never addresses (§9).
	for _, c := range changes {
		if strings.Contains(c.To, "@") || strings.Contains(c.From, "@") {
			t.Errorf("a change line leaked an address: %+v", c)
		}
	}
}

// A truncated guest list cannot be written back without dropping the
// guests Google did not show.
func TestATruncatedGuestListBlocksAGuestWrite(t *testing.T) {
	before := gcal.Event{
		Attendees:        []gcal.EventAttendee{{Email: "one@example.test"}},
		AttendeesOmitted: true,
	}
	_, _, err := plan.Patch(before, plan.Draft{AddGuests: []string{"new@example.test"}, Zone: zone(t)})
	if !errors.Is(err, plan.ErrBlocked) {
		t.Fatalf("want [blocked] on a truncated guest list, got %v", err)
	}
}

func TestSomethingThatIsNotAnAddressIsRefused(t *testing.T) {
	_, _, err := plan.Patch(gcal.Event{Summary: "x"},
		plan.Draft{AddGuests: []string{"Alice"}, Zone: zone(t)})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid] for a name where an address belongs, got %v", err)
	}
}

// A one-sided time change must keep the event's own kind: an all-day
// event with a timed end is not a thing the API can hold.
func TestAOneSidedTimeChangeKeepsTheEventsKind(t *testing.T) {
	allDay := gcal.Event{
		Start: &gcal.EventDateTime{Date: "2026-03-20"},
		End:   &gcal.EventDateTime{Date: "2026-03-21"},
	}
	_, _, err := plan.Patch(allDay, plan.Draft{End: "2026-03-21T10:00:00+01:00", Zone: zone(t)})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid] for a timed end on an all-day event, got %v", err)
	}
	// Both sides together DO change which it is.
	p, _, err := plan.Patch(allDay, plan.Draft{
		Start: "2026-03-20T09:00:00+01:00", End: "2026-03-20T10:00:00+01:00", Zone: zone(t),
	})
	if err != nil {
		t.Fatalf("passing both ends should convert it: %v", err)
	}
	if p.Start == nil || p.Start.DateTime == "" {
		t.Error("the converted start is missing from the patch")
	}
}

// §4.2: no scope, no write — and the refusal names the three choices and
// what each would do here.
func TestScopeIsRequiredOnARecurringEvent(t *testing.T) {
	e := model.Event{Recurrence: []string{"RRULE:FREQ=WEEKLY;COUNT=10"}}
	_, err := plan.Scope("", e, func() string { return "this series has 10 occurrences" })
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid], got %v", err)
	}
	for _, s := range recur.Scopes {
		if !strings.Contains(err.Error(), string(s)) {
			t.Errorf("the refusal must name %q: %v", s, err)
		}
	}
	if !strings.Contains(err.Error(), "10 occurrences") {
		t.Errorf("the refusal must say how far `series` would reach: %v", err)
	}
}

// The reach costs a request, so it is only paid for on the refusal path.
func TestTheReachIsNotComputedWhenTheScopeIsGiven(t *testing.T) {
	called := false
	e := model.Event{Recurrence: []string{"RRULE:FREQ=WEEKLY;COUNT=10"}}
	got, err := plan.Scope("series", e, func() string { called = true; return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got != recur.ScopeSeries {
		t.Fatalf("got scope %q, want series", got)
	}
	if called {
		t.Error("counting a series' occurrences reads its parent; that request must not be spent on a write that succeeds")
	}
}

// An event that does not repeat needs no scope, and passing one is not
// an error: the caller believed it repeated, and the result says it does
// not.
func TestScopeIsNotDemandedOnASingleEvent(t *testing.T) {
	got, err := plan.Scope("", model.Event{Title: "one-off"}, nil)
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; want no scope and no error", got, err)
	}
}

// §2.8: there is no server-side "this and following", so where this
// server cannot build it out of two calls the operation does not exist.
func TestNoSplitRefusesThisAndFollowing(t *testing.T) {
	err := plan.NoSplit(recur.ScopeThisAndFollowing, "answering an invitation")
	if !errors.Is(err, plan.ErrUnsupported) {
		t.Fatalf("want [unsupported], got %v", err)
	}
	if !strings.Contains(err.Error(), "scope:instance") || !strings.Contains(err.Error(), "scope:series") {
		t.Errorf("the refusal must name what does work: %v", err)
	}
	for _, s := range []recur.Scope{recur.ScopeInstance, recur.ScopeSeries, ""} {
		if err := plan.NoSplit(s, "x"); err != nil {
			t.Errorf("NoSplit(%q) refused something it should allow: %v", s, err)
		}
	}
}

func TestEmptyDraftIsRecognized(t *testing.T) {
	if !(plan.Draft{}).Empty() {
		t.Error("a draft with nothing in it must report itself empty")
	}
	if (plan.Draft{Title: ptr("x")}).Empty() {
		t.Error("a draft with a title is not empty")
	}
}

// §4.1, inside the guard that enforces it. An RFC3339 string sorts by
// its text, and either side of a daylight-saving fold two strings carry
// different offsets — so comparing them refused a valid event and
// accepted an impossible one. Copenhagen falls back at 03:00 on
// 2026-10-25, which makes 02:45+02:00 EARLIER than 02:30+01:00.
func TestTimesAreComparedAsInstantsNotStrings(t *testing.T) {
	z := zone(t)
	forward := plan.Draft{
		Title: ptr("45 real minutes"), Zone: z,
		Start: "2026-10-25T02:45:00+02:00", End: "2026-10-25T02:30:00+01:00",
	}
	if _, err := plan.Insert("abcde12345", forward); err != nil {
		t.Fatalf("a 45-minute event across the fold was refused: %v", err)
	}
	backwards := plan.Draft{
		Title: ptr("backwards"), Zone: z,
		Start: "2026-10-25T02:30:00+01:00", End: "2026-10-25T02:45:00+02:00",
	}
	if _, err := plan.Insert("abcde12345", backwards); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("an event ending before it began was accepted: %v", err)
	}
}

// A patch that moves one end has to be crossed against the end that
// stays, or an event ends before it begins and nothing notices until
// Google refuses it.
func TestAOneSidedTimeChangeIsCheckedAgainstTheSideThatStays(t *testing.T) {
	before := gcal.Event{
		Start: &gcal.EventDateTime{DateTime: "2026-03-16T09:00:00+01:00", TimeZone: "Europe/Copenhagen"},
		End:   &gcal.EventDateTime{DateTime: "2026-03-16T09:30:00+01:00", TimeZone: "Europe/Copenhagen"},
	}
	_, _, err := plan.Patch(before, plan.Draft{End: "2026-03-16T08:00:00+01:00", Zone: zone(t)})
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("an end moved before the start that stays was accepted: %v", err)
	}
	// Moving it later is fine.
	if _, _, err := plan.Patch(before, plan.Draft{End: "2026-03-16T10:00:00+01:00", Zone: zone(t)}); err != nil {
		t.Fatalf("a legitimate one-sided change was refused: %v", err)
	}

	allDay := gcal.Event{
		Start: &gcal.EventDateTime{Date: "2026-03-20"},
		End:   &gcal.EventDateTime{Date: "2026-03-23"},
	}
	if _, _, err := plan.Patch(allDay, plan.Draft{End: "2026-03-19", Zone: zone(t)}); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("an all-day last day before the first day was accepted: %v", err)
	}
}

// A word that is not a scope is a mistake whatever it was aimed at. The
// old order looked at the event first and returned before ever reading
// the word, so a typo was accepted in silence on a non-repeating event.
func TestAMisspelledScopeIsRefusedEvenOnASingleEvent(t *testing.T) {
	_, err := plan.Scope("sereis", model.Event{Title: "one-off"}, nil)
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("a misspelled scope was ignored: %v", err)
	}
	// A VALID scope on a non-repeating event is still not an error: the
	// caller believed it repeated, and the result saying it does not is
	// the useful answer.
	if got, err := plan.Scope("series", model.Event{Title: "one-off"}, nil); err != nil || got != "" {
		t.Fatalf("got %q, %v; a valid scope on a single event is ignored, not refused", got, err)
	}
}

// TestAConferenceIsOnlyAttachedAtCreation: adding a Meet link to an
// event that already exists is a write this server does not make, and
// the refusal says so rather than the field being dropped in silence.
func TestAConferenceIsOnlyAttachedAtCreation(t *testing.T) {
	before := gcal.Event{ID: "abcdef0123456789", Summary: "Standing meeting"}
	_, _, err := plan.Patch(before, plan.Draft{Conference: true})
	if !errors.Is(err, plan.ErrUnsupported) {
		t.Fatalf("Patch with a conference gave %v, want ErrUnsupported", err)
	}

	e, err := plan.Insert("abcdef0123456789", plan.Draft{
		Title: ptr("Kickoff"), Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Zone: zone(t), Conference: true,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if len(e.ConferenceData) == 0 {
		t.Fatal("an insert that asked for a conference sent no conference data")
	}
	// The request id is the event id, so a retry cannot make a second
	// conference: Google ignores a repeated request id.
	if !strings.Contains(string(e.ConferenceData), `"requestId":"abcdef0123456789"`) {
		t.Fatalf("the request does not carry the event id: %s", e.ConferenceData)
	}
}

// A Gmail server hands an address over as an RFC 5322 mailbox, with a
// display name. It is taken as its bare address, which is all that goes
// on. A list in one entry is refused rather than split.
func TestAMailboxIsTakenAsItsBareAddress(t *testing.T) {
	for in, want := range map[string]string{
		`"Sample Person" <person@example.test>`:  "person@example.test",
		`"Person, Sample" <person@example.test>`: "person@example.test",
		`Sample Person <person@example.test>`:    "person@example.test",
		`<person@example.test>`:                  "person@example.test",
		` person@example.test `:                  "person@example.test",
	} {
		got, err := plan.Address(in, "a guest")
		if err != nil || got != want {
			t.Errorf("Address(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		`"One" <one@example.test>, "Two" <two@example.test>`: "holds 2 addresses in one entry",
		`one@example.test, two@example.test`:                 "holds 2 addresses in one entry",
		`Sample Person`:                                      "is not an email address",
		`person@localhost`:                                   "is not an email address",
	} {
		_, err := plan.Address(in, "a guest")
		if !errors.Is(err, plan.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("Address(%q): %v, want invalid saying %q", in, err, want)
		}
	}
}

// Reminders: the limits are Google's, refused before a request is made,
// and the edges of each are accepted.
func TestNewRemindersHoldsGooglesLimits(t *testing.T) {
	for _, c := range []struct {
		name         string
		defaults     bool
		popup, email *[]int
		want         string
	}{
		{"default with a list", true, ptr([]int{10}), nil, "one or the other"},
		{"six in all", false, ptr([]int{1, 2, 3}), ptr([]int{4, 5, 6}), "6 reminders given"},
		{"past four weeks", false, ptr([]int{40321}), nil, "has 40321"},
		{"before the start", false, nil, ptr([]int{-1}), "has -1"},
		{"twice", false, ptr([]int{10, 10}), nil, "10 twice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := plan.NewReminders(c.defaults, c.popup, c.email)
			if !errors.Is(err, plan.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want [invalid] saying %q", err, c.want)
			}
		})
	}

	r, err := plan.NewReminders(false, ptr([]int{40320, 0, 30}), ptr([]int{1440, 60}))
	if err != nil {
		t.Fatalf("five reminders from 0 to 40320 minutes were refused: %v", err)
	}
	if fmt.Sprint(r.Popup, r.Email) != "[0 30 40320] [60 1440]" {
		t.Fatalf("got %v %v, want each list in ascending order", r.Popup, r.Email)
	}
	if r, err := plan.NewReminders(false, nil, nil); r != nil || err != nil {
		t.Fatalf("no reminder input gave %+v, %v; want nil, nil", r, err)
	}
}

// Giving one list replaces the whole set: the reminders of the other
// kind go, and the wire carries the overrides in full.
func TestRemindersReplaceTheWholeSet(t *testing.T) {
	before := gcal.Event{ID: "abcde12345", Reminders: &gcal.EventReminders{
		Overrides: []gcal.EventReminder{{Method: gcal.ReminderEmail, Minutes: 1440}},
	}}
	r, err := plan.NewReminders(false, ptr([]int{10}), nil)
	if err != nil {
		t.Fatal(err)
	}
	p, changes, err := plan.Patch(before, plan.Draft{Reminders: r})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	body, _ := json.Marshal(p)
	if string(body) != `{"reminders":{"useDefault":false,"overrides":[{"method":"popup","minutes":10}]}}` {
		t.Fatalf("got %s", body)
	}
	if len(changes) != 1 || changes[0] != (plan.Change{Field: "reminders", From: "email 1440 min", To: "popup 10 min"}) {
		t.Fatalf("got %+v", changes)
	}

	// Back to the calendar's own: overrides is sent as null, which is how
	// a patch deletes a field rather than merging into it.
	def, _ := plan.NewReminders(true, nil, nil)
	p, _, err = plan.Patch(before, plan.Draft{Reminders: def})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if body, _ := json.Marshal(p); string(body) != `{"reminders":{"useDefault":true,"overrides":null}}` {
		t.Fatalf("got %s", body)
	}
}

// The same reminders in another order are no change.
func TestTheSameRemindersAreNoChange(t *testing.T) {
	before := gcal.Event{ID: "abcde12345", Reminders: &gcal.EventReminders{Overrides: []gcal.EventReminder{
		{Method: gcal.ReminderEmail, Minutes: 60}, {Method: gcal.ReminderPopup, Minutes: 30},
		{Method: gcal.ReminderPopup, Minutes: 10},
	}}}
	r, _ := plan.NewReminders(false, ptr([]int{30, 10}), ptr([]int{60}))
	if _, _, err := plan.Patch(before, plan.Draft{Reminders: r}); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("got %v, want nothing to change", err)
	}
}

// A draft that touches only reminders is the one that reaches nobody.
func TestOnlyRemindersIsOnlyReminders(t *testing.T) {
	r, _ := plan.NewReminders(true, nil, nil)
	if !(plan.Draft{Reminders: r}).OnlyReminders() {
		t.Error("a reminders-only draft is not recognized")
	}
	if (plan.Draft{Reminders: r, Title: ptr("x")}).OnlyReminders() {
		t.Error("a draft with a title is not reminders only")
	}
	if (plan.Draft{Reminders: r, Visibility: ptr("private")}).OnlyReminders() {
		t.Error("visibility is the event's, not this account's")
	}
	if (plan.Draft{}).OnlyReminders() {
		t.Error("an empty draft is not reminders only")
	}
}

// PersonalNotification sends nothing whatever notify says, and still
// refuses a value that is not a choice.
func TestAPersonalWriteAsksForNoNotification(t *testing.T) {
	d, err := plan.PersonalNotification("all")
	if err != nil {
		t.Fatal(err)
	}
	if d.SendUpdatesFor() != "" {
		t.Fatalf("sendUpdates is %q, want none sent", d.SendUpdatesFor())
	}
	if _, err := plan.PersonalNotification("sometimes"); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("got %v, want a typo refused", err)
	}
}

// Visibility takes three values; confidential is refused with what to
// pass instead.
func TestVisibilityTakesThreeValues(t *testing.T) {
	before := gcal.Event{ID: "abcde12345"}
	p, changes, err := plan.Patch(before, plan.Draft{Visibility: ptr("Private")})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if p.Visibility == nil || *p.Visibility != "private" ||
		changes[0] != (plan.Change{Field: "visibility", From: "default", To: "private"}) {
		t.Fatalf("got %+v %+v", p.Visibility, changes)
	}
	for v, want := range map[string]string{"confidential": "Pass private", "secret": "default, public or private"} {
		if _, _, err := plan.Patch(before, plan.Draft{Visibility: ptr(v)}); !errors.Is(err, plan.ErrInvalid) ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want [invalid] saying %q", v, err, want)
		}
	}
}

// Google ignores a less restrictive visibility on one occurrence and
// applies a more restrictive one to the whole series.
func TestVisibilityOnOneOccurrence(t *testing.T) {
	for _, c := range []struct{ from, to string }{
		{"private", "public"}, {"private", "default"}, {"", "public"}, {"confidential", "default"},
	} {
		if _, err := plan.InstanceVisibility(c.from, c.to); !errors.Is(err, plan.ErrUnsupported) {
			t.Errorf("%q to %q: got %v, want [unsupported]", c.from, c.to, err)
		}
	}
	note, err := plan.InstanceVisibility("public", "private")
	if err != nil || !strings.Contains(note, "makes the whole series private") {
		t.Fatalf("got %q, %v; want a note that the series changes", note, err)
	}
	if note, err := plan.InstanceVisibility("private", "private"); note != "" || err != nil {
		t.Fatalf("an unchanged visibility got %q, %v", note, err)
	}
}

// A guest permission is compared with Google's default when the event
// does not carry it, so setting the default is no change.
func TestGuestPermissionsAgainstGooglesDefaults(t *testing.T) {
	before := gcal.Event{ID: "abcde12345"}
	p, changes, err := plan.Patch(before, plan.Draft{
		GuestsCanInviteOthers: ptr(false), GuestsCanSeeOtherGuests: ptr(true), GuestsCanModify: ptr(true),
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	body, _ := json.Marshal(p)
	if string(body) != `{"guestsCanModify":true,"guestsCanInviteOthers":false}` {
		t.Fatalf("got %s", body)
	}
	if len(changes) != 2 {
		t.Fatalf("got %+v, want two changes", changes)
	}
}

// An insert carries all of them.
func TestInsertCarriesRemindersVisibilityAndPermissions(t *testing.T) {
	r, _ := plan.NewReminders(false, ptr([]int{}), nil)
	e, err := plan.Insert("abcde12345", plan.Draft{
		Title: ptr("Quiet"), Start: "2026-03-20", End: "2026-03-20",
		Reminders: r, Visibility: ptr("public"), GuestsCanSeeOtherGuests: ptr(false),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if e.Reminders == nil || e.Reminders.UseDefault || len(e.Reminders.Overrides) != 0 {
		t.Fatalf("an empty list did not mean no reminders: %+v", e.Reminders)
	}
	if e.Visibility != "public" || e.GuestsCanSeeOtherGuests == nil || *e.GuestsCanSeeOtherGuests {
		t.Fatalf("got visibility %q and see-guests %v", e.Visibility, e.GuestsCanSeeOtherGuests)
	}
}
