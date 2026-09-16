package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
	"github.com/mmedum/google-calendar-mcp/internal/render"
)

// The sharing tools (§7.6): list_sharing, share_calendar,
// unshare_calendar.
//
// Every one of them reads the rules first, and the read is not
// incidental: §7.6 requires a share to show who could see the calendar
// before and who can see it now, and "shared with somebody" is not an
// answer to "who can see this". The same read decides the scope type of
// an address this calendar already knows, so the server infers less.

// sharingRules reads the ACL and turns it into the server's own type.
//
// A missing scope comes back as a named error rather than an empty list,
// because §2.15 makes the ACL read a separate grant and an empty list
// reads as "shared with nobody" — the wrong answer rather than a missing
// one.
func (s *Service) sharingRules(ctx context.Context, calendarID string) ([]model.Sharing, error) {
	var out []model.Sharing
	token := ""
	for {
		page, err := s.API.ListACL(ctx, calendarID, token)
		if err != nil {
			return nil, aclError(err)
		}
		for _, r := range page.Items {
			// Google reports a deleted rule as role "none" when asked to
			// show deleted ones. This server never asks, so one arriving
			// here would be a rule that grants nothing, and listing it
			// as exposure would be wrong.
			if r.Role == gcal.RoleNone {
				continue
			}
			out = append(out, model.FromACL(r))
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}

// MissingACLScope is what a caller is told when Google refuses the ACL
// read, wherever they meet it.
//
// One sentence, exported because get_calendar reports it as a note on an
// otherwise successful read while list_sharing raises it as the failure.
// Two wordings had already drifted: the shorter one dropped the fact
// that CHANGING a rule needs a second scope again.
const MissingACLScope = "could not read who this calendar is shared with. Reading the sharing rules needs " +
	"the calendar.acls.readonly scope, which calendar.readonly does NOT cover, and changing them needs " +
	"calendar.acls — run `google-calendar-mcp login` again to grant them. This is not the same as the " +
	"calendar being shared with nobody"

// aclError names the scope when Google refuses the ACL read.
func aclError(err error) error {
	cls, ok := gapi.ClassOf(err)
	if !ok || (cls != gapi.ClassAuth && cls != gapi.ClassForbidden) {
		return err
	}
	return gapi.Wrap(cls, err, "%s", MissingACLScope)
}

// missingACLScope reports whether an error is that refusal, so a caller
// that carries on without the sharing list can tell it from a real
// failure.
func missingACLScope(err error) bool {
	cls, ok := gapi.ClassOf(err)
	return ok && (cls == gapi.ClassAuth || cls == gapi.ClassForbidden)
}

// ListSharing is list_sharing.
func (s *Service) ListSharing(ctx context.Context, ref string) (render.SharingReport, error) {
	ctx, cal, err := s.openCalendar(ctx, ref)
	if err != nil {
		return render.SharingReport{}, err
	}
	rules, err := s.sharingRules(ctx, cal.ID)
	if err != nil {
		return render.SharingReport{}, err
	}
	report := render.SharingReport{
		CalendarID: cal.ID, Title: cal.Title, After: rules, Requests: gapi.Requests(ctx),
	}
	if pub, ok := model.PublicRule(rules); ok {
		report.Notes = append(report.Notes,
			"This calendar is PUBLIC: anybody at all can "+strings.TrimPrefix(pub.RoleMeans(), "can ")+
				". unshare_calendar with who:anyone removes that rule — which stops new readers and takes "+
				"nothing back from whoever has already looked.")
	}
	return report, nil
}

// ShareOptions is what share_calendar takes.
type ShareOptions struct {
	Calendar string
	Who      string
	// ScopeType is user, group, domain or default. Optional: the server
	// works it out from the address and says how it did.
	ScopeType   string
	Role        string
	Notify      string
	AllowPublic bool
	DryRun      bool
}

// ShareCalendar grants access to a calendar (§7.6).
func (s *Service) ShareCalendar(ctx context.Context, o ShareOptions) (render.SharingReport, error) {
	ctx, cal, err := s.openCalendar(ctx, o.Calendar)
	if err != nil {
		return render.SharingReport{}, err
	}
	// Changing who can see a calendar is the owner's to do, and Google
	// refuses anybody else. Refused here when the role is known, so the
	// caller gets the sentence rather than a 403.
	if err := needRole(cal, gcal.RoleOwner, "changing who a calendar is shared with"); err != nil {
		return render.SharingReport{}, err
	}

	before, err := s.sharingRules(ctx, cal.ID)
	if err != nil {
		return render.SharingReport{}, err
	}
	audience, err := plan.ParseAudience(o.Who, o.ScopeType, before)
	if err != nil {
		return render.SharingReport{}, classifyPlan(err)
	}
	if err := plan.AllowPublic(audience, o.AllowPublic); err != nil {
		return render.SharingReport{}, classifyPlan(err)
	}
	role, err := plan.ParseRole(o.Role)
	if err != nil {
		return render.SharingReport{}, classifyPlan(err)
	}
	notify, err := plan.ShareNotify(o.Notify)
	if err != nil {
		return render.SharingReport{}, classifyPlan(err)
	}

	report := render.SharingReport{
		Verb: render.VerbShare, DryRun: o.DryRun,
		CalendarID: cal.ID, Title: cal.Title, Before: before,
		Notify: plan.ShareReport(plan.ShareGranted, notify, audience),
	}
	if audience.Source != plan.SourceGiven {
		report.Notes = append(report.Notes, "Read as "+gcal.ScopeMeans(audience.Scope.Type)+
			" — the scope type was "+string(audience.Source)+
			". Pass scope_type if that is not what you meant; a group address and a person's look the same.")
	}
	if audience.Scope.IsPublic() {
		report.Notes = append(report.Notes,
			"This calendar is now PUBLIC. Removing the rule later stops new readers and takes nothing back "+
				"from whoever already looked, so treat this as published rather than shared.")
	}
	if role == gcal.RoleOwner {
		report.Notes = append(report.Notes,
			"Owner access includes changing who else can see this calendar, and removing you.")
	}

	existing, had := plan.FindRule(before, audience)
	switch {
	case had && existing.Role == role:
		// Nothing to write, and saying so matters: an insert here would
		// be a second email about access somebody already has.
		report.After = before
		changed := existing
		report.Changed = &changed
		report.Notify = plan.ShareReport(plan.ShareUnchanged, notify, audience)
		report.Notes = append(report.Notes,
			plan.Describe(audience)+" already had "+role+" access, so no rule was written.")
		report.Requests = gapi.Requests(ctx)
		return report, nil
	case had:
		report.Notes = append(report.Notes,
			"Their access changed from "+existing.Role+" to "+role+".")
	}

	if o.DryRun {
		// Changed is set here as well as on the real path, and that is
		// not decoration: the result renders exposure before and after
		// only when it knows which rule the write was about, so a dry run
		// without it printed a different SHAPE from the call it is
		// supposed to be showing. Phase 2 shipped that exact defect on a
		// cancellation and the transcript caught it.
		would := model.Sharing{
			RuleID: existing.RuleID, ScopeType: audience.Scope.Type,
			Value: audience.Scope.Value, Role: role,
		}
		report.Changed = &would
		report.After = withRule(before, would)
		report.Requests = gapi.Requests(ctx)
		return report, nil
	}

	var written *gcal.AclRule
	if had {
		written, err = s.API.PatchACL(ctx, cal.ID, existing.RuleID, &gcal.AclPatch{Role: &role},
			notify, existing.ETag)
	} else {
		written, err = s.API.InsertACL(ctx, cal.ID, &gcal.AclRule{
			Role: role, Scope: audience.Scope,
		}, notify)
	}
	if err != nil {
		return render.SharingReport{}, err
	}
	changed := model.FromACL(*written)
	report.Changed = &changed
	report.After = withRule(before, changed)
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// UnshareOptions is what unshare_calendar takes.
type UnshareOptions struct {
	Calendar  string
	Who       string
	ScopeType string
	DryRun    bool
}

// UnshareCalendar takes access away (§7.6).
//
// It has no notify parameter, and that is Google's shape rather than an
// omission: acl.delete publishes no sendNotifications, and acl.patch's
// own description says there are no notifications on access removal.
// Nobody is told; they find it gone. The result says that rather than
// leaving a caller to assume a mail went out.
func (s *Service) UnshareCalendar(ctx context.Context, o UnshareOptions) (render.SharingReport, error) {
	ctx, cal, err := s.openCalendar(ctx, o.Calendar)
	if err != nil {
		return render.SharingReport{}, err
	}
	if err := needRole(cal, gcal.RoleOwner, "changing who a calendar is shared with"); err != nil {
		return render.SharingReport{}, err
	}
	before, err := s.sharingRules(ctx, cal.ID)
	if err != nil {
		return render.SharingReport{}, err
	}
	audience, err := plan.ParseAudience(o.Who, o.ScopeType, before)
	if err != nil {
		return render.SharingReport{}, classifyPlan(err)
	}
	rule, had := plan.FindRule(before, audience)
	if !had {
		return render.SharingReport{}, gapi.Errf(gapi.ClassNotFound,
			"%q is not shared with %s, so there is no rule to remove. list_sharing shows the %d it does "+
				"have", cal.Title, plan.Describe(audience), len(before))
	}
	// Removing your own rule from your own calendar is a door locked
	// from the inside. Google may or may not refuse it; this server does,
	// because the caller cannot undo it afterwards — the tool that would
	// put the rule back needs the access the rule grants.
	//
	// Asked only of a rule that names one person. A domain or public rule
	// cannot be this account, and primaryID reads the whole calendar
	// list — so checking those spent a request on a comparison whose
	// other side is always empty.
	if rule.ScopeType == gcal.ScopeTypeUser && rule.Value != "" {
		if self := s.primaryID(ctx); self != "" && strings.EqualFold(rule.Value, self) {
			return render.SharingReport{}, gapi.Errf(gapi.ClassBlocked,
				"that rule is this account's own access to %q. Removing it would lock you out of the "+
					"calendar, and nothing here could put it back", cal.Title)
		}
	}

	report := render.SharingReport{
		Verb: render.VerbUnshare, DryRun: o.DryRun,
		CalendarID: cal.ID, Title: cal.Title, Before: before,
	}
	changed := rule
	report.Changed = &changed
	report.Notify = plan.ShareReport(plan.ShareRemoved, false, audience)
	if rule.Public() {
		report.Notes = append(report.Notes,
			"The calendar is no longer public. That stops new readers and takes nothing back from anybody "+
				"who already looked while it was.")
	}
	report.After = withoutRule(before, rule.RuleID)

	if !o.DryRun {
		if err := s.API.DeleteACL(ctx, cal.ID, rule.RuleID, rule.ETag); err != nil {
			return render.SharingReport{}, err
		}
	}
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// withRule is the exposure after a share: the rules as they were, with
// this one added or replaced.
//
// Computed rather than re-read, and the saving is the point: a second
// acl.list would cost a request to learn something this server just
// wrote. The rule that came back from Google is the one folded in, so
// the "after" is what Google stored rather than what was asked for.
func withRule(before []model.Sharing, rule model.Sharing) []model.Sharing {
	out := make([]model.Sharing, 0, len(before)+1)
	replaced := false
	for _, r := range before {
		if sameAudience(r, rule) {
			out = append(out, rule)
			replaced = true
			continue
		}
		out = append(out, r)
	}
	if !replaced {
		out = append(out, rule)
	}
	return out
}

// withoutRule is the exposure after an unshare.
func withoutRule(before []model.Sharing, ruleID string) []model.Sharing {
	out := make([]model.Sharing, 0, len(before))
	for _, r := range before {
		if r.RuleID == ruleID {
			continue
		}
		out = append(out, r)
	}
	return out
}

func sameAudience(a, b model.Sharing) bool {
	return gcal.SameScope(a.Scope(), b.Scope())
}
