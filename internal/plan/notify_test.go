package plan_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
)

// §4.3's whole point is that there is no default, so the first thing to
// hold is that an absent choice is a refusal rather than a value.
func TestNotifyIsRequiredWhenTheWriteReachesSomebody(t *testing.T) {
	r := plan.ReachOfAddresses("me@example.test", []string{"a@example.test", "b@example.test"})
	if r.Guests != 2 {
		t.Fatalf("counted %d guests, want 2", r.Guests)
	}
	_, err := plan.Notification("", r)
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("an absent notify must be [invalid], got %v", err)
	}
	// The count goes in the refusal; the addresses never do (§9).
	if !strings.Contains(err.Error(), "2 guests") {
		t.Errorf("the refusal must say how many people it would reach: %v", err)
	}
	for _, addr := range []string{"a@example.test", "b@example.test"} {
		if strings.Contains(err.Error(), addr) {
			t.Errorf("the refusal leaked an address: %v", err)
		}
	}
	// And it says what each choice would do, or a caller cannot choose.
	for _, want := range []string{"none", "external_only", "all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
}

// §4.3.2: a write that reaches nobody has no decision to make, and
// demanding one is friction with no safety in it.
func TestNotifyIsNotAskedWhenNobodyIsReached(t *testing.T) {
	d, err := plan.Notification("", plan.ReachOfAddresses("me@example.test", nil))
	if err != nil {
		t.Fatalf("an event with no guests must not demand notify: %v", err)
	}
	if d.Asked {
		t.Fatal("nothing to ask about, so nothing should be sent as sendUpdates")
	}
	if !strings.Contains(d.Report(), "Nobody to notify") {
		t.Errorf("the report should say why no choice was needed, got %q", d.Report())
	}
}

// Inviting yourself is not reaching somebody.
func TestTheOrganiserIsNotAGuest(t *testing.T) {
	r := plan.ReachOfAddresses("me@example.test", []string{"ME@example.test"})
	if r.Any() {
		t.Fatalf("the organiser's own address counted as a guest: %+v", r)
	}
}

// §4.3.4 and spike B: `none` is refused, not warned about, when a guest
// is outside the organiser's domain. That guest may have no calendar for
// the event to land in, so mail is the only channel there is.
func TestNoneIsRefusedForAnOutOfDomainGuest(t *testing.T) {
	r := plan.ReachOfAddresses("me@example.test", []string{"inside@example.test", "outside@elsewhere.test"})
	if r.External != 1 {
		t.Fatalf("counted %d external guests, want 1: %+v", r.External, r)
	}
	_, err := plan.Notification("none", r)
	if !errors.Is(err, plan.ErrBlocked) {
		t.Fatalf("notify:none with an outside guest must be [blocked], got %v", err)
	}
	if strings.Contains(err.Error(), "elsewhere.test") {
		t.Errorf("the refusal leaked the guest's domain: %v", err)
	}
	// The two ways out are named, or the refusal is a dead end.
	if !strings.Contains(err.Error(), "external_only") || !strings.Contains(err.Error(), "all") {
		t.Errorf("the refusal must name the choices that work: %v", err)
	}
}

// The same call with every guest inside the domain is allowed: §4.3.4 is
// about the guest who cannot discover the event any other way.
func TestNoneIsAllowedInsideTheDomain(t *testing.T) {
	d, err := plan.Notification("none", plan.ReachOfAddresses("me@example.test", []string{"a@example.test"}))
	if err != nil {
		t.Fatalf("notify:none with only same-domain guests: %v", err)
	}
	if d.Notify != plan.NotifyNone || d.SendUpdatesFor() != gcal.SendUpdatesNone {
		t.Fatalf("got %q on the wire, want none", d.SendUpdatesFor())
	}
	// §4.3.3: never reported as silence.
	if !strings.Contains(d.Report(), "not a promise of silence") {
		t.Errorf("a `none` report must not promise silence, got %q", d.Report())
	}
}

// An unknown organiser domain makes every guest external, which fails
// towards the refusal rather than past it.
func TestAnUnknownOrganiserDomainCountsEverybodyAsOutside(t *testing.T) {
	r := plan.ReachOfAddresses("", []string{"a@example.test"})
	if r.External != 1 {
		t.Fatalf("with no organiser domain every guest is external; got %+v", r)
	}
	if _, err := plan.Notification("none", r); !errors.Is(err, plan.ErrBlocked) {
		t.Fatalf("want [blocked] when the domain is unknown, got %v", err)
	}
}

// Google's own spelling is accepted: a model that read the API reference
// will type the one it saw there.
func TestGoogleSpellingOfExternalOnly(t *testing.T) {
	for _, v := range []string{"external_only", "externalOnly", "EXTERNALONLY"} {
		got, err := plan.ParseNotify(v)
		if err != nil || got != plan.NotifyExternalOnly {
			t.Fatalf("ParseNotify(%q) = %q, %v", v, got, err)
		}
	}
	if got := plan.NotifyExternalOnly.SendUpdates(); got != gcal.SendUpdatesExternalOnly {
		t.Fatalf("wire value %q, want %q", got, gcal.SendUpdatesExternalOnly)
	}
}

func TestUnknownNotifyIsRefusedWithTheChoices(t *testing.T) {
	_, err := plan.Notification("maybe", plan.ReachOfAddresses("me@example.test", []string{"a@example.test"}))
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("want [invalid], got %v", err)
	}
	if !strings.Contains(err.Error(), "external_only") {
		t.Errorf("the refusal must list the choices: %v", err)
	}
}

// A room is not a person, and neither is the account itself.
func TestReachOfEventIgnoresResourcesAndSelf(t *testing.T) {
	e := model.Event{Attendees: []model.Attendee{
		{Email: "me@example.test", Self: true},
		{Email: "room@example.test", Resource: true},
		{Email: "colleague@example.test"},
		{Email: "outside@elsewhere.test"},
	}}
	r := plan.ReachOfEvent("me@example.test", e)
	if r.Guests != 2 || r.External != 1 {
		t.Fatalf("got %+v, want 2 guests and 1 external", r)
	}
}

// §4.3.3: `all` is not delivery either. Spike A watched one invitation
// reach one of two guests with nothing different in the request.
func TestAllIsReportedAsAskedForNotAsDelivered(t *testing.T) {
	d, err := plan.Notification("all", plan.ReachOfAddresses("me@example.test", []string{"a@example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	report := d.Report()
	if !strings.Contains(report, "asked for") {
		t.Errorf("the report must say what was asked for, got %q", report)
	}
	if strings.Contains(strings.ToLower(report), "delivered") || strings.Contains(report, "were emailed") {
		t.Errorf("the report claims delivery the API never reports: %q", report)
	}
}
