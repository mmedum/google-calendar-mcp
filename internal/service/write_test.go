package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/plan"
	"github.com/mmedum/google-calendar-mcp/v3/internal/render"
	"github.com/mmedum/google-calendar-mcp/v3/internal/service"
)

// writeSeed is a calendar whose event ids are legal base32hex (§2.11).
//
// Seed's ids are not, and that is fine for reads — but an occurrence is
// addressed as "series id, underscore, scheduled start", so a fixture
// with a hyphen in its ids cannot exercise the address §6.2 is built on.
// A fake that made that path untestable is how it would have shipped
// untested.
func writeSeed(t *testing.T) (*service.Service, *caltest.Server) {
	t.Helper()
	const tz = "Europe/Copenhagen"
	fake := caltest.New()
	fake.AddCalendar("me@example.test", "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddCalendar("team@group.calendar.example.test", "Sample Team", tz, gcal.RoleWriter, false)
	fake.AddCalendar("readonly@group.calendar.example.test", "Sample Readonly", tz, gcal.RoleReader, false)

	fake.AddEvent("me@example.test", caltest.Timed("evsolo00001", "Solo thinking",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:30:00+01:00", tz))

	guests := caltest.Timed("evguests001", "Project review",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00", tz)
	guests.Organizer = &gcal.EventPerson{Email: "me@example.test", Self: true}
	guests.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true, ResponseStatus: gcal.ResponseAccepted},
		{Email: "colleague@example.test", ResponseStatus: gcal.ResponseAccepted, Comment: "looking forward"},
		{Email: "partner@elsewhere.test", ResponseStatus: gcal.ResponseNeedsAction},
	}
	fake.AddEvent("me@example.test", guests)

	// A weekly series and two of its occurrences. 24 March is still CET
	// and 31 March is CEST, so the two occurrence ids differ by an hour
	// in UTC while both are 14:00 on the wall clock — which is the whole
	// reason §4.1 carries a zone rather than an offset.
	fake.AddEvent("me@example.test", caltest.Recurring("evseries001", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00", tz,
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=8"))
	fake.AddEvent("me@example.test", caltest.Instance("evseries001_20260324T130000Z", "evseries001",
		"Weekly review", "2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00", tz,
		"2026-03-24T14:00:00+01:00"))
	fake.AddEvent("me@example.test", caltest.Instance("evseries001_20260331T120000Z", "evseries001",
		"Weekly review", "2026-03-31T14:00:00+02:00", "2026-03-31T15:00:00+02:00", tz,
		"2026-03-31T14:00:00+02:00"))

	// An invitation this account has not answered, organized elsewhere.
	invite := caltest.Timed("evinvite001", "Somebody else's meeting",
		"2026-03-19T13:00:00+01:00", "2026-03-19T14:00:00+01:00", tz)
	invite.Organizer = &gcal.EventPerson{Email: "host@example.test"}
	invite.Attendees = []gcal.EventAttendee{
		{Email: "host@example.test", Organizer: true, ResponseStatus: gcal.ResponseAccepted},
		{Email: "me@example.test", Self: true, ResponseStatus: gcal.ResponseNeedsAction},
		{Email: "other@example.test", ResponseStatus: gcal.ResponseDeclined, Comment: "clash"},
	}
	fake.AddEvent("me@example.test", invite)

	fake.Settings = []gcal.Setting{{ID: gcal.SettingTimezone, Value: tz}}
	return newService(t, fake), fake
}

// accepting is a person who confirms every question, for tests of what a
// write does once it is confirmed.
type accepting struct{}

func (accepting) Ask(context.Context, render.Question) error { return nil }

// accepted is a context whose asking writes are confirmed.
func accepted() context.Context { return service.WithAsker(context.Background(), accepting{}) }

func classOf(t *testing.T, err error) gapi.Class {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	cls, ok := gapi.ClassOf(err)
	if !ok {
		t.Fatalf("error carries no class: %v", err)
	}
	return cls
}

// ------------------------------------------------------------- creating

func TestCreateEventMintsALegalIDAndSendsTheZone(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "New thing",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if out.After == nil {
		t.Fatal("a create must report what it made")
	}
	// §2.11: the id is the server's, so a retry collides rather than
	// creating a second meeting.
	if err := gcal.ValidEventID(out.After.ID); err != nil {
		t.Fatalf("the generated id is not one Google would accept: %v", err)
	}
	writes := fake.Wrote()
	if len(writes) != 1 || writes[0].Method != "insert" {
		t.Fatalf("got writes %+v, want one insert", writes)
	}
	// §4.3.2: nobody to reach, so no sendUpdates was invented.
	if writes[0].SendUpdates != "" {
		t.Errorf("sendUpdates %q sent for an event with no guests", writes[0].SendUpdates)
	}
}

func TestCreateEventRefusesWithoutNotifyWhenItHasGuests(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "With people",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: []string{"colleague@example.test"},
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must happen before anything is written")
	}
}

// §4.3.4: `none` is refused when a guest is outside the organizer's
// domain, because that guest may have no calendar for the event to
// appear in (spike B).
func TestCreateEventRefusesNoneForAnOutsideGuest(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "With an outsider",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: []string{"partner@elsewhere.test"}, Notify: "none",
	})
	if got := classOf(t, err); got != gapi.ClassBlocked {
		t.Fatalf("got [%s], want [blocked]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("nothing may be written when a guard refuses")
	}
}

func TestCreateEventPassesTheNotifyChoiceThrough(t *testing.T) {
	svc, fake := writeSeed(t)
	if _, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "With people",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: []string{"partner@elsewhere.test"}, Notify: "external_only",
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if got := fake.Wrote()[0].SendUpdates; got != gcal.SendUpdatesExternalOnly {
		t.Fatalf("sent sendUpdates=%q, want %q", got, gcal.SendUpdatesExternalOnly)
	}
}

// A room is booked as a resource and reaches nobody, whether it comes as
// a room or as a guest address; so notify is not asked for, and none is
// not refused, on the create or on any write after it.
func TestCreateEventBooksRoomsWithoutReachingAnybody(t *testing.T) {
	const room = "room-sample@resource.calendar.google.com"
	for _, o := range []service.CreateOptions{
		{Rooms: []string{room}},
		{Guests: []string{room}, Notify: "none"},
	} {
		svc, fake := writeSeed(t)
		o.Calendar, o.Title, o.Start, o.End = "primary", "In a room", "2026-04-01T09:00:00+02:00", "2026-04-01T10:00:00+02:00"
		out, err := svc.CreateEvent(context.Background(), o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		created := fake.Events["me@example.test"][out.After.ID]
		if len(created.Attendees) != 1 || created.Attendees[0].Email != room || !created.Attendees[0].Resource {
			t.Fatalf("%+v: attendees %+v", o, created.Attendees)
		}
		if got := fake.Wrote()[0].SendUpdates; got != o.Notify {
			t.Fatalf("%+v: sent sendUpdates=%q", o, got)
		}
		if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
			Calendar: "primary", EventID: out.After.ID, Title: ptr("Renamed"), Notify: "none",
		}); err != nil {
			t.Fatalf("%+v: a later update with none: %v", o, err)
		}
	}
}

// A person's address is somebody the write reaches whichever list it
// comes in: passed as a room, it still asks for notify and refuses none
// for somebody outside the domain (§4.3.4).
func TestAPersonPassedAsARoomIsStillReached(t *testing.T) {
	outside := []string{"partner@elsewhere.test"}
	for _, notify := range []string{"", "none"} {
		svc, fake := writeSeed(t)
		_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
			Calendar: "primary", Title: "Not a room", Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			Rooms: outside, Notify: notify,
		})
		want := map[string]gapi.Class{"": gapi.ClassInvalid, "none": gapi.ClassBlocked}[notify]
		if classOf(t, err) != want {
			t.Fatalf("create with notify %q: %v, want %s", notify, err, want)
		}
		_, err = svc.UpdateEvent(context.Background(), service.UpdateOptions{
			Calendar: "primary", EventID: "evsolo00001", AddRooms: outside, Notify: notify,
		})
		if classOf(t, err) != want {
			t.Fatalf("update with notify %q: %v, want %s", notify, err, want)
		}
		if len(fake.Wrote()) != 0 {
			t.Fatalf("refusals wrote %+v", fake.Wrote())
		}
	}
}

// Adding a guest reaches that guest, on an event that had nobody: notify
// is required, and none is refused for somebody outside the domain.
// Adding a room is not reaching anybody.
func TestUpdateEventCountsTheGuestsItAdds(t *testing.T) {
	svc, fake := writeSeed(t)
	add := func(notify string, guests, rooms []string) error {
		_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
			Calendar: "primary", EventID: "evsolo00001", AddGuests: guests, AddRooms: rooms, Notify: notify,
		})
		return err
	}
	outside := []string{"partner@elsewhere.test"}
	if err := add("", outside, nil); classOf(t, err) != gapi.ClassInvalid {
		t.Fatalf("no notify: %v, want [invalid]", err)
	}
	if err := add("none", outside, nil); classOf(t, err) != gapi.ClassBlocked {
		t.Fatalf("none: %v, want [blocked]", err)
	}
	if len(fake.Wrote()) != 0 {
		t.Fatalf("refusals wrote %+v", fake.Wrote())
	}
	// Splitting a series at an occurrence is a write that adds them too.
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260331T120000Z", Scope: "this_and_following",
		AddGuests: outside, Notify: "none",
	})
	if classOf(t, err) != gapi.ClassBlocked {
		t.Fatalf("none on this_and_following: %v, want [blocked]", err)
	}
	if err := add("", nil, []string{"room-sample@resource.calendar.google.com"}); err != nil {
		t.Fatalf("adding a room: %v", err)
	}
	if got := fake.Events["me@example.test"]["evsolo00001"].Attendees; len(got) != 1 || !got[0].Resource {
		t.Fatalf("the room went in as %+v", got)
	}
}

// An optional guest is a guest who may skip the meeting, and Google
// mails them like any other: notify is required, none is refused for
// somebody outside the domain, and the attendee goes to Google marked
// optional, on a create and on an update.
func TestOptionalGuestsAreReachedLikeGuests(t *testing.T) {
	svc, fake := writeSeed(t)
	outside := []string{"partner@elsewhere.test"}
	create := func(notify string) (render.WriteReport, error) {
		return svc.CreateEvent(context.Background(), service.CreateOptions{
			Calendar: "primary", Title: "Optional", Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			OptionalGuests: outside, Notify: notify,
		})
	}
	update := func(notify string) error {
		_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
			Calendar: "primary", EventID: "evsolo00001", AddOptionalGuests: outside, Notify: notify,
		})
		return err
	}
	for notify, want := range map[string]gapi.Class{"": gapi.ClassInvalid, "none": gapi.ClassBlocked} {
		if _, err := create(notify); classOf(t, err) != want {
			t.Fatalf("create with notify %q: %v, want [%s]", notify, err, want)
		}
		if err := update(notify); classOf(t, err) != want {
			t.Fatalf("update with notify %q: %v, want [%s]", notify, err, want)
		}
	}
	if len(fake.Wrote()) != 0 {
		t.Fatalf("refusals wrote %+v", fake.Wrote())
	}

	out, err := create("external_only")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := update("external_only"); err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, id := range []string{out.After.ID, "evsolo00001"} {
		got := fake.Events["me@example.test"][id].Attendees
		if len(got) != 1 || got[0].Email != "partner@elsewhere.test" || !got[0].Optional {
			t.Fatalf("%s: attendees %+v, want the one guest, optional", id, got)
		}
	}
	for _, w := range fake.Wrote() {
		if w.SendUpdates != gcal.SendUpdatesExternalOnly {
			t.Fatalf("%s sent sendUpdates=%q, want %q", w.Method, w.SendUpdates, gcal.SendUpdatesExternalOnly)
		}
	}
}

// Adding is add only. An address already on the event keeps the role it
// has, and the result names it rather than skipping it in silence; when
// every address is already there, there is nothing to write.
func TestAnAddressAlreadyOnTheEventIsReportedNotSkipped(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evguests001", Notify: "all",
		AddOptionalGuests: []string{"Colleague@example.test", "newcomer@example.test"},
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	optional := map[string]bool{}
	for _, a := range fake.Events["me@example.test"]["evguests001"].Attendees {
		optional[a.Email] = a.Optional
	}
	if got, ok := optional["colleague@example.test"]; !ok || got {
		t.Fatalf("the guest already there was changed or dropped: %+v", optional)
	}
	if !optional["newcomer@example.test"] {
		t.Fatalf("the new guest was not added as optional: %+v", optional)
	}
	want := "Already on the event, so left as they were: Colleague@example.test."
	if !strings.Contains(strings.Join(out.Notes, "\n"), want) {
		t.Fatalf("the result does not name the address already there: %v", out.Notes)
	}

	_, err = svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evguests001", Notify: "all",
		AddOptionalGuests: []string{"partner@elsewhere.test"},
	})
	if classOf(t, err) != gapi.ClassInvalid || !strings.Contains(err.Error(), "already on the event") {
		t.Fatalf("adding only addresses already there: %v, want [invalid] naming why", err)
	}
}

// A split carries the series' guests to the new series, so an address it
// is asked to add that the series already has is named there too.
func TestASplitNamesAnAddressTheSeriesAlreadyHas(t *testing.T) {
	svc, _ := writeSeed(t)
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", Scope: "series", Notify: "all",
		AddGuests: []string{"colleague@example.test"},
	}); err != nil {
		t.Fatalf("adding a guest to the series: %v", err)
	}
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260331T120000Z", Scope: "this_and_following",
		Notify: "all", Title: ptr("Later review"), AddOptionalGuests: []string{"colleague@example.test"},
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if !strings.Contains(strings.Join(out.Notes, "\n"), "left as they were: colleague@example.test") {
		t.Fatalf("the split does not name the address already there: %v", out.Notes)
	}
}

// A room cannot be optional, and one address cannot be asked for in two
// roles at once, guest and room included. Each is refused before
// anything is written.
func TestOptionalGuestsRefuseRoomsAndDoubleRoles(t *testing.T) {
	svc, fake := writeSeed(t)
	for _, c := range []struct {
		o    service.CreateOptions
		want string
	}{
		{service.CreateOptions{OptionalGuests: []string{"room-sample@resource.calendar.google.com"}},
			"a room cannot be an optional guest"},
		{service.CreateOptions{Guests: []string{"colleague@example.test"}, OptionalGuests: []string{"COLLEAGUE@example.test"}},
			"given as both a guest and an optional guest"},
		{service.CreateOptions{Guests: []string{"Partner <partner@elsewhere.test>"}, Rooms: []string{"partner@elsewhere.test"}},
			"given as both a guest and a room"},
		{service.CreateOptions{Guests: []string{"room-sample@resource.calendar.google.com"},
			Rooms: []string{"room-sample@resource.calendar.google.com"}},
			"given as both a guest and a room"},
	} {
		o := c.o
		o.Calendar, o.Title, o.Start, o.End, o.Notify = "primary", "Refused", "2026-04-01T09:00:00+02:00", "2026-04-01T10:00:00+02:00", "all"
		_, err := svc.CreateEvent(context.Background(), o)
		if classOf(t, err) != gapi.ClassInvalid || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%+v: %v, want [invalid] saying %q", c.o, err, c.want)
		}
	}
	if len(fake.Wrote()) != 0 {
		t.Fatalf("refusals wrote %+v", fake.Wrote())
	}
}

// A guest given as a mailbox, the way a Gmail server hands one over, is
// invited, counted and removed by its bare address. The reach count reads
// the domain the address has rather than the bracket after it, so none
// is allowed for a guest inside the domain and refused for one outside.
// The display name goes nowhere.
func TestAGuestGivenAsAMailboxIsItsAddress(t *testing.T) {
	svc, fake := writeSeed(t)
	create := func(guest, notify string) (render.WriteReport, error) {
		return svc.CreateEvent(context.Background(), service.CreateOptions{
			Calendar: "primary", Title: "Mailbox", Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			Guests: []string{guest}, Notify: notify,
		})
	}
	out, err := create(`"Sample Colleague" <colleague@example.test>`, "none")
	if err != nil {
		t.Fatalf("a guest inside the domain, given as a mailbox, with none: %v", err)
	}
	got := fake.Events["me@example.test"][out.After.ID].Attendees
	if len(got) != 1 || got[0].Email != "colleague@example.test" {
		t.Fatalf("attendees %+v, want the bare address", got)
	}
	body, err := json.Marshal(service.NewWriteResult(out))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body)+out.Text(), "Sample Colleague") {
		t.Fatal("the display name reached the result")
	}

	if _, err := create(`"Sample Partner" <partner@elsewhere.test>`, "none"); classOf(t, err) != gapi.ClassBlocked {
		t.Fatalf("none for an outside guest given as a mailbox: %v, want [blocked]", err)
	}
	_, err = create(`one@example.test, two@example.test`, "all")
	if classOf(t, err) != gapi.ClassInvalid || !strings.Contains(err.Error(), "holds 2 addresses in one entry") {
		t.Fatalf("two addresses in one entry: %v, want [invalid]", err)
	}

	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evguests001", Notify: "all",
		RemoveGuests: []string{`Sample Partner <partner@elsewhere.test>`},
	}); err != nil {
		t.Fatalf("removing a guest given as a mailbox: %v", err)
	}
	for _, a := range fake.Events["me@example.test"]["evguests001"].Attendees {
		if a.Email == "partner@elsewhere.test" {
			t.Fatal("the guest given as a mailbox was not removed")
		}
	}
}

// §4.3.5: dry_run shows the blast radius and writes nothing.
func TestDryRunWritesNothing(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Not really",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: []string{"colleague@example.test", "partner@elsewhere.test"}, Notify: "all",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if len(fake.Wrote()) != 0 {
		t.Fatalf("a dry run wrote something: %+v", fake.Wrote())
	}
	text := out.Text()
	if !strings.Contains(text, "DRY RUN") {
		t.Errorf("a dry run must say so in the text a model sees:\n%s", text)
	}
	// How many, and how many of them are outside the domain.
	if !strings.Contains(text, "2 guests") || !strings.Contains(text, "1 of them outside") {
		t.Errorf("the blast radius is missing:\n%s", text)
	}
}

// §2.11 and §11: an insert that failed without an answer this server can
// trust is ambiguous_outcome, never a silent retry.
func TestAnInsertThatMayHaveLandedIsAmbiguous(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Fail["POST /calendars/me@example.test/events"] = 500
	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Maybe",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
	})
	if got := classOf(t, err); got != gapi.ClassAmbiguousOutcome {
		t.Fatalf("got [%s], want [ambiguous_outcome]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "get_event") {
		t.Errorf("the class exists so somebody can look; the message must say how: %v", err)
	}
}

// A 400 definitely did not land, so it is not ambiguous.
func TestAnInsertGoogleRefusedIsNotAmbiguous(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Fail["POST /calendars/me@example.test/events"] = 400
	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "No",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
}

// ------------------------------------------------------------- updating

func TestUpdatePatchesUnderIfMatch(t *testing.T) {
	svc, fake := writeSeed(t)
	before, _, err := svc.GetEvent(context.Background(), "primary", "evsolo00001", "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 2"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].Method != "patch" {
		t.Fatalf("got %+v, want one patch", w)
	}
	if w[0].IfMatch != before.ETag {
		t.Fatalf("If-Match was %q, want the etag the read produced (%q)", w[0].IfMatch, before.ETag)
	}
	if len(out.Changes) != 1 || out.Changes[0].Field != "location" {
		t.Fatalf("got changes %+v, want one location change", out.Changes)
	}
	if out.Before == nil || out.After == nil {
		t.Fatal("§4.9: a write reports what it looked like before and after")
	}
}

// §4.4: the etag the caller decided on is held to. If it moved, the
// write is [stale] and no request is spent.
func TestAnEtagThatMovedIsStale(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 2"),
		ETag: `"something-older"`,
	})
	if got := classOf(t, err); got != gapi.ClassStale {
		t.Fatalf("got [%s], want [stale]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("a stale write must be refused before it is sent")
	}
}

// And If-Match: * is available, is not the default, and says so.
func TestForceWritesWithAStarAndSaysSo(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 2"), Force: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got := fake.Wrote()[0].IfMatch; got != "*" {
		t.Fatalf("If-Match was %q, want *", got)
	}
	if !strings.Contains(out.Text(), "If-Match: *") {
		t.Errorf("forcing a write must be visible in the result:\n%s", out.Text())
	}
}

// §4.2: the refusal names the three choices and how far `series` reaches.
func TestUpdateRefusesARecurringEventWithoutAScope(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", Title: ptr("Renamed"),
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "8 occurrences") {
		t.Errorf("the refusal must say how far series would reach: %v", err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("nothing may be written while the caller has not chosen")
	}
}

// scope:series on an occurrence follows it up to the parent, which is
// the difference between renaming one meeting and renaming six months.
func TestScopeSeriesFollowsAnInstanceToItsParent(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z",
		Scope: "series", Title: ptr("Weekly sync"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].EventID != "evseries001" {
		t.Fatalf("patched %+v, want the series parent evseries001", w)
	}
	if !strings.Contains(out.Text(), "evseries001") {
		t.Errorf("the result must say it wrote the series:\n%s", out.Text())
	}
}

// scope:instance against the series id cannot know which occurrence.
func TestScopeInstanceAgainstTheSeriesIDIsRefused(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", Scope: "instance", Title: ptr("Renamed"),
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "original_start") {
		t.Errorf("the refusal must name the way to say which occurrence: %v", err)
	}
}

// §6.2: an occurrence is addressable as "the series, that scheduled
// start" — the address that survives somebody moving it.
func TestAnOccurrenceIsAddressableByItsScheduledStart(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", OriginalStart: "2026-03-24T14:00:00+01:00",
		Scope: "instance", Location: ptr("Room 3"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got := fake.Wrote()[0].EventID; got != "evseries001_20260324T130000Z" {
		t.Fatalf("patched %q, want the occurrence for 24 March", got)
	}
	if !strings.Contains(out.Text(), "Addressed the occurrence") {
		t.Errorf("the result must say which address it used (§6.2):\n%s", out.Text())
	}
}

func TestAnOriginalStartThatNamesNoOccurrenceSaysWhy(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", OriginalStart: "2026-06-02T14:00:00+02:00",
		Scope: "instance", Location: ptr("Room 3"),
	})
	if got := classOf(t, err); got != gapi.ClassNotFound {
		t.Fatalf("got [%s], want [not_found]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "SCHEDULED") {
		t.Errorf("the refusal must explain what original_start is: %v", err)
	}
}

// §2.8: two calls, the original truncated and a new series started — and
// the result says the exceptions after the target were reset, because
// Google does that and no caller expects it (spike E).
func TestThisAndFollowingIsTwoCallsAndSaysWhatItReset(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001", OriginalStart: "2026-03-31T14:00:00+02:00",
		Scope: "this_and_following", Title: ptr("Weekly sync"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 2 {
		t.Fatalf("got %d writes, want 2 (truncate then insert): %+v", len(w), w)
	}
	if w[0].Method != "patch" || w[0].EventID != "evseries001" {
		t.Errorf("the first call must truncate the original series, got %+v", w[0])
	}
	if w[1].Method != "insert" {
		t.Errorf("the second call must start the new series, got %+v", w[1])
	}
	text := out.Text()
	if !strings.Contains(text, "RESET") {
		t.Errorf("the result must warn that later exceptions were reset:\n%s", text)
	}
	if !strings.Contains(text, "COUNT=2") {
		t.Errorf("the truncated rule should keep the two occurrences before the target:\n%s", text)
	}
	if !strings.Contains(text, "COUNT=6") {
		t.Errorf("the new series should carry the remaining six:\n%s", text)
	}
	if out.Requests < 2 {
		t.Errorf("§4.7: a two-call write must report it, got %d", out.Requests)
	}
}

func TestUpdateWithNothingToChangeIsRefused(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
}

// A calendar this account can only read is refused before anything is
// attempted, with the role named.
func TestAWriteToAReadOnlyCalendarIsForbidden(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "Sample Readonly", Title: "Nope",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
	})
	if got := classOf(t, err); got != gapi.ClassForbidden {
		t.Fatalf("got [%s], want [forbidden]: %v", got, err)
	}
}

// ----------------------------------------------------------- canceling

// §7.4: two API shapes behind one verb, and the result names which.
func TestCancelingAWholeEventDeletesIt(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evsolo00001",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].Method != "delete" {
		t.Fatalf("got %+v, want one delete", w)
	}
	if !strings.Contains(out.Text(), "deletes the event") {
		t.Errorf("the result must say which of the two shapes happened:\n%s", out.Text())
	}
}

func TestCancelingOneOccurrenceIsAStatusPatch(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z", Scope: "instance",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].Method != "patch" {
		t.Fatalf("got %+v, want one status patch", w)
	}
	if out.After == nil || out.After.Status != gcal.StatusCanceled {
		t.Fatalf("the occurrence should come back canceled, got %+v", out.After)
	}
	if !strings.Contains(out.Text(), "rather than deleted") {
		t.Errorf("the result must explain why an occurrence is patched, not deleted:\n%s", out.Text())
	}
}

// §4.3.3: canceling with no notification removes it from the
// organizer's calendar and leaves it on the guests' (§18 row 43). The
// result says so, because that is the sentence a caller most needs.
func TestCancelingQuietlySaysTheGuestsStillHaveIt(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evguests001", Notify: "all",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "not something the API reports") {
		t.Errorf("a notified cancellation must not claim delivery:\n%s", out.Text())
	}

	svc2, _ := writeSeed(t)
	// Every guest inside the domain, so `none` is allowed and the
	// warning is the protection instead.
	quiet, err := svc2.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evinvite001", Notify: "none",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(quiet.Text(), "still") || !strings.Contains(quiet.Text(), "leaves it on theirs") {
		t.Errorf("a quiet cancellation must say the guests keep the meeting:\n%s", quiet.Text())
	}
}

func TestCancelingSomethingAlreadyCanceledIsAConflict(t *testing.T) {
	svc, fake := writeSeed(t)
	gone := caltest.Timed("evgone00001", "Already gone",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00", "Europe/Copenhagen")
	gone.Status = gcal.StatusCanceled
	fake.AddEvent("me@example.test", gone)

	_, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evgone00001",
	})
	if got := classOf(t, err); got != gapi.ClassConflict {
		t.Fatalf("got [%s], want [conflict]: %v", got, err)
	}
}

// Canceling "this and following" is ONE call: nothing has to start
// again, so the pattern collapses to truncating the original.
func TestCancelingThisAndFollowingIsOneCall(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evseries001_20260331T120000Z", Scope: "this_and_following",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].Method != "patch" || w[0].EventID != "evseries001" {
		t.Fatalf("got %+v, want one patch on the series", w)
	}
	if !strings.Contains(out.Text(), "COUNT=2") {
		t.Errorf("the series should now end before the target:\n%s", out.Text())
	}
	if !strings.Contains(out.Text(), "one call") {
		t.Errorf("the result should say why this is not two calls:\n%s", out.Text())
	}
}

// -------------------------------------------------------------- moving

func TestMoveChangesTheCalendarAndSaysWhatThatMeans(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
	})
	if err != nil {
		t.Fatalf("MoveEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].Method != "move" {
		t.Fatalf("got %+v, want one move", w)
	}
	if out.After == nil || out.After.CalendarID != "team@group.calendar.example.test" {
		t.Fatalf("the result must report the event on its new calendar, got %+v", out.After)
	}
	if !strings.Contains(out.Text(), "organizer") {
		t.Errorf("a move changes the organizer, and the result should say so:\n%s", out.Text())
	}
}

func TestMoveToTheSameCalendarIsRefused(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "primary",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
}

// §2.8: there is no "this and following" to build here, so the operation
// does not exist rather than being half-done.
func TestMoveRefusesThisAndFollowingAsUnsupported(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z",
		ToCalendar: "Sample Team", Scope: "this_and_following",
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported {
		t.Fatalf("got [%s], want [unsupported]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("nothing may be written for an operation that does not exist")
	}
}

// ----------------------------------------------------------- responding

// §7.4: RSVPing is not editing. Only this account's row changes, and
// everybody else's answer and comment survive exactly.
func TestRespondChangesOnlyThisAccountsAnswer(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
		Calendar: "primary", EventID: "evinvite001", Response: "accepted",
		Comment: "will be there", Notify: "all",
	})
	if err != nil {
		t.Fatalf("RespondToEvent: %v", err)
	}
	if len(fake.Wrote()) != 1 {
		t.Fatalf("got %+v, want one patch", fake.Wrote())
	}
	after := fake.Events["me@example.test"]["evinvite001"]
	var mine, theirs gcal.EventAttendee
	for _, a := range after.Attendees {
		switch a.Email {
		case "me@example.test":
			mine = a
		case "other@example.test":
			theirs = a
		}
	}
	if mine.ResponseStatus != gcal.ResponseAccepted || mine.Comment != "will be there" {
		t.Fatalf("this account's answer is wrong: %+v", mine)
	}
	if theirs.ResponseStatus != gcal.ResponseDeclined || theirs.Comment != "clash" {
		t.Fatalf("somebody else's answer was overwritten: %+v", theirs)
	}
	if len(out.Changes) != 1 || out.Changes[0].To != gcal.ResponseAccepted {
		t.Fatalf("got changes %+v, want one response change", out.Changes)
	}
}

func TestRespondRefusesWhenThisAccountIsNotAGuest(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
		Calendar: "primary", EventID: "evsolo00001", Response: "accepted",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "update_event") {
		t.Errorf("the refusal should point at the tool that does apply: %v", err)
	}
}

func TestRespondRefusesAnAnswerThatIsNotOne(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
		Calendar: "primary", EventID: "evinvite001", Response: "needsAction",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
}

func TestRespondRefusesThisAndFollowingAsUnsupported(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z",
		Response: "declined", Scope: "this_and_following",
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported {
		t.Fatalf("got [%s], want [unsupported]: %v", got, err)
	}
}

// ptr is v by pointer, for an input whose absence means "leave it".
func ptr[T any](v T) *T { return &v }

// §4.7: one tool call is one API request, and where it cannot be, the
// exceptions are named. This is the guard that stops the count creeping
// back — phase 1 found `check_availability` spending 168 requests to set
// up a query its own result called 2, and the direction that hides in is
// the one nothing prints.
//
// The two reads every write shares — the calendar list and the account
// settings — are cached for the process, so they are paid once and never
// again. What is asserted here is the TOTAL served, not the reported
// count, because the reported count is the operation's own work and the
// total is what Google's quota sees.
func TestAWriteSpendsTheRequestsItShould(t *testing.T) {
	for _, c := range []struct {
		name  string
		want  int
		run   func(*service.Service) error
		after string
	}{
		{"create", 3, func(s *service.Service) error {
			_, err := s.CreateEvent(context.Background(), service.CreateOptions{
				Calendar: "primary", Title: "x",
				Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			})
			return err
		}, "list, settings, insert"},
		{"update", 4, func(s *service.Service) error {
			_, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 2"),
			})
			return err
		}, "list, settings, read, patch"},
		{"cancel", 4, func(s *service.Service) error {
			_, err := s.CancelEvent(accepted(), service.CancelOptions{
				Calendar: "primary", EventID: "evsolo00001",
			})
			return err
		}, "list, settings, read, delete"},
		// Five, not four: the event is read back from the destination,
		// because a successful move answers with status:cancelled and
		// the result would otherwise report a moved meeting as a
		// canceled one (§18 row 48).
		{"move", 5, func(s *service.Service) error {
			_, err := s.MoveEvent(context.Background(), service.MoveOptions{
				Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
			})
			return err
		}, "list, settings, read, move, read back"},
		{"respond", 4, func(s *service.Service) error {
			_, err := s.RespondToEvent(context.Background(), service.RespondOptions{
				Calendar: "primary", EventID: "evinvite001", Response: "accepted", Notify: "all",
			})
			return err
		}, "list, settings, read, patch"},
		// The one write that is genuinely two calls, plus the parent read
		// the split needs. §4.7 says the result has to say so, and
		// TestThisAndFollowingIsTwoCallsAndSaysWhatItReset holds that.
		{"this_and_following", 6, func(s *service.Service) error {
			_, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evseries001",
				OriginalStart: "2026-03-31T14:00:00+02:00",
				Scope:         "this_and_following", Title: ptr("Renamed"),
			})
			return err
		}, "list, settings, read occurrence, read parent, truncate, insert"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			if err := c.run(svc); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			got := fake.Served()
			if len(got) != c.want {
				t.Fatalf("%s spent %d requests, want %d (%s):\n  %s",
					c.name, len(got), c.want, c.after, strings.Join(got, "\n  "))
			}
		})
	}
}

// And the shared reads are shared: a second write on the same process
// pays for neither the calendar list nor the settings again.
func TestASecondWriteReusesWhatTheFirstRead(t *testing.T) {
	svc, fake := writeSeed(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := svc.CreateEvent(ctx, service.CreateOptions{
			Calendar: "primary", Title: "x",
			Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		}); err != nil {
			t.Fatalf("CreateEvent %d: %v", i, err)
		}
	}
	got := fake.Served()
	if len(got) != 4 {
		t.Fatalf("two creates spent %d requests, want 4 — list and settings once, then an insert each:\n  %s",
			len(got), strings.Join(got, "\n  "))
	}
}

// §4.2's second half, and the reason it has one owner: choosing a scope
// and applying it are two decisions, and only the first had one. These
// four cases are the reproductions that found it — move_event had no
// targeting at all, and respond_to_event was missing the refusal.
func TestAScopeReachesTheEventItNames(t *testing.T) {
	t.Run("move series from an occurrence writes the parent", func(t *testing.T) {
		svc, fake := writeSeed(t)
		if _, err := svc.MoveEvent(context.Background(), service.MoveOptions{
			Calendar: "primary", EventID: "evseries001_20260324T130000Z",
			ToCalendar: "Sample Team", Scope: "series",
		}); err != nil {
			t.Fatalf("MoveEvent: %v", err)
		}
		w := fake.Wrote()
		if len(w) != 1 || w[0].EventID != "evseries001" {
			t.Fatalf("scope:series moved %+v; it must move the series, not one occurrence", w)
		}
	})

	t.Run("move instance against the series id is refused", func(t *testing.T) {
		svc, fake := writeSeed(t)
		_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
			Calendar: "primary", EventID: "evseries001", ToCalendar: "Sample Team", Scope: "instance",
		})
		if got := classOf(t, err); got != gapi.ClassInvalid {
			t.Fatalf("got [%s], want [invalid]: %v", got, err)
		}
		if len(fake.Wrote()) != 0 {
			t.Fatal("the whole series was moved by a call that named one occurrence")
		}
	})

	t.Run("RSVP instance against the series id is refused", func(t *testing.T) {
		svc, fake := writeSeed(t)
		// The series this account is actually a guest on, so the refusal
		// is the thing under test rather than "you are not invited".
		series := caltest.Recurring("evrsvpser01", "Weekly with guests",
			"2026-03-17T16:00:00+01:00", "2026-03-17T17:00:00+01:00", "Europe/Copenhagen",
			"RRULE:FREQ=WEEKLY;COUNT=4")
		series.Attendees = []gcal.EventAttendee{
			{Email: "host@example.test", Organizer: true},
			{Email: "me@example.test", Self: true, ResponseStatus: gcal.ResponseNeedsAction},
		}
		fake.AddEvent("me@example.test", series)

		_, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
			Calendar: "primary", EventID: "evrsvpser01", Response: "declined", Scope: "instance",
		})
		if got := classOf(t, err); got != gapi.ClassInvalid {
			t.Fatalf("got [%s], want [invalid]: %v", got, err)
		}
		if len(fake.Wrote()) != 0 {
			t.Fatal("an RSVP naming one occurrence patched the whole series' attendee array")
		}
	})

	t.Run("RSVP series from an occurrence writes the parent", func(t *testing.T) {
		svc, fake := writeSeed(t)
		series := caltest.Recurring("evrsvpser02", "Weekly with guests",
			"2026-03-17T16:00:00+01:00", "2026-03-17T17:00:00+01:00", "Europe/Copenhagen",
			"RRULE:FREQ=WEEKLY;COUNT=4")
		series.Attendees = []gcal.EventAttendee{
			{Email: "host@example.test", Organizer: true},
			{Email: "me@example.test", Self: true, ResponseStatus: gcal.ResponseNeedsAction},
		}
		fake.AddEvent("me@example.test", series)
		occ := caltest.Instance("evrsvpser02_20260324T150000Z", "evrsvpser02", "Weekly with guests",
			"2026-03-24T16:00:00+01:00", "2026-03-24T17:00:00+01:00", "Europe/Copenhagen",
			"2026-03-24T16:00:00+01:00")
		occ.Attendees = series.Attendees
		fake.AddEvent("me@example.test", occ)

		if _, err := svc.RespondToEvent(context.Background(), service.RespondOptions{
			Calendar: "primary", EventID: "evrsvpser02_20260324T150000Z",
			Response: "declined", Scope: "series", Notify: "all",
		}); err != nil {
			t.Fatalf("RespondToEvent: %v", err)
		}
		w := fake.Wrote()
		if len(w) != 1 || w[0].EventID != "evrsvpser02" {
			t.Fatalf("answered %+v; scope:series must answer for the whole series", w)
		}
	})
}

// The new series of a this_and_following write is the parent copied, so
// it keeps its attachments and the fields this server has no struct
// field for. It used to be built from the decoded fields alone:
// attachments and another application's properties were lost, and a
// working-location series went to Google without the details a create
// needs (§18 row 88).
func TestASplitCarriesWhatThisServerDoesNotModel(t *testing.T) {
	svc, fake := writeSeed(t)
	var series gcal.Event
	if err := json.Unmarshal([]byte(`{
		"id": "evoffice001", "summary": "Office days", "status": "confirmed",
		"kind": "calendar#event", "eventType": "workingLocation",
		"visibility": "public", "transparency": "transparent",
		"start": {"dateTime": "2026-03-17T09:00:00+01:00", "timeZone": "Europe/Copenhagen"},
		"end": {"dateTime": "2026-03-17T17:00:00+01:00", "timeZone": "Europe/Copenhagen"},
		"recurrence": ["RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"],
		"hangoutLink": "https://meet.example.test/aaaa-bbbb-ccc",
		"workingLocationProperties": {"type": "officeLocation", "officeLocation": {"label": "Sample building"}},
		"extendedProperties": {"private": {"sampleKey": "sampleValue"}},
		"attachments": [{"fileUrl": "https://drive.example.test/AAAAfile1", "title": "Sample agenda"}],
		"eventLabelId": "AAAAlabel1"
	}`), &series); err != nil {
		t.Fatal(err)
	}
	fake.AddEvent("me@example.test", &series)
	occ := caltest.Instance("evoffice001_20260331T070000Z", "evoffice001", "Office days",
		"2026-03-31T09:00:00+02:00", "2026-03-31T17:00:00+02:00", "Europe/Copenhagen",
		"2026-03-31T09:00:00+02:00")
	occ.EventType = gcal.EventTypeWorkingLocation
	fake.AddEvent("me@example.test", occ)

	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evoffice001_20260331T070000Z",
		Scope: "this_and_following", Title: ptr("Office days, later"),
	}); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 2 || w[1].Method != "insert" {
		t.Fatalf("want a truncate and an insert, got %+v", w)
	}
	data, err := json.Marshal(fake.Events["me@example.test"][w[1].EventID])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"workingLocationProperties": `{"type":"officeLocation","officeLocation":{"label":"Sample building"}}`,
		"extendedProperties":        `{"private":{"sampleKey":"sampleValue"}}`,
		"attachments":               `[{"fileUrl":"https://drive.example.test/AAAAfile1","title":"Sample agenda"}]`,
		"eventLabelId":              `"AAAAlabel1"`,
	} {
		if got := string(fields[name]); got != want {
			t.Errorf("the new series has %s %s, want the parent's %s", name, got, want)
		}
	}
	for _, name := range []string{"kind", "hangoutLink"} {
		if got, ok := fields[name]; ok {
			t.Errorf("the new series carries the old event's %s %s", name, got)
		}
	}
}

// §4.3.3: a two-call write asks for the same notification on both calls.
// The truncate is the one that removes the later occurrences from
// everybody's calendar, so a result saying "asked Google to notify all
// 2 guests" while the truncate asked for nothing described a decision
// applied to half the operation.
func TestBothHalvesOfASplitCarryTheNotifyChoice(t *testing.T) {
	svc, fake := writeSeed(t)
	series := caltest.Recurring("evsplitgs01", "Weekly with guests",
		"2026-03-17T16:00:00+01:00", "2026-03-17T17:00:00+01:00", "Europe/Copenhagen",
		"RRULE:FREQ=WEEKLY;COUNT=4")
	series.Organizer = &gcal.EventPerson{Email: "me@example.test", Self: true}
	series.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true},
		{Email: "colleague@example.test"},
	}
	fake.AddEvent("me@example.test", series)
	occ := caltest.Instance("evsplitgs01_20260331T140000Z", "evsplitgs01", "Weekly with guests",
		"2026-03-31T16:00:00+02:00", "2026-03-31T17:00:00+02:00", "Europe/Copenhagen",
		"2026-03-31T16:00:00+02:00")
	occ.Attendees = series.Attendees
	fake.AddEvent("me@example.test", occ)

	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsplitgs01_20260331T140000Z",
		Scope: "this_and_following", Title: ptr("Renamed"), Notify: "all",
	}); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 2 {
		t.Fatalf("got %d writes, want 2: %+v", len(w), w)
	}
	for _, got := range w {
		if got.SendUpdates != gcal.SendUpdatesAll {
			t.Errorf("%s asked for sendUpdates=%q; both calls carry the caller's choice",
				got.Method, got.SendUpdates)
		}
	}
}

// §4.7: api_requests is read off a counter the client increments, so it
// counts every request the call made — the shared setup reads included.
// It used to be a field incremented by hand, which missed all of them: a
// create reported 1 and made 4, and a dry run reported 0 while spending
// three.
func TestTheReportedRequestCountIsTheTruth(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(*service.Service) (int, error)
	}{
		{"create", func(s *service.Service) (int, error) {
			o, err := s.CreateEvent(context.Background(), service.CreateOptions{
				Calendar: "primary", Title: "x",
				Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			})
			return o.Requests, err
		}},
		{"create dry run", func(s *service.Service) (int, error) {
			o, err := s.CreateEvent(context.Background(), service.CreateOptions{
				Calendar: "primary", Title: "x", DryRun: true,
				Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
			})
			return o.Requests, err
		}},
		{"update", func(s *service.Service) (int, error) {
			o, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 2"),
			})
			return o.Requests, err
		}},
		{"this_and_following", func(s *service.Service) (int, error) {
			o, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evseries001",
				OriginalStart: "2026-03-31T14:00:00+02:00",
				Scope:         "this_and_following", Title: ptr("Renamed"),
			})
			return o.Requests, err
		}},
		{"cancel", func(s *service.Service) (int, error) {
			o, err := s.CancelEvent(accepted(), service.CancelOptions{
				Calendar: "primary", EventID: "evsolo00001",
			})
			return o.Requests, err
		}},
		{"move", func(s *service.Service) (int, error) {
			o, err := s.MoveEvent(context.Background(), service.MoveOptions{
				Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
			})
			return o.Requests, err
		}},
		{"respond", func(s *service.Service) (int, error) {
			o, err := s.RespondToEvent(context.Background(), service.RespondOptions{
				Calendar: "primary", EventID: "evinvite001", Response: "accepted", Notify: "all",
			})
			return o.Requests, err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			reported, err := c.run(svc)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if served := len(fake.Served()); reported != served {
				t.Fatalf("%s reported %d requests and made %d:\n  %s",
					c.name, reported, served, strings.Join(fake.Served(), "\n  "))
			}
		})
	}
}

// And a call that can never succeed does not spend somebody's quota
// finding that out.
func TestArgumentsThatCannotWorkAreRefusedBeforeAnyRequest(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(*service.Service) error
	}{
		{"update with nothing to change", func(s *service.Service) error {
			_, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evsolo00001",
			})
			return err
		}},
		{"an answer that is not one", func(s *service.Service) error {
			_, err := s.RespondToEvent(context.Background(), service.RespondOptions{
				Calendar: "primary", EventID: "evinvite001", Response: "nonsense",
			})
			return err
		}},
		{"this_and_following on an RSVP", func(s *service.Service) error {
			_, err := s.RespondToEvent(context.Background(), service.RespondOptions{
				Calendar: "primary", EventID: "evinvite001",
				Response: "declined", Scope: "this_and_following",
			})
			return err
		}},
		{"this_and_following on a move", func(s *service.Service) error {
			_, err := s.MoveEvent(context.Background(), service.MoveOptions{
				Calendar: "primary", EventID: "evsolo00001",
				ToCalendar: "Sample Team", Scope: "this_and_following",
			})
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			if err := c.run(svc); err == nil {
				t.Fatal("expected a refusal")
			}
			if n := len(fake.Served()); n != 0 {
				t.Fatalf("spent %d requests to refuse an argument that reads wrong on its own:\n  %s",
					n, strings.Join(fake.Served(), "\n  "))
			}
		})
	}
}

// The all-day half of "this and following", which is the caller §16's
// phase-1 note said would arrive in phase 2 and decide whether the split
// arithmetic could be shared. A date has no instant, so it cannot go
// through the timed path at all (§4.1).
func TestThisAndFollowingOnAnAllDaySeries(t *testing.T) {
	svc, fake := writeSeed(t)
	// A weekly all-day series: four Tuesdays from 17 March.
	series := caltest.AllDay("evallday001", "Weekly all-day", "2026-03-17", "2026-03-18")
	series.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"}
	fake.AddEvent("me@example.test", series)
	occ := caltest.AllDay("evallday001_20260331", "Weekly all-day", "2026-03-31", "2026-04-01")
	occ.RecurringEventID = "evallday001"
	occ.OriginalStartTime = &gcal.EventDateTime{Date: "2026-03-31"}
	fake.AddEvent("me@example.test", occ)

	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evallday001", OriginalStart: "2026-03-31",
		Scope: "this_and_following", Title: ptr("Renamed all-day"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 2 {
		t.Fatalf("got %d writes, want 2: %+v", len(w), w)
	}
	text := out.Text()
	// 17 and 24 March stay behind; 31 March and 7 April start again.
	if !strings.Contains(text, "COUNT=2") {
		t.Errorf("the split did not land on two and two:\n%s", text)
	}
	// And the new series is still all-day: a date must never have become
	// an instant on the way through (§4.1).
	after := out.After
	if after == nil || !after.Start.AllDay {
		t.Fatalf("the new series is not all-day: %+v", after)
	}
	if !after.Start.At.IsZero() || !after.End.At.IsZero() {
		t.Fatalf("an all-day series came back carrying an instant: %+v", after)
	}
	if got := after.Start.Date.String(); got != "2026-03-31" {
		t.Fatalf("the new series starts on %q, want 2026-03-31", got)
	}
	// The duration carries over: one day in, one day out, and Google's
	// end is the day after.
	if got := after.End.Date.String(); got != "2026-04-01" {
		t.Fatalf("the new series ends on %q, want the exclusive 2026-04-01", got)
	}
}

// And canceling "this and following" on an all-day series, which is the
// one-call version of the same arithmetic.
func TestCancelThisAndFollowingOnAnAllDaySeries(t *testing.T) {
	svc, fake := writeSeed(t)
	series := caltest.AllDay("evallday002", "Weekly all-day", "2026-03-17", "2026-03-18")
	series.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"}
	fake.AddEvent("me@example.test", series)
	occ := caltest.AllDay("evallday002_20260324", "Weekly all-day", "2026-03-24", "2026-03-25")
	occ.RecurringEventID = "evallday002"
	occ.OriginalStartTime = &gcal.EventDateTime{Date: "2026-03-24"}
	fake.AddEvent("me@example.test", occ)

	out, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evallday002", OriginalStart: "2026-03-24",
		Scope: "this_and_following",
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) != 1 || w[0].EventID != "evallday002" {
		t.Fatalf("got %+v, want one patch truncating the series", w)
	}
	if !strings.Contains(out.Text(), "COUNT=1") {
		t.Errorf("only the 17 March occurrence should survive:\n%s", out.Text())
	}
}

// §4.4: the etag a caller passes is a statement about the event THEY
// read. A scope that redirects the write to the series parent must not
// turn that into a refusal — it used to, every time, and re-reading the
// occurrence returned the same etag it had just rejected.
func TestAnEtagFromTheOccurrenceWorksWithScopeSeries(t *testing.T) {
	svc, fake := writeSeed(t)
	occ, _, err := svc.GetEvent(context.Background(), "primary", "evseries001_20260324T130000Z", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z",
		Scope: "series", Title: ptr("Renamed"), ETag: occ.ETag,
	}); err != nil {
		t.Fatalf("an etag read from the occurrence was refused under scope:series: %v", err)
	}
	// And If-Match still protects the event actually written.
	w := fake.Wrote()
	if len(w) != 1 || w[0].EventID != "evseries001" || w[0].IfMatch == "" {
		t.Fatalf("got %+v; the parent must be written under its own If-Match", w)
	}
	if w[0].IfMatch == occ.ETag {
		t.Fatalf("If-Match carried the occurrence's etag %q, not the parent's", w[0].IfMatch)
	}
}

// §4.3.4's refusal must not fire on a colleague. An event on a secondary
// calendar is organized by the CALENDAR, whose id has a domain of its
// own, and splitting the guest count on that made everybody external.
func TestAnEventOrganizedByACalendarStillKnowsWhoIsInside(t *testing.T) {
	svc, fake := writeSeed(t)
	ev := caltest.Timed("evteamev001", "Team thing",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00", "Europe/Copenhagen")
	ev.Organizer = &gcal.EventPerson{Email: "team@group.calendar.example.test"}
	ev.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true},
		{Email: "colleague@example.test"},
	}
	fake.AddEvent("team@group.calendar.example.test", ev)

	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "Sample Team", EventID: "evteamev001", Location: ptr("Room 9"), Notify: "none",
	}); err != nil {
		t.Fatalf("notify:none was refused for a same-domain colleague: %v", err)
	}
	// And a genuinely outside guest is still caught.
	ev2 := caltest.Timed("evteamev002", "Team thing",
		"2026-03-18T12:00:00+01:00", "2026-03-18T13:00:00+01:00", "Europe/Copenhagen")
	ev2.Organizer = &gcal.EventPerson{Email: "team@group.calendar.example.test"}
	ev2.Attendees = []gcal.EventAttendee{{Email: "partner@elsewhere.test"}}
	fake.AddEvent("team@group.calendar.example.test", ev2)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "Sample Team", EventID: "evteamev002", Location: ptr("Room 9"), Notify: "none",
	})
	if got := classOf(t, err); got != gapi.ClassBlocked {
		t.Fatalf("got [%s], want [blocked] for a guest genuinely outside the domain", got)
	}
}

// §4.3.5: a dry run reports what WOULD change, so it has to project the
// change rather than print the event twice. create's always did; update
// and cancel printed the unchanged event, and cancel printed it alive
// under the words "Deleted the event".
func TestADryRunProjectsTheChange(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", Location: ptr("Room 9"), DryRun: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if out.After == nil || out.After.Location != "Room 9" {
		t.Fatalf("a dry run showed the event unchanged: %+v", out.After)
	}
	if out.Before == nil || out.Before.Location == "Room 9" {
		t.Fatalf("the before side was projected too: %+v", out.Before)
	}

	c, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evsolo00001", DryRun: true,
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(c.Text(), "after:  (canceled)") {
		t.Fatalf("a cancellation dry run showed the event alive:\n%s", c.Text())
	}
}

// A 412 is the opposite of "already gone": the event is there and
// somebody edited it. Reporting it as gone tells a caller their meeting
// was canceled when it is still live.
func TestAConcurrentEditOnADeleteIsStaleNotGone(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Fail["DELETE /calendars/me@example.test/events/evsolo00001"] = 412
	_, err := svc.CancelEvent(accepted(), service.CancelOptions{
		Calendar: "primary", EventID: "evsolo00001",
	})
	if got := classOf(t, err); got != gapi.ClassStale {
		t.Fatalf("got [%s], want [stale]: %v", got, err)
	}
	if strings.Contains(err.Error(), "already gone") {
		t.Fatalf("a concurrent edit was reported as the event being gone: %v", err)
	}
	if !strings.Contains(err.Error(), "again") {
		t.Fatalf("the refusal does not tell the caller to re-read: %v", err)
	}
}

// §18 row 48, offline: a successful move answers with status:cancelled,
// so the result must come from reading the destination rather than from
// the move's own response. It reported a meeting that had just been
// moved as one that had been called off — green in every gate, and
// visible only in the live transcript.
func TestAMovedEventIsNotReportedAsCanceled(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
	})
	if err != nil {
		t.Fatalf("MoveEvent: %v", err)
	}
	if out.After == nil {
		t.Fatal("a move must report where the event landed")
	}
	if out.After.Canceled() {
		t.Fatalf("a moved event was reported canceled: %+v", out.After)
	}
	// Both spellings: the "canceled" tag and Google's own "cancelled"
	// status would each report the move wrong.
	if text := out.Text(); strings.Contains(text, "canceled") || strings.Contains(text, "cancelled") {
		t.Fatalf("the move result says canceled:\n%s", out.Text())
	}
	if out.After.CalendarID != "team@group.calendar.example.test" {
		t.Fatalf("reported on %q, want the destination", out.After.CalendarID)
	}
}

// §18 row 49: events.move honors If-Match, which nothing Google
// publishes says. The server sent none for a while and told callers the
// protection was absent; spike J asked the API instead of the
// documentation, and §4.4 turned out to have no exception.
func TestMoveIsMadeUnderIfMatch(t *testing.T) {
	svc, fake := writeSeed(t)
	before, _, err := svc.GetEvent(context.Background(), "primary", "evsolo00001", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
	}); err != nil {
		t.Fatalf("MoveEvent: %v", err)
	}
	w := fake.Wrote()
	if len(w) == 0 || w[0].Method != "move" {
		t.Fatalf("got %+v, want a move", w)
	}
	if w[0].IfMatch != before.ETag {
		t.Fatalf("the move carried If-Match %q, want the etag the read produced (%q)",
			w[0].IfMatch, before.ETag)
	}
}

func TestMoveWithAnEtagThatMovedIsStale(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
		ETag: `"something-older"`,
	})
	if got := classOf(t, err); got != gapi.ClassStale {
		t.Fatalf("got [%s], want [stale]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("a stale move must be refused before it is sent")
	}
}

// TestCreateEventAsksForAMeetLinkAndSaysItIsNotThereYet is §17.3's
// whole point: Google makes the conference asynchronously, so a result
// that announced a link on the strength of having asked for one would
// be wrong about the only thing the caller wanted.
func TestCreateEventAsksForAMeetLinkAndSaysItIsNotThereYet(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "With a link",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Conference: true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	// The conference survived the insert at all only because the client
	// sent conferenceDataVersion=1; the fake drops it otherwise, the way
	// Google does.
	if !out.After.Conference.Pending() {
		t.Fatalf("the created event's conference is %+v, want pending", out.After.Conference)
	}
	text := out.Text()
	if !strings.Contains(text, "still making it") {
		t.Fatalf("the result does not say the link is not ready:\n%s", text)
	}
	if strings.Contains(text, "meet.google.com") {
		t.Fatalf("the result offered a link Google has not made:\n%s", text)
	}

	// And the link is there on the read that follows, which is what the
	// result told the caller to do.
	got, zone, err := svc.GetEvent(context.Background(), "primary", out.After.ID, "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if text := service.NewEventResult(got, zone).Render(); !strings.Contains(text, "join: https://meet.google.com/") {
		t.Fatalf("the event does not show the link Google finished making:\n%s", text)
	}
}

// TestCreateEventReportsAConferenceGoogleRefused: the event exists and
// has no link, and a caller who is told nothing will promise one.
func TestCreateEventReportsAConferenceGoogleRefused(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.ConferenceFails = true

	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "No link for you",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Conference: true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "could NOT be created") {
		t.Fatalf("a failed conference was not reported:\n%s", out.Text())
	}
}

// TestCreateEventRefusesAConferenceTheCalendarForbids: refused before
// the write, because Google's own answer is a 200 with a failed request
// and an event that exists.
func TestCreateEventRefusesAConferenceTheCalendarForbids(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Entries["team@group.calendar.example.test"].ConferenceProperties =
		&gcal.ConferenceProperties{AllowedConferenceSolutionTypes: []string{"eventHangout"}}

	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "team@group.calendar.example.test", Title: "Nope",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Conference: true,
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported {
		t.Fatalf("got [%s], want [unsupported]: %v", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must happen before anything is written")
	}
}

// TestCreateEventDryRunDoesNotPromiseALink: a dry run cannot know what
// Google will do, and says so rather than implying a link.
func TestCreateEventDryRunDoesNotPromiseALink(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Maybe",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Conference: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "would be requested") {
		t.Fatalf("the dry run does not mention the conference:\n%s", out.Text())
	}
	// The structured half read the request back as a conference with no
	// video link, a state Google never answered with.
	if got := service.NewWriteResult(out).Event.ConferenceStatus; got != "" {
		t.Fatalf("the dry run reports conference_status %q", got)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("a dry run wrote something")
	}
}

// TestACrowdedEventSaysItsRSVPsAreIncomplete is §17.5: above 200 guests
// Google stops propagating response status, so the RSVPs this server
// reports are not the RSVPs on the event.
func TestACrowdedEventSaysItsRSVPsAreIncomplete(t *testing.T) {
	svc, _ := writeSeed(t)
	guests := make([]string, 0, plan.AttendeeLimit+1)
	for i := range plan.AttendeeLimit + 1 {
		guests = append(guests, fmt.Sprintf("guest%03d@example.test", i))
	}

	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "All hands",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: guests, Notify: "all",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "do not count acceptances off them") {
		t.Fatalf("a crowded event did not warn about its RSVPs:\n%s", out.Text())
	}
	// Both halves carry it: a client showing only the structured block
	// would otherwise drop the warning entirely.
	res := service.NewWriteResult(out)
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "above Google's limit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the structured result does not carry the warning: %+v", res.Notes)
	}
	// And the addresses are never in it (§9).
	for _, n := range res.Notes {
		if strings.Contains(n, "@example.test") {
			t.Fatalf("a note names a guest: %q", n)
		}
	}
}

// TestAnOrdinaryEventDoesNotWarnAboutItsSize: the warning is for the
// event Google cannot track, not for every meeting with guests.
func TestAnOrdinaryEventDoesNotWarnAboutItsSize(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Three of us",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Guests: []string{"colleague@example.test", "other@example.test"}, Notify: "all",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if strings.Contains(out.Text(), "above Google's limit") {
		t.Fatalf("an ordinary event was warned about:\n%s", out.Text())
	}
}

// TestAConferenceIsRefusedOnACalendarReachedByID: the guard reads the
// calendar's published conference types, and a calendar the account is
// not subscribed to is resolved through a different path — one that
// used to build the model by hand and drop the field, so the refusal
// silently did not fire for exactly the calendars it exists for.
func TestAConferenceIsRefusedOnACalendarReachedByID(t *testing.T) {
	fake := caltest.New()
	fake.AddCalendar("me@example.test", "Mine", "Europe/Copenhagen", gcal.RoleOwner, true)
	// A calendar that exists as a resource but is not in the list: the
	// entry read 404s and the resource read answers.
	fake.Calendars["nolist@group.calendar.example.test"] = &gcal.Calendar{
		ID: "nolist@group.calendar.example.test", Summary: "Not subscribed",
		TimeZone:             "Europe/Copenhagen",
		ConferenceProperties: &gcal.ConferenceProperties{AllowedConferenceSolutionTypes: []string{"eventHangout"}},
	}
	svc := newService(t, fake)

	_, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "nolist@group.calendar.example.test", Title: "No link here",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		Conference: true,
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported {
		t.Fatalf("got [%s], want [unsupported]: %v", got, err)
	}
}

// TestACrowdedEventWarnsOnTheReadPathToo: get_event prints each guest's
// response, and above the limit those responses are not the ones on the
// event. §17.5 says any result describing such an event says so, not
// only the write that made it.
func TestACrowdedEventWarnsOnTheReadPathToo(t *testing.T) {
	fake := caltest.Seed()
	crowd := caltest.Timed("evcrowded01", "All hands",
		"2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")
	for i := range plan.AttendeeLimit + 1 {
		crowd.Attendees = append(crowd.Attendees,
			gcal.EventAttendee{Email: fmt.Sprintf("guest%03d@example.test", i)})
	}
	fake.AddEvent("primary", crowd)
	svc := newService(t, fake)

	e, zone, err := svc.GetEvent(context.Background(), "primary", "evcrowded01", "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	text := service.NewEventResult(e, zone).Render()
	if !strings.Contains(text, "above Google's limit") {
		t.Fatalf("a crowded event read back without the warning:\n%s", text)
	}
}

// TestTheCrowdWarningCountsWhatGoogleCounts: the threshold is Google's,
// on Google's own attendees field, so the account's own row and the
// rooms are on it. Counting only guests left an event at exactly the
// limit plus two unqualified while printing "Guests (202)".
func TestTheCrowdWarningCountsWhatGoogleCounts(t *testing.T) {
	fake := caltest.Seed()
	crowd := caltest.Timed("evcrowded02", "Company meeting",
		"2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")
	crowd.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true},
		{Email: "room@resource.example.test", Resource: true},
	}
	for i := range plan.AttendeeLimit {
		crowd.Attendees = append(crowd.Attendees,
			gcal.EventAttendee{Email: fmt.Sprintf("guest%03d@example.test", i)})
	}
	fake.AddEvent("primary", crowd)
	svc := newService(t, fake)

	e, zone, err := svc.GetEvent(context.Background(), "primary", "evcrowded02", "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	text := service.NewEventResult(e, zone).Render()
	if !strings.Contains(text, "above Google's limit") {
		t.Fatalf("an event of %d attendees was not warned about:\n%s", len(crowd.Attendees), text)
	}
	// And the two numbers in one result agree.
	if !strings.Contains(text, fmt.Sprintf("This event has %d attendees", len(crowd.Attendees))) {
		t.Fatalf("the warning quotes a different number from the guest list:\n%s", text)
	}
	if !strings.Contains(text, fmt.Sprintf("Guests (%d)", len(crowd.Attendees))) {
		t.Fatalf("the guest list count is not the one warned about:\n%s", text)
	}
}

// TestEveryForcedWriteSaysSo is §4.4's other half: a write made with
// If-Match: * overwrote somebody's change without seeing it, and the
// result has to say which. Two of the four writes that take `force`
// were silent about it, cancel_event among them — the one where what
// is overwritten is gone.
func TestEveryForcedWriteSaysSo(t *testing.T) {
	svc, _ := writeSeed(t)
	ctx := accepted()

	canceled, err := svc.CancelEvent(ctx, service.CancelOptions{
		Calendar: "primary", EventID: "evsolo00001", Force: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(canceled.Text(), "If-Match: *") {
		t.Fatalf("a forced cancel does not say it was forced:\n%s", canceled.Text())
	}

	answered, err := svc.RespondToEvent(ctx, service.RespondOptions{
		Calendar: "primary", EventID: "evguests001", Response: "accepted",
		Notify: "all", Force: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("RespondToEvent: %v", err)
	}
	if !strings.Contains(answered.Text(), "If-Match: *") {
		t.Fatalf("a forced RSVP does not say it was forced:\n%s", answered.Text())
	}
}

// §11: events.move is a POST that is not retried, so a move Google
// answered with a 5xx may have landed. Plain [unavailable] tells a model
// to retry; the class has to send it to look on the destination first.
func TestAMoveThatMayHaveLandedIsAmbiguous(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Fail["POST /calendars/me@example.test/events/evsolo00001/move"] = 503
	_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
	})
	if got := classOf(t, err); got != gapi.ClassAmbiguousOutcome {
		t.Fatalf("got [%s], want [ambiguous_outcome]: %v", got, err)
	}
	if !strings.Contains(err.Error(), "get_event") {
		t.Errorf("the message must say how to look: %v", err)
	}
}

// A 4xx definitely did not land, so a refused move keeps its own class.
func TestAMoveGoogleRefusedIsNotAmbiguous(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Fail["POST /calendars/me@example.test/events/evsolo00001/move"] = 400
	_, err := svc.MoveEvent(context.Background(), service.MoveOptions{
		Calendar: "primary", EventID: "evsolo00001", ToCalendar: "Sample Team",
	})
	if got := classOf(t, err); got != gapi.ClassInvalid {
		t.Fatalf("got [%s], want [invalid]: %v", got, err)
	}
}

// Reminders are kept per person, so a write that changes only them
// reaches nobody: on an event with a guest outside the organization it
// needs no notify, and sends no sendUpdates.
func TestARemindersOnlyUpdateReachesNobody(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evguests001", PopupReminders: ptr([]int{10}), EmailReminders: ptr([]int{1440}),
	})
	if err != nil {
		t.Fatalf("a reminders-only update asked for notify: %v", err)
	}
	writes := fake.Wrote()
	if len(writes) != 1 || writes[0].SendUpdates != "" {
		t.Fatalf("got writes %+v, want one patch with no sendUpdates", writes)
	}
	got := fake.Events["me@example.test"]["evguests001"].Reminders
	if got == nil || got.UseDefault || len(got.Overrides) != 2 {
		t.Fatalf("the event's reminders are %+v", got)
	}
	if !strings.Contains(out.Text(), "Reminders are yours alone") {
		t.Fatalf("the result does not say why nobody was notified:\n%s", out.Text())
	}
	r := service.NewWriteResult(out).Event.Reminders
	if r == nil || r.Default || fmt.Sprint(r.Popup, r.Email) != "[10] [1440]" {
		t.Fatalf("the result's reminders are %+v", r)
	}
}

// A split that changes only reminders still makes a new series, which
// reaches the series' guests, so it needs notify like any other write.
func TestARemindersOnlySplitStillNeedsNotify(t *testing.T) {
	svc, fake := writeSeed(t)
	series := caltest.Recurring("evsplitgs02", "Weekly with guests",
		"2026-03-17T16:00:00+01:00", "2026-03-17T17:00:00+01:00", "Europe/Copenhagen",
		"RRULE:FREQ=WEEKLY;COUNT=4")
	series.Organizer = &gcal.EventPerson{Email: "me@example.test", Self: true}
	series.Attendees = []gcal.EventAttendee{
		{Email: "me@example.test", Self: true, Organizer: true},
		{Email: "partner@elsewhere.test"},
	}
	fake.AddEvent("me@example.test", series)
	fake.AddEvent("me@example.test", caltest.Instance("evsplitgs02_20260331T140000Z", "evsplitgs02",
		"Weekly with guests", "2026-03-31T16:00:00+02:00", "2026-03-31T17:00:00+02:00", "Europe/Copenhagen",
		"2026-03-31T16:00:00+02:00"))
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsplitgs02_20260331T140000Z", Scope: "this_and_following",
		PopupReminders: ptr([]int{10}),
	})
	if err == nil {
		t.Fatal("a reminders-only split went through with no notify")
	}
	if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(), "notify") {
		t.Fatalf("got [%s] %v, want [invalid] asking for notify", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Fatalf("the refusal came after writes: %+v", fake.Wrote())
	}
}

// Anything else alongside the reminders is the event's, and reaches its
// guests as before.
func TestRemindersWithAnotherChangeStillNeedNotify(t *testing.T) {
	svc, _ := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evguests001", PopupReminders: ptr([]int{10}), Visibility: ptr("private"),
	})
	if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(), "pass notify") {
		t.Fatalf("got [%s] %v, want notify required", got, err)
	}
}

// Google ignores a less restrictive visibility on one occurrence, so it
// is refused before anything is written.
func TestALessRestrictiveVisibilityOnAnOccurrenceIsRefused(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Events["me@example.test"]["evseries001_20260324T130000Z"].Visibility = gcal.VisibilityPrivate
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z", Scope: "instance",
		Visibility: ptr("public"),
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported || !strings.Contains(err.Error(), "scope:series") {
		t.Fatalf("got [%s] %v, want [unsupported] naming scope:series", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// A more restrictive one is applied to every occurrence, and the result
// says so rather than reporting one occurrence changed.
func TestAMoreRestrictiveVisibilityOnAnOccurrenceSaysItReachesTheSeries(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260324T130000Z", Scope: "instance",
		Visibility: ptr("private"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "makes the whole series private") {
		t.Fatalf("the result does not say the series changed:\n%s", out.Text())
	}
	if got := fake.Events["me@example.test"]["evseries001"].Visibility; got != gcal.VisibilityPrivate {
		t.Fatalf("the fake's series is %q; it does not hold Google's rule", got)
	}
}

// create_event sends reminders, visibility and the guest permissions,
// and get_event reads them back.
func TestCreateEventSetsRemindersVisibilityAndPermissions(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Quiet planning",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
		PopupReminders: ptr([]int{30}), Visibility: "private",
		GuestsCanInviteOthers: ptr(false), GuestsCanModify: ptr(true),
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	got, zone, err := svc.GetEvent(context.Background(), "primary", out.After.ID, "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	res := service.NewEventResult(got, zone)
	if res.Event.Visibility != "private" || res.Event.Reminders == nil ||
		fmt.Sprint(res.Event.Reminders.Popup) != "[30]" || res.Event.Reminders.Default {
		t.Fatalf("got visibility %q and reminders %+v", res.Event.Visibility, res.Event.Reminders)
	}
	if !res.GuestsCanModify || res.GuestsCanInviteOthers || !res.GuestsCanSeeOtherGuests {
		t.Fatalf("got modify %v, invite %v, see %v; want true, false, true",
			res.GuestsCanModify, res.GuestsCanInviteOthers, res.GuestsCanSeeOtherGuests)
	}
	text := res.Render()
	for _, want := range []string{"[private", "your reminders: popup 30 minutes before",
		"guests can change the event, see the guest list; cannot invite others"} {
		if !strings.Contains(text, want) {
			t.Errorf("the card does not say %q:\n%s", want, text)
		}
	}
}

// An event created with no reminder input uses the calendar's own, and
// the card says so.
func TestANewEventUsesTheCalendarsReminders(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Plain",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	r := service.NewWriteResult(out).Event.Reminders
	if r == nil || !r.Default {
		t.Fatalf("got %+v, want the calendar's default reminders", r)
	}
}

// A dry run shows the reminders it would set.
func TestADryRunProjectsReminders(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", EmailReminders: ptr([]int{}), DryRun: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if out.After.Reminders == nil || out.After.Reminders.Default || len(out.After.Reminders.Email) != 0 {
		t.Fatalf("the dry run shows reminders %+v, want none", out.After.Reminders)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("a dry run wrote something")
	}
}

// add_conference asks for a Meet link on an event that has none, under
// conferenceDataVersion=1, which the fake requires as Google does.
func TestAddConferenceAsksForAMeetLink(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", AddConference: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !out.After.Conference.Pending() {
		t.Fatalf("the conference is %+v, want pending", out.After.Conference)
	}
	if !strings.Contains(out.Text(), "still making it") || !strings.Contains(out.Text(), "conference: none → Google Meet link requested") {
		t.Fatalf("the result does not report the request:\n%s", out.Text())
	}
	got, zone, err := svc.GetEvent(context.Background(), "primary", "evsolo00001", "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if text := service.NewEventResult(got, zone).Render(); !strings.Contains(text, "join: https://meet.google.com/") {
		t.Fatalf("the event does not show the link:\n%s", text)
	}
}

// The request id is not the event id. A create with a conference used
// that, and Google ignores a repeated one, so after somebody removed the
// first link, asking again under the event id would be ignored.
func TestAddConferenceAfterTheFirstLinkWasRemoved(t *testing.T) {
	svc, fake := writeSeed(t)
	created, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "primary", Title: "Linked",
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T10:00:00+02:00", Conference: true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	// Somebody removes the link in Google Calendar.
	fake.Events["me@example.test"][created.After.ID].ConferenceData = nil

	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: created.After.ID, AddConference: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !out.After.Conference.Pending() {
		t.Fatalf("the second request was ignored: %+v", out.After.Conference)
	}
}

// An event that has a conference is refused, because Google replaces the
// field whole.
func TestAddConferenceRefusesAnEventThatHasOne(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Events["me@example.test"]["evsolo00001"].ConferenceData =
		json.RawMessage(`{"entryPoints":[{"entryPointType":"video","uri":"https://meet.google.com/aaa-bbbb-ccc"}]}`)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", AddConference: true,
	})
	if got := classOf(t, err); got != gapi.ClassBlocked || !strings.Contains(err.Error(), "already has a conference") {
		t.Fatalf("got [%s] %v, want [blocked]", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// The calendar check create_event makes holds here too.
func TestAddConferenceRefusesACalendarThatForbidsMeet(t *testing.T) {
	svc, fake := writeSeed(t)
	fake.Entries["me@example.test"].ConferenceProperties =
		&gcal.ConferenceProperties{AllowedConferenceSolutionTypes: []string{"eventHangout"}}
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", AddConference: true,
	})
	if got := classOf(t, err); got != gapi.ClassUnsupported {
		t.Fatalf("got [%s] %v, want [unsupported]", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// A split starts a new series and mints no conference for it.
func TestAddConferenceDoesNotGoWithASplit(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evseries001_20260331T120000Z", Scope: "this_and_following",
		AddConference: true,
	})
	if got := classOf(t, err); got != gapi.ClassBlocked || !strings.Contains(err.Error(), "scope:series") {
		t.Fatalf("got [%s] %v, want [blocked] naming scope:series", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// A dry run names the request and reports no conference state it did
// not see.
func TestAddConferenceDryRun(t *testing.T) {
	svc, fake := writeSeed(t)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "evsolo00001", AddConference: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "would be requested") {
		t.Fatalf("the dry run does not mention the conference:\n%s", out.Text())
	}
	if out.After.Conference.Present {
		t.Fatalf("the dry run invented a conference state: %+v", out.After.Conference)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("a dry run wrote something")
	}
}

// ------------------------------------------------------- status events

// awayOptions is an out-of-office event on the primary calendar.
func awayOptions(autoDecline string) service.CreateOptions {
	return service.CreateOptions{
		Calendar: "primary", Title: "Away", EventType: "outOfOffice", AutoDecline: autoDecline,
		Start: "2026-04-01T09:00:00+02:00", End: "2026-04-01T17:00:00+02:00",
	}
}

// A status event goes to Google with its details, and every read carries
// them back in the words create_event took them in.
func TestAStatusEventIsCreatedAndReadBack(t *testing.T) {
	svc, fake := writeSeed(t)
	o := awayOptions("new")
	o.DeclineMessage = "Back on Thursday"
	out, err := svc.CreateEvent(context.Background(), o)
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	stored := fake.Events["me@example.test"][out.After.ID]
	if stored.EventType != gcal.EventTypeOutOfOffice || stored.Transparency != gcal.TransparencyOpaque ||
		string(stored.OutOfOfficeProperties) !=
			`{"autoDeclineMode":"declineOnlyNewConflictingInvitations","declineMessage":"Back on Thursday"}` {
		t.Fatalf("Google was sent type %q, transparency %q, details %s",
			stored.EventType, stored.Transparency, stored.OutOfOfficeProperties)
	}
	if !strings.Contains(out.Text(), "Google declines each invitation for this time that arrives while it stands") {
		t.Errorf("the result does not say what it declines:\n%s", out.Text())
	}
	got, zone, err := svc.GetEvent(context.Background(), "primary", out.After.ID, "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	res := service.NewEventResult(got, zone)
	if ev := res.Event; ev.EventType != "outOfOffice" || ev.AutoDecline != "new" ||
		ev.DeclineMessage != "Back on Thursday" || ev.ChatStatus != "" || ev.WorkingLocation != "" {
		t.Fatalf("read back %+v", ev)
	}
	for _, want := range []string{"[out of office; declines new invitations]", "decline message: Back on Thursday"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the card does not say %q:\n%s", want, res.Render())
		}
	}
}

// A working location reads back with its place and label, public and
// free, as Google requires.
func TestAWorkingLocationReadsBackWithItsPlace(t *testing.T) {
	svc, _ := writeSeed(t)
	out, err := svc.CreateEvent(context.Background(), service.CreateOptions{
		Calendar: "me@example.test", Title: "Office day", EventType: "workingLocation",
		WorkingLocation: "office", WorkingLocationLabel: "Annex", Start: "2026-04-01", End: "2026-04-01",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	ev := service.NewWriteResult(out).Event
	if ev.WorkingLocation != "office" || ev.WorkingLocationLabel != "Annex" || !ev.Transparent ||
		ev.Visibility != "public" || ev.StartDate != "2026-04-01" {
		t.Fatalf("got %+v", ev)
	}
}

// Only the primary calendar holds a status event, so another is refused
// before anything is written.
func TestAStatusEventOnAnotherCalendarIsRefused(t *testing.T) {
	svc, fake := writeSeed(t)
	o := awayOptions("none")
	o.Calendar = "Sample Team"
	_, err := svc.CreateEvent(context.Background(), o)
	if got := classOf(t, err); got != gapi.ClassUnsupported || !strings.Contains(err.Error(), "primary calendar") {
		t.Fatalf("got [%s] %v, want [unsupported] naming the primary calendar", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// A status event with guests is refused for the guests, not asked who to
// email: the plan is built before notify is decided.
func TestAStatusEventWithGuestsIsRefusedForThem(t *testing.T) {
	svc, fake := writeSeed(t)
	o := awayOptions("none")
	o.Guests = []string{"partner@elsewhere.test"}
	_, err := svc.CreateEvent(context.Background(), o)
	if got := classOf(t, err); got != gapi.ClassBlocked || !strings.Contains(err.Error(), "guests") {
		t.Fatalf("got [%s] %v, want [blocked] about guests", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Error("the refusal must come before any write")
	}
}

// Declining every overlapping meeting is put to the person: without a
// way to ask, nothing is written, and once confirmed it is one insert.
// A dry run shows it and asks nothing.
func TestDecliningEveryMeetingIsPutToThePerson(t *testing.T) {
	svc, fake := writeSeed(t)
	_, err := svc.CreateEvent(context.Background(), awayOptions("all"))
	if got := classOf(t, err); got != gapi.ClassBlocked {
		t.Fatalf("got [%s] %v, want [blocked] with no way to ask", got, err)
	}
	if len(fake.Wrote()) != 0 {
		t.Fatal("a decline nobody confirmed was written")
	}

	dry := awayOptions("all")
	dry.DryRun = true
	out, err := svc.CreateEvent(context.Background(), dry)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.Text(), "including the ones you already accepted") || len(fake.Wrote()) != 0 {
		t.Fatalf("the dry run wrote %d times or did not say what it declines:\n%s", len(fake.Wrote()), out.Text())
	}

	if _, err := svc.CreateEvent(accepted(), awayOptions("all")); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if w := fake.Wrote(); len(w) != 1 || w[0].Method != "insert" {
		t.Fatalf("got writes %+v, want one insert", w)
	}
}

// counting is a person who confirms every question and keeps them.
type counting struct{ asked *[]render.Question }

func (c counting) Ask(_ context.Context, q render.Question) error {
	*c.asked = append(*c.asked, q)
	return nil
}

// statusSeries puts a weekly status series of four Tuesdays from 17 March
// on the primary calendar, with the occurrence of 31 March. A timed one
// runs 09:00 to 17:00 in Copenhagen; an all-day one is one day.
func statusSeries(t *testing.T, fake *caltest.Server, id, eventType string, block any, allDay bool) string {
	t.Helper()
	const tz = "Europe/Copenhagen"
	var series, occ *gcal.Event
	occID := id + "_20260331T070000Z"
	if allDay {
		occID = id + "_20260331"
		series = caltest.AllDay(id, "Status", "2026-03-17", "2026-03-18")
		occ = caltest.AllDay(occID, "Status", "2026-03-31", "2026-04-01")
		occ.OriginalStartTime = &gcal.EventDateTime{Date: "2026-03-31"}
	} else {
		series = caltest.Timed(id, "Status", "2026-03-17T09:00:00+01:00", "2026-03-17T17:00:00+01:00", tz)
		occ = caltest.Instance(occID, id, "Status", "2026-03-31T09:00:00+02:00", "2026-03-31T17:00:00+02:00",
			tz, "2026-03-31T09:00:00+02:00")
	}
	series.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"}
	occ.RecurringEventID = id
	for _, e := range []*gcal.Event{series, occ} {
		e.EventType = eventType
		switch eventType {
		case gcal.EventTypeWorkingLocation:
			e.Transparency, e.Visibility = gcal.TransparencyTransparent, gcal.VisibilityPublic
			e.WorkingLocationProperties = gcal.Raw(block)
		case gcal.EventTypeFocusTime:
			e.Transparency = gcal.TransparencyOpaque
			e.FocusTimeProperties = gcal.Raw(block)
		default:
			e.Transparency = gcal.TransparencyOpaque
			e.OutOfOfficeProperties = gcal.Raw(block)
		}
		fake.AddEvent("me@example.test", e)
	}
	return occID
}

// A split of an out-of-office series that declines every meeting makes
// a new series that declines them too, so it is put to the person before
// the truncate, as create_event's is. A dry run says it and asks nothing.
func TestASplitThatDeclinesEveryMeetingIsPutToThePerson(t *testing.T) {
	svc, fake := writeSeed(t)
	occ := statusSeries(t, fake, "evaway00001", gcal.EventTypeOutOfOffice,
		gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineAll}, false)
	o := service.UpdateOptions{
		Calendar: "primary", EventID: occ, Scope: "this_and_following", End: "2026-03-31T19:00:00+02:00",
	}

	_, err := svc.UpdateEvent(context.Background(), o)
	if got := classOf(t, err); got != gapi.ClassBlocked {
		t.Fatalf("got [%s] %v, want [blocked] with no way to ask", got, err)
	}
	if w := fake.Wrote(); len(w) != 0 {
		t.Fatalf("a decline nobody confirmed was written: %+v", w)
	}

	var asked []render.Question
	ctx := service.WithAsker(context.Background(), counting{&asked})
	dry := o
	dry.DryRun = true
	out, err := svc.UpdateEvent(ctx, dry)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.Text(), "Google declines every meeting this overlaps, including the ones you already "+
		"accepted") || len(asked) != 0 || len(fake.Wrote()) != 0 {
		t.Fatalf("the dry run asked %d times, wrote %d times, or did not say what it declines:\n%s",
			len(asked), len(fake.Wrote()), out.Text())
	}

	if _, err := svc.UpdateEvent(ctx, o); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if len(asked) != 1 || !strings.HasPrefix(asked[0].Text, "update_event: split the out-of-office event `Status`") {
		t.Fatalf("asked %d times: %+v", len(asked), asked)
	}
	if w := fake.Wrote(); len(w) != 2 || w[0].Method != "patch" || w[1].Method != "insert" {
		t.Fatalf("got writes %+v, want a truncate and an insert", w)
	}
}

// A split that declines less asks nothing: only all is put to the person.
func TestASplitThatDeclinesOnlyNewInvitationsAsksNothing(t *testing.T) {
	svc, fake := writeSeed(t)
	occ := statusSeries(t, fake, "evaway00002", gcal.EventTypeOutOfOffice,
		gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNew}, false)
	out, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: occ, Scope: "this_and_following", Title: ptr("Away"),
	})
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !strings.Contains(out.Text(), "Google declines each invitation for this time that arrives while it stands") {
		t.Errorf("the result does not say what the new series declines:\n%s", out.Text())
	}
	if w := fake.Wrote(); len(w) != 2 {
		t.Fatalf("got writes %+v, want a truncate and an insert", w)
	}
}

// A write that would leave a status event in a shape Google refuses is
// refused before anything is sent: for a split, before the truncate,
// because a refusal after it leaves the series cut short.
func TestAStatusEventIsHeldToGooglesRulesOnEveryWrite(t *testing.T) {
	away := gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNone}
	focus := gcal.EventFocusTimeProperties{AutoDeclineMode: gcal.AutoDeclineNone}
	home := gcal.EventWorkingLocationProperties{Type: gcal.WorkingHome, HomeOffice: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		name, eventType string
		block           any
		allDay          bool
		scope           string
		change          func(*service.UpdateOptions)
		class           gapi.Class
		says            string
	}{
		{"free out of office, split", gcal.EventTypeOutOfOffice, away, false, "this_and_following",
			func(o *service.UpdateOptions) { o.Transparent = ptr(true) },
			gapi.ClassInvalid, "an out-of-office event always shows you as busy"},
		{"free out of office, whole series", gcal.EventTypeOutOfOffice, away, false, "series",
			func(o *service.UpdateOptions) { o.Transparent = ptr(true) },
			gapi.ClassInvalid, "an out-of-office event always shows you as busy"},
		{"free out of office, one occurrence", gcal.EventTypeOutOfOffice, away, false, "instance",
			func(o *service.UpdateOptions) { o.Transparent = ptr(true) },
			gapi.ClassInvalid, "an out-of-office event always shows you as busy"},
		{"all-day focus time, split", gcal.EventTypeFocusTime, focus, false, "this_and_following",
			func(o *service.UpdateOptions) { o.Start, o.End = "2026-03-31", "2026-03-31" },
			gapi.ClassUnsupported, "focus time cannot be all day"},
		{"a private working location, split", gcal.EventTypeWorkingLocation, home, false, "this_and_following",
			func(o *service.UpdateOptions) { o.Visibility = ptr("private") },
			gapi.ClassInvalid, "a working location is always public"},
		{"a busy working location, whole series", gcal.EventTypeWorkingLocation, home, false, "series",
			func(o *service.UpdateOptions) { o.Transparent = ptr(false) },
			gapi.ClassInvalid, "a working location always shows you free"},
		{"a two-day working location, split", gcal.EventTypeWorkingLocation, home, true, "this_and_following",
			func(o *service.UpdateOptions) { o.End = "2026-04-01" },
			gapi.ClassUnsupported, "covers exactly one day"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			occ := statusSeries(t, fake, "evstatus001", tc.eventType, tc.block, tc.allDay)
			o := service.UpdateOptions{Calendar: "primary", EventID: occ, Scope: tc.scope, Notify: "none"}
			tc.change(&o)
			_, err := svc.UpdateEvent(context.Background(), o)
			if got := classOf(t, err); got != tc.class || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("got [%s] %v, want [%s] saying %q", got, err, tc.class, tc.says)
			}
			if w := fake.Wrote(); len(w) != 0 {
				t.Fatalf("the refusal came after %d writes: %+v", len(w), w)
			}
		})
	}
}

// A change a status event can take goes through: a later end on one
// occurrence, and a new title on the whole of a working location.
func TestAStatusEventTakesTheChangesGoogleAllows(t *testing.T) {
	svc, fake := writeSeed(t)
	occ := statusSeries(t, fake, "evstatus002", gcal.EventTypeOutOfOffice,
		gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNone}, false)
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: occ, Scope: "instance", End: "2026-03-31T18:00:00+02:00",
	}); err != nil {
		t.Fatalf("a later end: %v", err)
	}
	office := statusSeries(t, fake, "evstatus003", gcal.EventTypeWorkingLocation,
		gcal.EventWorkingLocationProperties{Type: gcal.WorkingCustom,
			CustomLocation: &gcal.WorkingLocationCustom{Label: "Library"}}, true)
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: office, Scope: "series", Title: ptr("Library day"),
	}); err != nil {
		t.Fatalf("a new title: %v", err)
	}
	if w := fake.Wrote(); len(w) != 2 {
		t.Fatalf("got writes %+v, want two patches", w)
	}
}

// Guests, rooms and a Meet link are refused on an existing status event
// as on a new one, at every scope and through a split, before notify is
// decided and before anything is written. Removing a guest is not.
func TestAStatusEventTakesNoGuestsOnAnyWrite(t *testing.T) {
	for _, scope := range []string{"instance", "series", "this_and_following"} {
		for _, tc := range []struct {
			name   string
			change func(*service.UpdateOptions)
		}{
			{"a guest", func(o *service.UpdateOptions) { o.AddGuests = []string{"partner@elsewhere.test"} }},
			{"an optional guest", func(o *service.UpdateOptions) {
				o.AddOptionalGuests = []string{"colleague@example.test"}
			}},
			{"a room", func(o *service.UpdateOptions) {
				o.AddRooms = []string{"room-sample@resource.calendar.google.com"}
			}},
			{"a Meet link", func(o *service.UpdateOptions) { o.AddConference = true }},
		} {
			if scope == "this_and_following" && tc.name == "a Meet link" {
				// Refused for the split itself; TestAddConferenceDoesNotGoWithASplit.
				continue
			}
			t.Run(scope+" "+tc.name, func(t *testing.T) {
				svc, fake := writeSeed(t)
				occ := statusSeries(t, fake, "evaway00003", gcal.EventTypeOutOfOffice,
					gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNone}, false)
				o := service.UpdateOptions{Calendar: "primary", EventID: occ, Scope: scope}
				tc.change(&o)
				_, err := svc.UpdateEvent(context.Background(), o)
				if got := classOf(t, err); got != gapi.ClassBlocked ||
					!strings.Contains(err.Error(), "does not put guests, rooms or a Meet link on an out-of-office event") {
					t.Fatalf("got [%s] %v, want [blocked] about guests, rooms and Meet", got, err)
				}
				if w := fake.Wrote(); len(w) != 0 {
					t.Fatalf("the refusal came after %d writes: %+v", len(w), w)
				}
			})
		}
	}

	svc, fake := writeSeed(t)
	occ := statusSeries(t, fake, "evaway00004", gcal.EventTypeOutOfOffice,
		gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNone}, false)
	away := fake.Events["me@example.test"]["evaway00004"]
	away.Attendees = []gcal.EventAttendee{{Email: "colleague@example.test"}}
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: occ, Scope: "series", RemoveGuests: []string{"colleague@example.test"},
		Notify: "all",
	}); err != nil {
		t.Fatalf("removing a guest: %v", err)
	}
}

// declining puts statusSeries' weekly series on the primary calendar,
// declining every meeting it overlaps and repeating by rule, and returns
// the id of its occurrence of 31 March.
func declining(t *testing.T, fake *caltest.Server, id, eventType, rule string) string {
	t.Helper()
	var block any = gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineAll}
	if eventType == gcal.EventTypeFocusTime {
		block = gcal.EventFocusTimeProperties{AutoDeclineMode: gcal.AutoDeclineAll}
	}
	occ := statusSeries(t, fake, id, eventType, block, false)
	fake.Events["me@example.test"][id].Recurrence = []string{rule}
	return occ
}

const weeklyFour = "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"

// A change that leaves an out-of-office event declining every meeting
// over time it did not cover is put to the person before the patch, as a
// create is. A dry run says it and asks nothing.
func TestACoverThatGrowsIsPutToThePerson(t *testing.T) {
	svc, fake := writeSeed(t)
	occ := declining(t, fake, "evaway00005", gcal.EventTypeOutOfOffice, weeklyFour)
	o := service.UpdateOptions{Calendar: "primary", EventID: occ, Scope: "instance", End: "2026-03-31T19:00:00+02:00"}

	_, err := svc.UpdateEvent(context.Background(), o)
	if got := classOf(t, err); got != gapi.ClassBlocked {
		t.Fatalf("got [%s] %v, want [blocked] with no way to ask", got, err)
	}
	if w := fake.Wrote(); len(w) != 0 {
		t.Fatalf("a decline nobody confirmed was written: %+v", w)
	}

	var asked []render.Question
	ctx := service.WithAsker(context.Background(), counting{&asked})
	dry := o
	dry.DryRun = true
	out, err := svc.UpdateEvent(ctx, dry)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.Text(), "This change covers time the event did not, and the event declines every "+
		"meeting it overlaps.") || len(asked) != 0 || len(fake.Wrote()) != 0 {
		t.Fatalf("the dry run asked %d times, wrote %d times, or did not say what it declines:\n%s",
			len(asked), len(fake.Wrote()), out.Text())
	}

	if _, err := svc.UpdateEvent(ctx, o); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	want := "update_event: change the out-of-office event `Status` on your primary calendar `Sample Primary`, so " +
		"it covers time it did not and declines every meeting it overlaps?\n\n" +
		"It was 2026-03-31 09:00-17:00 Europe/Copenhagen.\n\n" +
		"It would be 2026-03-31 09:00-19:00 Europe/Copenhagen.\n\n" +
		"That includes meetings you already accepted.\n\n"
	if len(asked) != 1 || !strings.HasPrefix(asked[0].Text, want) {
		t.Fatalf("asked %d times, want once:\n%+v", len(asked), asked)
	}
	if w := fake.Wrote(); len(w) != 1 || w[0].Method != "patch" || w[0].EventID != occ {
		t.Fatalf("got writes %+v, want one patch of the occurrence", w)
	}
}

// Every way a change can make such an event cover more asks: moved, made
// longer, repeated more, or made to repeat at all.
func TestEveryCoverThatGrowsAsks(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, rule, scope string
		change                       func(*service.UpdateOptions)
		// shows is what the question says, where it says more than
		// that it changes the event.
		shows string
	}{
		{"an earlier start on the series", gcal.EventTypeOutOfOffice, weeklyFour, "series",
			func(o *service.UpdateOptions) { o.Start = "2026-03-17T08:00:00+01:00" }, ""},
		{"an earlier start on one occurrence", gcal.EventTypeOutOfOffice, weeklyFour, "instance",
			func(o *service.UpdateOptions) { o.Start = "2026-03-31T08:00:00+02:00" }, ""},
		{"one occurrence, a week before the series began", gcal.EventTypeOutOfOffice, weeklyFour, "series",
			func(o *service.UpdateOptions) {
				o.Start, o.End = "2026-03-10T09:00:00+01:00", "2026-03-10T17:00:00+01:00"
				o.Recurrence = &[]string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=1"}
			}, ""},
		{"the same length later in the day", gcal.EventTypeOutOfOffice, weeklyFour, "instance",
			func(o *service.UpdateOptions) {
				o.Start, o.End = "2026-03-31T10:00:00+02:00", "2026-03-31T18:00:00+02:00"
			}, ""},
		{"more occurrences", gcal.EventTypeOutOfOffice, weeklyFour, "series",
			func(o *service.UpdateOptions) { o.Recurrence = &[]string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=8"} },
			"\n\nThe series started 2026-03-17 09:00-17:00 Europe/Copenhagen, repeating every week on Tuesday, 4 " +
				"times.\n\nThe series would start 2026-03-17 09:00-17:00 Europe/Copenhagen.\n\nIt repeats every week " +
				"on Tuesday, 8 times, and declines on every occurrence.\n\n"},
		{"a series twenty years long made endless", gcal.EventTypeOutOfOffice,
			"RRULE:FREQ=DAILY;UNTIL=20460317T235959Z", "series",
			func(o *service.UpdateOptions) { o.Recurrence = &[]string{"RRULE:FREQ=DAILY"} }, ""},
		{"focus time made longer", gcal.EventTypeFocusTime, weeklyFour, "instance",
			func(o *service.UpdateOptions) { o.End = "2026-03-31T18:00:00+02:00" },
			"update_event: change the focus time `Status`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			occ := declining(t, fake, "evaway00006", tc.eventType, tc.rule)
			var asked []render.Question
			o := service.UpdateOptions{Calendar: "primary", EventID: occ, Scope: tc.scope}
			tc.change(&o)
			if _, err := svc.UpdateEvent(service.WithAsker(context.Background(), counting{&asked}), o); err != nil {
				t.Fatalf("UpdateEvent: %v", err)
			}
			if len(asked) != 1 || !strings.HasPrefix(asked[0].Text, "update_event: change ") ||
				!strings.Contains(asked[0].Text, tc.shows) {
				t.Fatalf("asked %d times, want once, showing %q: %+v", len(asked), tc.shows, asked)
			}
		})
	}

	svc, fake := writeSeed(t)
	one := caltest.Timed("evaway00007", "Status", "2026-03-31T09:00:00+02:00", "2026-03-31T17:00:00+02:00",
		"Europe/Copenhagen")
	one.EventType, one.Transparency = gcal.EventTypeOutOfOffice, gcal.TransparencyOpaque
	one.OutOfOfficeProperties = gcal.Raw(gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineAll})
	fake.AddEvent("me@example.test", one)
	var asked []render.Question
	if _, err := svc.UpdateEvent(service.WithAsker(context.Background(), counting{&asked}), service.UpdateOptions{
		Calendar: "primary", EventID: one.ID, Recurrence: &[]string{"RRULE:FREQ=WEEKLY;COUNT=2"},
	}); err != nil {
		t.Fatalf("made to repeat: %v", err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0].Text, "\n\nIt was 2026-03-31 09:00-17:00 Europe/Copenhagen."+
		"\n\nThe series would start 2026-03-31 09:00-17:00 Europe/Copenhagen.\n\nIt repeats ") {
		t.Fatalf("made to repeat: asked %d times, want once: %+v", len(asked), asked)
	}
}

// A change that only shrinks the time such an event covers asks nothing,
// and neither does one to an event that declines less, or to a canceled
// occurrence, or one that leaves the time alone.
func TestACoverThatOnlyShrinksAsksNothing(t *testing.T) {
	for _, tc := range []struct {
		name, rule, scope string
		change            func(*service.UpdateOptions)
		setup             func(fake *caltest.Server, occ string)
	}{
		{"an earlier end on one occurrence", weeklyFour, "instance",
			func(o *service.UpdateOptions) { o.End = "2026-03-31T16:00:00+02:00" }, nil},
		{"a later start on the series", weeklyFour, "series",
			func(o *service.UpdateOptions) { o.Start = "2026-03-17T10:00:00+01:00" }, nil},
		{"fewer occurrences", weeklyFour, "series",
			func(o *service.UpdateOptions) { o.Recurrence = &[]string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=2"} }, nil},
		{"the repetition ended", weeklyFour, "series",
			func(o *service.UpdateOptions) { o.Recurrence = &[]string{} }, nil},
		{"an endless series made shorter each day", "RRULE:FREQ=WEEKLY;BYDAY=TU", "series",
			func(o *service.UpdateOptions) { o.End = "2026-03-17T16:00:00+01:00" }, nil},
		{"a new title on a series this server cannot expand", "RRULE:FREQ=HOURLY;COUNT=4", "series",
			func(o *service.UpdateOptions) { o.Title = ptr("Away") }, nil},
		{"a later end on an occurrence that declines only new invitations", weeklyFour, "instance",
			func(o *service.UpdateOptions) { o.End = "2026-03-31T19:00:00+02:00" },
			func(fake *caltest.Server, occ string) {
				fake.Events["me@example.test"][occ].OutOfOfficeProperties = gcal.Raw(
					gcal.EventOutOfOfficeProperties{AutoDeclineMode: gcal.AutoDeclineNew})
			}},
		{"a later end on a canceled occurrence", weeklyFour, "instance",
			func(o *service.UpdateOptions) { o.End = "2026-03-31T19:00:00+02:00" },
			func(fake *caltest.Server, occ string) {
				fake.Events["me@example.test"][occ].Status = gcal.StatusCanceled
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := writeSeed(t)
			occ := declining(t, fake, "evaway00008", gcal.EventTypeOutOfOffice, tc.rule)
			if tc.setup != nil {
				tc.setup(fake, occ)
			}
			var asked []render.Question
			o := service.UpdateOptions{Calendar: "primary", EventID: occ, Scope: tc.scope}
			tc.change(&o)
			if _, err := svc.UpdateEvent(service.WithAsker(context.Background(), counting{&asked}), o); err != nil {
				t.Fatalf("UpdateEvent: %v", err)
			}
			if len(asked) != 0 {
				t.Fatalf("asked %d times, want none: %+v", len(asked), asked)
			}
			if w := fake.Wrote(); len(w) != 1 || w[0].Method != "patch" {
				t.Fatalf("got writes %+v, want one patch", w)
			}
		})
	}
}
