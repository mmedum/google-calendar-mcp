package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v2/internal/plan"
	"github.com/mmedum/google-calendar-mcp/v2/internal/service"
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
		Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 2"),
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
		Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 2"),
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
		Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 2"), Force: true,
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
		Calendar: "primary", EventID: "evseries001", Title: strptr("Renamed"),
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
		Scope: "series", Title: strptr("Weekly sync"),
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
		Calendar: "primary", EventID: "evseries001", Scope: "instance", Title: strptr("Renamed"),
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
		Scope: "instance", Location: strptr("Room 3"),
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
		Scope: "instance", Location: strptr("Room 3"),
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
		Scope: "this_and_following", Title: strptr("Weekly sync"),
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
	out, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	out, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	out, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	quiet, err := svc2.CancelEvent(context.Background(), service.CancelOptions{
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

	_, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	out, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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

func strptr(s string) *string { return &s }

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
				Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 2"),
			})
			return err
		}, "list, settings, read, patch"},
		{"cancel", 4, func(s *service.Service) error {
			_, err := s.CancelEvent(context.Background(), service.CancelOptions{
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
				Scope:         "this_and_following", Title: strptr("Renamed"),
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
		Scope: "this_and_following", Title: strptr("Renamed"), Notify: "all",
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
				Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 2"),
			})
			return o.Requests, err
		}},
		{"this_and_following", func(s *service.Service) (int, error) {
			o, err := s.UpdateEvent(context.Background(), service.UpdateOptions{
				Calendar: "primary", EventID: "evseries001",
				OriginalStart: "2026-03-31T14:00:00+02:00",
				Scope:         "this_and_following", Title: strptr("Renamed"),
			})
			return o.Requests, err
		}},
		{"cancel", func(s *service.Service) (int, error) {
			o, err := s.CancelEvent(context.Background(), service.CancelOptions{
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
		Scope: "this_and_following", Title: strptr("Renamed all-day"),
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

	out, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
		Scope: "series", Title: strptr("Renamed"), ETag: occ.ETag,
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
		Calendar: "Sample Team", EventID: "evteamev001", Location: strptr("Room 9"), Notify: "none",
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
		Calendar: "Sample Team", EventID: "evteamev002", Location: strptr("Room 9"), Notify: "none",
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
		Calendar: "primary", EventID: "evsolo00001", Location: strptr("Room 9"), DryRun: true,
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

	c, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	_, err := svc.CancelEvent(context.Background(), service.CancelOptions{
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
	ctx := context.Background()

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
