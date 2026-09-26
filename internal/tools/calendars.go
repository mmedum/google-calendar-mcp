package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/service"
)

// The sentences every calendar and sharing tool has to say, written once
// here because they are the distinctions a model gets wrong: which of
// the two resources a change lands on, and who finds out about it.

const twoThingsHelp = "A calendar is two things and the difference is visible to other people: the CALENDAR " +
	"itself — its title, description, location and time zone — is what everybody it is shared with sees, and " +
	"YOUR SUBSCRIPTION to it — the color, the name you give it, whether it is hidden, what you are emailed " +
	"about — is yours alone. Renaming the calendar renames it for the team; `my_name` renames it only for you."

const shareNotifyHelp = "`notify` is REQUIRED and has no default: `all` asks Google to email about the " +
	"change, `none` asks it not to. Google's own default here is to EMAIL — the opposite of its default on an " +
	"event — which is why this server asks rather than inheriting either. `external_only` has no meaning on a " +
	"sharing change and is refused: Google's switch is on or off, with no internal/external split. Whatever " +
	"you pass, the result says what was ASKED FOR; the API reports nothing about what arrived."

const exposureHelp = "The result lists who could see the calendar before and who can see it now, because " +
	"\"shared with somebody\" is not an answer to \"who can see this\"."

func registerCalendars(s *mcp.Server, d Deps) {
	add(s, d, Def[createCalendarIn, service.CalendarWriteResult]{
		Name: "create_calendar",
		Description: "Create a new calendar of your own. " +
			"You are its owner, it appears in your calendar list, and nobody else can see it until you " +
			"share it — use share_calendar for that. " +
			"It is created in `time_zone` if you pass one, otherwise the zone in this account's Calendar " +
			"settings; the result says which it used, and every time written to the calendar afterward is " +
			"read against it. " +
			"Creating a calendar is quota-counted by Google and deleting one does not give the quota back, " +
			"so do not create one per task. " + dryRunHelp,
		Kind: Write,
		Handle: func(ctx context.Context, in createCalendarIn) (service.CalendarWriteResult, error) {
			out, err := d.Service.CreateCalendar(ctx, service.CreateCalendarOptions{
				Title: in.Title, Description: in.Description, Location: in.Location,
				TimeZone: in.TimeZone, DryRun: in.DryRun,
			})
			if err != nil {
				return service.CalendarWriteResult{}, err
			}
			return service.NewCalendarWriteResult(out), nil
		},
	})

	add(s, d, Def[manageCalendarIn, service.CalendarWriteResult]{
		Name: "manage_calendar",
		Description: "Change a calendar, or change how you see it, or add and remove it from your list. " +
			twoThingsHelp + " " +
			"`subscribe` adds an existing calendar to your list by id; `unsubscribe` removes it from YOUR " +
			"list and does not delete it, does not touch its events and changes nothing for anybody else. " +
			"Use delete_calendar to remove a calendar itself. " +
			"`notifications` replaces the whole list of what you are emailed about on this calendar; an " +
			"empty list turns them all off. Email is the only delivery Google offers. " +
			"Only the fields you pass are touched. There is no `etag` here on purpose: the two halves are " +
			"two resources with two etags, so each patch is made under the etag of the read this call just " +
			"did, and the result shows before and after for every field it changed. " + dryRunHelp,
		Kind: IdempotentWrite,
		Handle: func(ctx context.Context, in manageCalendarIn) (service.CalendarWriteResult, error) {
			out, err := d.Service.ManageCalendar(ctx, service.ManageOptions{
				Calendar: in.Calendar,
				Title:    in.Title, Description: in.Description, Location: in.Location, TimeZone: in.TimeZone,
				Subscribe: in.Subscribe, Unsubscribe: in.Unsubscribe,
				MyName: in.MyName, ColorID: in.ColorID, Hidden: in.Hidden, Selected: in.Selected,
				Notifications: in.Notifications, DryRun: in.DryRun,
			})
			if err != nil {
				return service.CalendarWriteResult{}, err
			}
			return service.NewCalendarWriteResult(out), nil
		},
	})

	add(s, d, Def[listSharingIn, service.SharingResult]{
		Name: "list_sharing",
		Description: "Who can see a calendar, and what each of them can see. " +
			"Roles are explained rather than echoed, because `writerWithoutPrivateAccess` and " +
			"`freeBusyReader` do not say what they mean. A rule reading ANYONE means the calendar is public " +
			"to the whole internet. " +
			"Reading this needs the calendar.acls.readonly scope, which calendar.readonly does not cover: if " +
			"it was not granted the call says so rather than reporting an empty list, because \"could not " +
			"read\" and \"shared with nobody\" are different answers.",
		Kind: SharingRead,
		Handle: func(ctx context.Context, in listSharingIn) (service.SharingResult, error) {
			out, err := d.Service.ListSharing(ctx, in.Calendar)
			if err != nil {
				return service.SharingResult{}, err
			}
			return service.NewSharingResult(out), nil
		},
	})

	add(s, d, Def[shareCalendarIn, service.SharingResult]{
		Name: "share_calendar",
		Description: "Give somebody access to a calendar, or change the access they already have. " +
			"`who` is an email address, a group address, a domain, or \"anyone\" for the public internet. " +
			"An address is read as one person unless this calendar already has a rule for it — pass " +
			"`scope_type` to be sure, because a group address and a person's look identical. " +
			"`role` is what they may do: freeBusyReader, reader, writerWithoutPrivateAccess, writer or " +
			"owner. The result explains the one it set. " +
			"Sharing with \"anyone\" publishes the calendar to the whole internet and needs " +
			"`allow_public: true`: removing the rule later stops new readers and takes nothing back from " +
			"whoever already looked. " + shareNotifyHelp + " " + exposureHelp + " " + dryRunHelp,
		Kind: Sharing,
		Handle: func(ctx context.Context, in shareCalendarIn) (service.SharingResult, error) {
			out, err := d.Service.ShareCalendar(ctx, service.ShareOptions{
				Calendar: in.Calendar, Who: in.Who, ScopeType: in.ScopeType, Role: in.Role,
				Notify: in.Notify, AllowPublic: in.AllowPublic, DryRun: in.DryRun,
			})
			if err != nil {
				return service.SharingResult{}, err
			}
			return service.NewSharingResult(out), nil
		},
	})

	add(s, d, Def[unshareCalendarIn, service.SharingResult]{
		Name: "unshare_calendar",
		Description: "Take somebody's access to a calendar away. " +
			"`who` is the address, domain or \"anyone\" whose rule should go; the server finds the rule " +
			"itself, and says so if there is none. " +
			"There is no notify here, and that is Google's shape rather than an omission: nobody is emailed " +
			"when access is removed and there is no way to ask for it. They are not told — they find the " +
			"calendar gone. " +
			"Removing the public rule stops new readers and takes nothing back from anybody who already " +
			"looked. " + exposureHelp + " " + dryRunHelp,
		Kind: Sharing,
		Handle: func(ctx context.Context, in unshareCalendarIn) (service.SharingResult, error) {
			out, err := d.Service.UnshareCalendar(ctx, service.UnshareOptions{
				Calendar: in.Calendar, Who: in.Who, ScopeType: in.ScopeType, DryRun: in.DryRun,
			})
			if err != nil {
				return service.SharingResult{}, err
			}
			return service.NewSharingResult(out), nil
		},
	})

	add(s, d, Def[destructiveIn, service.CalendarWriteResult]{
		Name: "delete_calendar",
		Description: "Delete a calendar and every event on it, for everybody it is shared with. " +
			"This cannot be undone. It needs `confirm: true` on the call, and the server it runs in has to " +
			"have been started with the destructive tools enabled. " +
			"Only a calendar you own, and never your own primary calendar — Google has no way to delete " +
			"that one. To empty a calendar and keep it, use clear_calendar; to remove it from your list " +
			"without deleting it, use manage_calendar with unsubscribe. " + dryRunHelp,
		Kind: Destructive,
		Handle: func(ctx context.Context, in destructiveIn) (service.CalendarWriteResult, error) {
			out, err := d.Service.DeleteCalendar(ctx, service.DestructiveOptions{
				Calendar: in.Calendar, Confirm: in.Confirm, DryRun: in.DryRun,
			})
			if err != nil {
				return service.CalendarWriteResult{}, err
			}
			return service.NewCalendarWriteResult(out), nil
		},
	})

	add(s, d, Def[destructiveIn, service.CalendarWriteResult]{
		Name: "clear_calendar",
		Description: "Delete EVERY event on this account's own primary calendar: past, future, and every " +
			"meeting it organizes. This is the most destructive call the Calendar API offers and it cannot " +
			"be undone. Guests are not notified, and the meetings stay on their calendars. " +
			"It needs `confirm: true` on the call, and the server has to have been started with the " +
			"destructive tools enabled. " +
			"It works only on the primary calendar, which is what Google documents it for; another calendar " +
			"is refused, and delete_calendar removes one of those whole. " + dryRunHelp,
		Kind: Destructive,
		Handle: func(ctx context.Context, in destructiveIn) (service.CalendarWriteResult, error) {
			out, err := d.Service.ClearCalendar(ctx, service.DestructiveOptions{
				Calendar: in.Calendar, Confirm: in.Confirm, DryRun: in.DryRun,
			})
			if err != nil {
				return service.CalendarWriteResult{}, err
			}
			return service.NewCalendarWriteResult(out), nil
		},
	})
}

// Inputs. Flat, snake_case, and a pointer wherever "leave it alone" and
// "set it to empty" are different requests.

type createCalendarIn struct {
	Title       string `json:"title" jsonschema:"What the calendar is called, for everybody it is shared with. Required."`
	Description string `json:"description,omitempty" jsonschema:"What it is for."`
	Location    string `json:"location,omitempty" jsonschema:"Free text, such as a city or a building."`
	TimeZone    string `json:"time_zone,omitempty" jsonschema:"IANA zone the calendar keeps, such as Europe/Copenhagen. Defaults to this account's own."`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"Report what would be created, without writing."`
}

type manageCalendarIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar id or title. Ids come from list_calendars."`

	Title       *string `json:"title,omitempty" jsonschema:"New title for the CALENDAR ITSELF, which everybody it is shared with sees. Needs owner access."`
	Description *string `json:"description,omitempty" jsonschema:"New description. Pass an empty string to clear it."`
	Location    *string `json:"location,omitempty" jsonschema:"New location. Pass an empty string to clear it."`
	TimeZone    *string `json:"time_zone,omitempty" jsonschema:"New IANA time zone for the calendar, such as Europe/Copenhagen."`

	Subscribe   bool `json:"subscribe,omitempty" jsonschema:"Add this calendar to your list. It grants no access; it only puts a calendar you can already reach where you can see it."`
	Unsubscribe bool `json:"unsubscribe,omitempty" jsonschema:"Remove it from YOUR list. It is not deleted, its events stay, and nobody else notices."`

	MyName        *string   `json:"my_name,omitempty" jsonschema:"The name YOU see for this calendar. Nobody else sees it. Pass an empty string to go back to its own title."`
	ColorID       *string   `json:"color_id,omitempty" jsonschema:"Your color for it, from the calendar palette get_settings reports."`
	Hidden        *bool     `json:"hidden,omitempty" jsonschema:"Hide it from your calendar list."`
	Selected      *bool     `json:"selected,omitempty" jsonschema:"Whether its events are drawn in the Calendar UI. Not the same as hidden."`
	Notifications *[]string `json:"notifications,omitempty" jsonschema:"What YOU are emailed about on this calendar: creation, change, cancellation, response, agenda. Replaces the whole list; an empty list turns them off."`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"Report what would change, without writing."`
}

type listSharingIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar id, its title, or \"primary\" for this account's own calendar."`
}

type shareCalendarIn struct {
	Calendar    string `json:"calendar" jsonschema:"The calendar to share. You must own it."`
	Who         string `json:"who" jsonschema:"An email address, a group address, a domain, or \"anyone\" for the public internet. Required."`
	ScopeType   string `json:"scope_type,omitempty" jsonschema:"user, group, domain or default. Optional: an address is read as one person unless a rule for it already exists."`
	Role        string `json:"role" jsonschema:"What they may do: freeBusyReader, reader, writerWithoutPrivateAccess, writer or owner. Required."`
	Notify      string `json:"notify,omitempty" jsonschema:"Whether Google emails about the change: all or none. Required; there is no default, and Google's own is to email."`
	AllowPublic bool   `json:"allow_public,omitempty" jsonschema:"Required to share with \"anyone\", which publishes the calendar to the whole internet."`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"Report what access would change and who would be emailed, without writing."`
}

type unshareCalendarIn struct {
	Calendar  string `json:"calendar" jsonschema:"The calendar to stop sharing. You must own it."`
	Who       string `json:"who" jsonschema:"The address, domain, or \"anyone\", whose access should go. Required."`
	ScopeType string `json:"scope_type,omitempty" jsonschema:"user, group, domain or default, when the address alone is ambiguous."`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"Report whose access would go, without writing."`
}

type destructiveIn struct {
	Calendar string `json:"calendar" jsonschema:"The calendar id or title."`
	// Not required in the schema, deliberately. The SDK validates a
	// required field before the handler runs, so the caller would get
	// "missing properties: [confirm]" instead of the sentence that says
	// what would be destroyed — which is the whole point of asking.
	// `notify` is optional in the schema for the same reason.
	Confirm bool `json:"confirm,omitempty" jsonschema:"Must be true. Without it the call is refused, whatever the server is configured to allow."`
	DryRun  bool `json:"dry_run,omitempty" jsonschema:"Report what would be destroyed, without writing."`
}
