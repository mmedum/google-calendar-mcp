package plan_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
)

func TestParseAudienceReadsAnAddressAsOnePerson(t *testing.T) {
	got, err := plan.ParseAudience("somebody@example.test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope.Type != gcal.ScopeTypeUser || got.Scope.Value != "somebody@example.test" {
		t.Fatalf("scope %+v, want one user", got.Scope)
	}
	// It says it inferred, because a group address is the same shape.
	if got.Source != plan.SourceAddress {
		t.Fatalf("source %q, want the inference to be named", got.Source)
	}
}

// The strongest inference there is: this calendar already has a rule for
// that address, so Google has already answered the question.
func TestParseAudienceReusesTheScopeTypeOfAnExistingRule(t *testing.T) {
	existing := []model.Sharing{
		{RuleID: "group:team@example.test", ScopeType: gcal.ScopeTypeGroup,
			Value: "team@example.test", Role: gcal.RoleReader},
	}
	got, err := plan.ParseAudience("TEAM@example.test", "", existing)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope.Type != gcal.ScopeTypeGroup {
		t.Fatalf("scope type %q, want group — the calendar already says so", got.Scope.Type)
	}
	if got.Source != plan.SourceExisting {
		t.Fatalf("source %q, want the existing rule", got.Source)
	}
}

// A bare word with a dot is a whole domain, which is far too large a
// difference to infer from a missing @.
func TestParseAudienceRefusesToGuessADomain(t *testing.T) {
	_, err := plan.ParseAudience("example.test", "", nil)
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
	if !strings.Contains(err.Error(), "scope_type:domain") {
		t.Fatalf("the refusal does not say how to mean it: %v", err)
	}
}

func TestParseAudienceTakesTheWordsForPublic(t *testing.T) {
	for _, who := range []string{"anyone", "public", "everyone", "default"} {
		got, err := plan.ParseAudience(who, "", nil)
		if err != nil {
			t.Fatalf("%q: %v", who, err)
		}
		if !got.Scope.IsPublic() {
			t.Fatalf("%q did not read as the public scope", who)
		}
		if got.Scope.Value != "" {
			t.Fatalf("%q carried a value %q; the public scope has none", who, got.Scope.Value)
		}
	}
}

// A public rule takes no address, so one passed alongside it would be
// silently ignored — which is how a caller comes to believe they shared
// with one person.
func TestParseAudienceRefusesAnAddressOnThePublicScope(t *testing.T) {
	_, err := plan.ParseAudience("somebody@example.test", "default", nil)
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
}

func TestParseAudienceValidatesWhatTheTypeImplies(t *testing.T) {
	if _, err := plan.ParseAudience("not-a-domain", "domain", nil); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("a domain rule accepted %q", "not-a-domain")
	}
	if _, err := plan.ParseAudience("example.test", "user", nil); !errors.Is(err, plan.ErrInvalid) {
		t.Fatal("a user rule accepted something that is not an address")
	}
	if _, err := plan.ParseAudience("somebody@example.test", "clique", nil); !errors.Is(err, plan.ErrInvalid) {
		t.Fatal("an unknown scope type was accepted")
	}
}

// §7.6: the public scope is the one ACL value that cannot be undone from
// the other side, so it takes a flag of its own.
func TestAllowPublicGuardsOnlyThePublicScope(t *testing.T) {
	public, err := plan.ParseAudience("anyone", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.AllowPublic(public, false); !errors.Is(err, plan.ErrBlocked) {
		t.Fatalf("error %v, want blocked", err)
	}
	if err := plan.AllowPublic(public, true); err != nil {
		t.Fatalf("the flag did not allow it: %v", err)
	}

	person, err := plan.ParseAudience("somebody@example.test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.AllowPublic(person, false); err != nil {
		t.Fatalf("an ordinary share needed the public flag: %v", err)
	}
}

func TestParseRoleTakesBothSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"reader":                     gcal.RoleReader,
		"READER":                     gcal.RoleReader,
		"writer":                     gcal.RoleWriter,
		"owner":                      gcal.RoleOwner,
		"freeBusyReader":             gcal.RoleFreeBusyReader,
		"free_busy":                  gcal.RoleFreeBusyReader,
		"writerWithoutPrivateAccess": gcal.RoleWriterWithoutPrivateData,
	} {
		got, err := plan.ParseRole(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Fatalf("%q became %q, want %q", in, got, want)
		}
	}
}

// role:none is how acl.list REPORTS a deleted rule. Writing one would
// leave a rule granting nothing that reads as if it did something.
func TestParseRoleRefusesNoneAndSaysWhatToUse(t *testing.T) {
	_, err := plan.ParseRole("none")
	if !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
	if !strings.Contains(err.Error(), "unshare_calendar") {
		t.Fatalf("the refusal does not name the tool that removes access: %v", err)
	}
}

func TestRoleChoicesExplainEveryRole(t *testing.T) {
	choices := plan.RoleChoices()
	for _, r := range gcal.Roles {
		if !strings.Contains(choices, r) {
			t.Fatalf("the role list omits %q", r)
		}
	}
	// Explained, not echoed: the point of the list is that the names do
	// not say what they do.
	if !strings.Contains(choices, gcal.RoleMeans(gcal.RoleWriterWithoutPrivateData)) {
		t.Fatalf("the roles are echoed rather than explained:\n%s", choices)
	}
}

// §4.3 at the ACL: no default in either direction, and Google's own
// default here is the opposite of the one on an event.
func TestShareNotifyHasNoDefault(t *testing.T) {
	if _, err := plan.ShareNotify(""); !errors.Is(err, plan.ErrInvalid) {
		t.Fatalf("error %v, want invalid", err)
	}
	on, err := plan.ShareNotify("all")
	if err != nil || !on {
		t.Fatalf("notify:all became %v, %v", on, err)
	}
	off, err := plan.ShareNotify("none")
	if err != nil || off {
		t.Fatalf("notify:none became %v, %v", off, err)
	}
}

// external_only splits guests on a domain; an ACL notification is one
// switch. Rounding it either way would email the wrong set of people, so
// it is refused as unsupported.
func TestShareNotifyRefusesExternalOnly(t *testing.T) {
	_, err := plan.ShareNotify("external_only")
	if !errors.Is(err, plan.ErrUnsupported) {
		t.Fatalf("error %v, want unsupported", err)
	}
}

// The report says what was ASKED FOR, and never that anything arrived.
func TestShareReportNeverClaimsDelivery(t *testing.T) {
	person, err := plan.ParseAudience("somebody@example.test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.ShareReport(plan.ShareGranted, true, person)
	if !strings.Contains(got, "Asked Google") {
		t.Fatalf("report %q does not say it is a request", got)
	}
	if !strings.Contains(got, "not what arrived") {
		t.Fatalf("report %q claims delivery", got)
	}

	quiet := plan.ShareReport(plan.ShareGranted, false, person)
	if !strings.Contains(quiet, "not a promise of silence") {
		t.Fatalf("notify:none reported as silence: %q", quiet)
	}
	if !strings.Contains(quiet, "access either way") {
		t.Fatalf("report %q does not say the access happens regardless", quiet)
	}
}

// A public rule names nobody to write to, and the report says that
// rather than implying an email went somewhere.
func TestShareReportOnAPublicRuleNamesNobody(t *testing.T) {
	public, err := plan.ParseAudience("anyone", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.ShareReport(plan.ShareGranted, true, public); !strings.Contains(got, "names nobody") {
		t.Fatalf("report %q does not say a public rule has no recipient", got)
	}
}

func TestFindRuleMatchesCaseInsensitively(t *testing.T) {
	rules := []model.Sharing{
		{RuleID: "user:Somebody@Example.test", ScopeType: gcal.ScopeTypeUser,
			Value: "Somebody@Example.test", Role: gcal.RoleReader},
	}
	audience, err := plan.ParseAudience("somebody@example.test", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := plan.FindRule(rules, audience)
	if !ok {
		t.Fatal("the same address in another case did not match")
	}
	if got.RuleID != "user:Somebody@Example.test" {
		t.Fatalf("matched %q", got.RuleID)
	}
}

func TestDescribeNamesTheAudience(t *testing.T) {
	for who, want := range map[string]string{
		"anyone":                "ANYONE",
		"somebody@example.test": "somebody@example.test",
	} {
		a, err := plan.ParseAudience(who, "", nil)
		if err != nil {
			t.Fatalf("%q: %v", who, err)
		}
		if got := plan.Describe(a); !strings.Contains(got, want) {
			t.Fatalf("%q described as %q, want it to contain %q", who, got, want)
		}
	}
	domain, err := plan.ParseAudience("example.test", "domain", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Describe(domain); !strings.Contains(got, "everybody in example.test") {
		t.Fatalf("a domain rule described as %q", got)
	}
}

// The two outcomes that write nothing say so without promising silence,
// and the removal one says why there was never a choice to make.
func TestShareReportCoversTheOutcomesThatSendNothing(t *testing.T) {
	person, err := plan.ParseAudience("somebody@example.test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := plan.ShareReport(plan.ShareUnchanged, true, person)
	if !strings.Contains(unchanged, "nothing was written") {
		t.Fatalf("an unchanged share reported %q", unchanged)
	}
	removed := plan.ShareReport(plan.ShareRemoved, false, person)
	if !strings.Contains(removed, "no way to ask") {
		t.Fatalf("a removal reported %q, want it to say Google offers no notification", removed)
	}
	if strings.Contains(removed, "Nobody was emailed") {
		t.Fatalf("a removal claims silence it cannot promise: %q", removed)
	}
}
