package plan

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v5/internal/model"
)

// The sharing half of the plan package (§7.6).
//
// Sharing is the one act here whose effect leaves the account, so the
// guards are about blast radius rather than about correctness: who a
// rule reaches, whether the caller meant the public internet, and
// whether anybody is emailed about it.

// Audience is who a sharing rule covers.
type Audience struct {
	Scope gcal.AclScope
	// Source says how the scope type was arrived at. A server that
	// infers has to say it inferred: "user" and "group" are the same
	// shape — an address — and picking one silently is the kind of guess
	// that reads as a fact in a result.
	Source AudienceSource
}

// AudienceSource is where the scope type came from.
type AudienceSource string

// The four sources, in decreasing order of certainty.
const (
	// SourceGiven: the caller named the type.
	SourceGiven AudienceSource = "given on the call"
	// SourceExisting: this calendar already has a rule for that address,
	// and its type is reused. The strongest inference there is, because
	// it is Google's own answer about the same audience.
	SourceExisting AudienceSource = "taken from the rule this calendar already has"
	// SourceAddress: it has an @, so it was read as one person.
	SourceAddress AudienceSource = "inferred from the address"
	// SourcePublic: the caller asked for anyone.
	SourcePublic AudienceSource = "the public scope"
)

// ParseAudience turns the caller's `who` and optional `scope_type` into
// an ACL scope.
//
// existing is the calendar's current rules, which are read before every
// share anyway (§7.6 shows exposure before and after). They are used
// here rather than only reported: if this calendar already has a rule
// for that address, its scope type is Google's own answer to the
// question this function would otherwise guess at.
func ParseAudience(who, scopeType string, existing []model.Sharing) (Audience, error) {
	who = strings.TrimSpace(who)
	scopeType = strings.TrimSpace(strings.ToLower(scopeType))
	if strings.Contains(who, "@") {
		// A mailbox with a display name shares with its address, and the
		// rule, the match against existing rules and the result all carry
		// the bare one.
		bare, err := Address(who, "somebody to share with")
		if err != nil {
			return Audience{}, err
		}
		who = bare
	}

	if scopeType != "" {
		return audienceFromType(who, scopeType)
	}
	if isPublicWord(who) {
		return Audience{Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}, Source: SourcePublic}, nil
	}
	if who == "" {
		return Audience{}, fmt.Errorf("%w: who is required: an email address, a group address, a domain, "+
			"or \"anyone\" for the public internet", ErrInvalid)
	}
	for _, r := range existing {
		if r.Value != "" && strings.EqualFold(r.Value, who) {
			return Audience{
				Scope:  gcal.AclScope{Type: r.ScopeType, Value: r.Value},
				Source: SourceExisting,
			}, nil
		}
	}
	switch {
	case strings.Contains(who, "@"):
		if err := validAddress(who, "somebody to share with"); err != nil {
			return Audience{}, err
		}
		// A group address looks exactly like a person's, and Google's
		// scope types tell them apart. Read as a person, said out loud
		// in the result, and overridable with scope_type:group.
		return Audience{
			Scope:  gcal.AclScope{Type: gcal.ScopeTypeUser, Value: who},
			Source: SourceAddress,
		}, nil
	case looksLikeDomain(who):
		return Audience{}, fmt.Errorf("%w: %q has no @, so it reads as the whole %s domain — everybody in "+
			"it. That is too large a difference to infer: pass scope_type:domain if you mean it, or the "+
			"person's full address if you do not", ErrInvalid, who, who)
	default:
		return Audience{}, fmt.Errorf("%w: %q is neither an email address nor a domain. Pass an address, "+
			"a domain with scope_type:domain, or \"anyone\" for the public internet", ErrInvalid, who)
	}
}

func audienceFromType(who, scopeType string) (Audience, error) {
	switch scopeType {
	case gcal.ScopeTypeDefault, "public", "anyone":
		if who != "" && !isPublicWord(who) {
			return Audience{}, fmt.Errorf("%w: scope_type:default is the public scope and covers everybody, "+
				"so it takes no address — %q would be ignored. Drop who, or pass scope_type:user with it",
				ErrInvalid, who)
		}
		return Audience{Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}, Source: SourceGiven}, nil
	case gcal.ScopeTypeUser, gcal.ScopeTypeGroup:
		if err := validAddress(who, "somebody to share with"); err != nil {
			return Audience{}, err
		}
		return Audience{Scope: gcal.AclScope{Type: scopeType, Value: who}, Source: SourceGiven}, nil
	case gcal.ScopeTypeDomain:
		if !looksLikeDomain(who) {
			return Audience{}, fmt.Errorf("%w: %q is not a domain. A domain rule takes a name like "+
				"example.com, not an address", ErrInvalid, who)
		}
		return Audience{Scope: gcal.AclScope{Type: gcal.ScopeTypeDomain, Value: who}, Source: SourceGiven}, nil
	default:
		return Audience{}, fmt.Errorf("%w: %q is not a scope type. Pass:%s", ErrInvalid, scopeType, ScopeChoices())
	}
}

// ScopeChoices is the scope vocabulary and what each one covers.
func ScopeChoices() string {
	var b strings.Builder
	for _, t := range gcal.ScopeTypes {
		fmt.Fprintf(&b, "\n  %s — %s", t, gcal.ScopeMeans(t))
	}
	return b.String()
}

func isPublicWord(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "anyone", "public", "everyone", "default":
		return true
	default:
		return false
	}
}

// looksLikeDomain is deliberately loose: it separates "a word that could
// be a domain" from "a word that cannot be", and Google decides the
// rest. A stricter pattern here would refuse a real domain this server
// had never heard of.
func looksLikeDomain(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.ContainsAny(v, "@ \t") || !strings.Contains(v, ".") {
		return false
	}
	return !strings.HasPrefix(v, ".") && !strings.HasSuffix(v, ".")
}

// The three sharing tools split one act by how far it reaches (§7.6).
// share_calendar takes named people and groups inside the organization,
// below owner; publish_calendar the public and whole domains; and
// widen_calendar_access people and groups outside the organization, and
// owners. A client remembers an approval per tool, never per argument, so
// each wider form is a tool of its own, registered only with the
// destructive flag, rather than a value an always-allowed share carries
// unseen.

// gatedTool names a wider tool and the flag it needs, which is the
// operator's to set, never the caller's.
func gatedTool(name string) string {
	return name + ", which is registered only when the server runs with " + config.EnvPrefix + "ENABLE_DESTRUCTIVE=true; that " +
		"is for the person running it to decide"
}

// consumerDomains are the domains of personal Google accounts. They name
// no organization.
var consumerDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// GroupReach is what a group rule reaches, said on every group share and
// in share_calendar's description. Google's own help says access follows
// membership, later members included, and that a member outside the
// organization gets in unless its sharing policy forbids it (§7.6).
const GroupReach = "A group rule reaches everyone in the group, including people added later; a member " +
	"outside the organization too, if the organization's sharing policy lets them in."

// Outside reports whether a person's or a group's address is outside the
// organization of account, the signed-in account's own address. The
// organization is taken as the account's domain. A personal account has
// none, so every address is outside it, and so is every address when
// the account is unknown. A secondary domain of the same organization
// counts as outside too, and so does a domain with a character past
// ASCII, which Unicode case folding can make equal to the account's
// while Google is sent it unchanged: each errs toward one refusal too
// many, never one share too many.
func Outside(address, account string) bool {
	theirs, mine := domainOf(address), domainOf(account)
	return theirs == "" || mine == "" || consumerDomains[mine] || theirs != mine ||
		!ascii(address[strings.LastIndex(address, "@")+1:])
}

// ascii reports whether s holds only ASCII characters.
func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// OrdinaryAudience refuses, in share_calendar, an audience past named
// people and groups inside the organization, naming the tool that takes
// it. outside is Outside for the audience's address.
func OrdinaryAudience(a Audience, outside bool) error {
	switch {
	case a.Scope.IsPublic():
		return fmt.Errorf("%w: share_calendar shares with named people and groups inside your organization. "+
			"Sharing with anyone publishes the calendar to the whole internet, signed in or not, and "+
			"removing the rule later takes nothing back from whoever already looked. That is %s. To take a "+
			"public rule away, use unshare_calendar", ErrBlocked, gatedTool("publish_calendar"))
	case a.Scope.Type == gcal.ScopeTypeDomain:
		return fmt.Errorf("%w: share_calendar shares with named people and groups inside your organization. "+
			"A domain rule opens the calendar to everybody in %s, which is %s. To take a domain rule away, "+
			"use unshare_calendar", ErrBlocked, a.Scope.Value, gatedTool("publish_calendar"))
	case outside:
		return fmt.Errorf("%w: %s is outside your organization as far as this server can tell: an address "+
			"is inside only when its domain is your account's own, and a personal Google account has no "+
			"organization. share_calendar shares inside it; sharing outside is %s", ErrBlocked,
			Describe(a), gatedTool("widen_calendar_access"))
	}
	return nil
}

// OrdinaryRole refuses, in share_calendar, the owner role, naming the
// tool that grants it.
func OrdinaryRole(role string) error {
	if role != gcal.RoleOwner {
		return nil
	}
	return fmt.Errorf("%w: owner access includes changing who else can see the calendar, and removing you. "+
		"share_calendar grants access below owner; making somebody an owner is %s", ErrBlocked,
		gatedTool("widen_calendar_access"))
}

// WithinRule reports whether a share keeps or lowers the role of a rule
// the calendar already has. That takes nothing past where the rule
// already reaches, so share_calendar makes it whatever the rule's reach:
// it needs neither the destructive flag nor confirm.
func WithinRule(existing []model.Sharing, a Audience, role string) bool {
	rule, had := FindRule(existing, a)
	return had && gcal.AtLeast(rule.Role, role)
}

// PublicRole refuses owner on a public or a domain rule. Everybody in a
// domain, or the internet, able to change who else can see the calendar
// and to remove its owner is not a share this server makes; an owner is a
// named person, through widen_calendar_access.
func PublicRole(role string) error {
	if role != gcal.RoleOwner {
		return nil
	}
	return fmt.Errorf("%w: owner access lets whoever holds it change who else can see the calendar and remove "+
		"you, and publish_calendar does not give it to a domain or to anyone. Pass freeBusyReader, reader, "+
		"writerWithoutPrivateAccess or writer; an owner is one named person, through widen_calendar_access",
		ErrInvalid)
}

// ParsePublic reads publish_calendar's `who`: "anyone" for the public,
// or a domain for everybody in it. A domain needs no scope_type here,
// because nothing else this tool takes looks like one.
func ParsePublic(who string) (Audience, error) {
	who = strings.TrimSpace(who)
	switch {
	case isPublicWord(who):
		return Audience{Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}, Source: SourcePublic}, nil
	case looksLikeDomain(who):
		return Audience{Scope: gcal.AclScope{Type: gcal.ScopeTypeDomain, Value: strings.ToLower(who)},
			Source: SourceGiven}, nil
	case who == "":
		return Audience{}, fmt.Errorf("%w: who is required: \"anyone\" for the public internet, or a domain "+
			"such as example.com for everybody in it", ErrInvalid)
	}
	return Audience{}, fmt.Errorf("%w: publish_calendar takes \"anyone\" or a domain, such as example.com. "+
		"A person or a group is share_calendar inside your organization below owner, and "+
		"widen_calendar_access otherwise", ErrInvalid)
}

// WideAudience refuses, in widen_calendar_access, what one of the other
// two tools takes: the public or a domain, and a person or group inside
// the organization below owner. outside is Outside for the audience's
// address.
func WideAudience(a Audience, role string, outside bool) error {
	switch {
	case a.Scope.IsPublic() || a.Scope.Type == gcal.ScopeTypeDomain:
		return fmt.Errorf("%w: widen_calendar_access shares with a person or a group. The public and a whole "+
			"domain are publish_calendar's", ErrInvalid)
	case !outside && role != gcal.RoleOwner:
		return fmt.Errorf("%w: %s is inside your organization and %s is below owner, which is "+
			"share_calendar's to share. widen_calendar_access is for somebody outside your organization, or "+
			"for an owner", ErrInvalid, Describe(a), role)
	}
	return nil
}

// ParseRole reads the access level a share grants.
//
// Google's own spellings are accepted alongside plainer ones, as
// ParseNotify accepts `externalOnly`: a model that has read the API
// reference types what it saw there.
func ParseRole(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(v, "_", ""))) {
	case "freebusyreader", "freebusy", "busy":
		return gcal.RoleFreeBusyReader, nil
	case "reader", "read":
		return gcal.RoleReader, nil
	case "writerwithoutprivateaccess", "writerwithoutprivatedata", "writerlimited":
		return gcal.RoleWriterWithoutPrivateData, nil
	case "writer", "write":
		return gcal.RoleWriter, nil
	case "owner":
		return gcal.RoleOwner, nil
	case "none":
		// A rule with role "none" is how acl.list REPORTS a deleted rule,
		// not a way to take access away. Writing one would leave a rule
		// behind that grants nothing and reads as if it did something.
		return "", fmt.Errorf("%w: role:none does not remove access — Google uses it to report a rule that "+
			"has already been deleted. Use unshare_calendar to take access away", ErrInvalid)
	case "":
		return "", fmt.Errorf("%w: role is required: what this person may do. Pass:%s", ErrInvalid, RoleChoices())
	default:
		return "", fmt.Errorf("%w: %q is not an access role. Pass:%s", ErrInvalid, v, RoleChoices())
	}
}

// RoleChoices is the role vocabulary, explained rather than echoed
// (§7.6): writerWithoutPrivateAccess is not self-explanatory.
func RoleChoices() string {
	var b strings.Builder
	for _, r := range gcal.Roles {
		fmt.Fprintf(&b, "\n  %s — %s", r, gcal.RoleMeans(r))
	}
	return b.String()
}

// ShareNotify applies §4.3 to a sharing change.
//
// Two differences from a write to an event, both of them Google's:
//
//  1. the default is TRUE here and false there (§2.5), which is why this
//     server asks rather than inheriting either;
//  2. sendNotifications is a boolean, so `external_only` has nothing to
//     map to. It is refused as unsupported rather than quietly rounded
//     to one of the two, because rounding it would email either more
//     people or fewer than the caller asked for.
//
// There is no "reaches nobody" exemption of the kind §4.3.2 gives an
// event write, and that is the honest position rather than an oversight:
// a group address expands to people this server cannot count, a domain
// rule covers everybody in it, and the public scope covers the internet.
// The reach of a sharing change is not knowable here, so the choice is
// always the caller's.
func ShareNotify(v string) (bool, error) {
	if strings.TrimSpace(v) == "" {
		// Its own refusal rather than ParseNotify's, which lists all
		// three choices — including the one the next branch refuses as
		// unsupported. A caller that followed the list would be refused
		// twice for doing what it said.
		return false, fmt.Errorf("%w: notify is required on a sharing change, and there is no default — "+
			"Google's own is to EMAIL, the opposite of its default on an event. Pass:\n  all — emails the "+
			"people this rule names\n  none — asks Google to email nobody, which is not a promise of "+
			"silence and does not stop the access", ErrInvalid)
	}
	choice, err := ParseNotify(v)
	if err != nil {
		return false, err
	}
	switch choice {
	case NotifyNone:
		return false, nil
	case NotifyAll:
		return true, nil
	default:
		return false, fmt.Errorf("%w: notify:external_only has no meaning on a sharing change. Google's "+
			"sharing notification is a single switch — everybody the rule names, or nobody — with no "+
			"external/internal split. Pass notify:all or notify:none", ErrUnsupported)
	}
}

// ShareOutcome is what a sharing call did, which decides what its
// notification sentence can honestly claim.
type ShareOutcome int

// The three outcomes.
const (
	// ShareGranted: a rule was written, new or changed.
	ShareGranted ShareOutcome = iota
	// ShareUnchanged: the access was already in place, so nothing was
	// sent because nothing was written.
	ShareUnchanged
	// ShareRemoved: access was taken away, which Google never notifies
	// anybody about — acl.delete publishes no parameter for it.
	ShareRemoved
)

// ShareReport is what a sharing result says about notification (§4.9).
//
// As on an event, it says what was ASKED FOR. The API reports nothing
// about delivery.
//
// All four sentences a sharing call can produce come from here, outcome
// included. The service wrote two of them by hand for a while, and both
// made the flat promise "Nobody was emailed" — which is the exact claim
// this function exists to avoid making (§2.6).
func ShareReport(outcome ShareOutcome, notify bool, a Audience) string {
	switch outcome {
	case ShareUnchanged:
		return "Nothing was sent, because nothing was written: " + Describe(a) +
			" already had this access."
	case ShareRemoved:
		return "Nobody was asked to be emailed, because Google publishes no way to ask: there are no " +
			"notifications on access removal. They are not told — they find the calendar gone."
	}
	return grantReport(notify, a)
}

func grantReport(notify bool, a Audience) string {
	who := "the people this rule names"
	switch a.Scope.Type {
	case gcal.ScopeTypeUser:
		who = "the person this rule names"
	case gcal.ScopeTypeGroup:
		who = "the group this rule names"
	case gcal.ScopeTypeDomain:
		who = "the domain this rule names"
	case gcal.ScopeTypeDefault:
		// There is no address in a public rule, so there is nobody for
		// Google to write to. The choice still has to be made and sent,
		// because leaving it out means Google's default, which is to
		// notify (§2.5).
		if notify {
			return "Asked Google to send a sharing notification, though a public rule names nobody to send " +
				"it to. That is what was asked for, not what arrived."
		}
		return "Asked Google to send no sharing notification. A public rule names nobody to notify in any case."
	}
	if notify {
		return "Asked Google to email " + who + " about the change. That is what was asked for, not what " +
			"arrived: the API reports nothing about delivery."
	}
	return "Asked Google to email nobody about the change. Google says some mail may still be sent, so this " +
		"is not a promise of silence — and " + who + " gets the access either way."
}

// FindRule returns the calendar's existing rule for an audience.
func FindRule(rules []model.Sharing, a Audience) (model.Sharing, bool) {
	for _, r := range rules {
		if gcal.SameScope(gcal.AclScope{Type: r.ScopeType, Value: r.Value}, a.Scope) {
			return r, true
		}
	}
	return model.Sharing{}, false
}

// Describe names an audience in the words a result uses, without
// printing an address that a log or a transcript must not carry: the
// caller supplied it, so the result may echo it, but §9 keeps it out of
// everything else.
//
// model.Sharing.Who() owns the wording, because the same audience is
// named by the exposure list in the same result. Two spellings meant one
// share could read "Shared X with ANYONE, signed in or not" above a note
// about "anyone at all, signed in or not".
func Describe(a Audience) string {
	return model.Sharing{ScopeType: a.Scope.Type, Value: a.Scope.Value}.Who()
}
