# google-calendar-mcp

[![CI](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/google-calendar-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mmedum/google-calendar-mcp?sort=semver)](https://github.com/mmedum/google-calendar-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-calendar-mcp/v5.svg)](https://pkg.go.dev/github.com/mmedum/google-calendar-mcp/v5)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-calendar-mcp)](./LICENSE)

An MCP server for Google Calendar. One binary, stdio, per-user OAuth
against your own Google account.

It answers **when** — calendars, the events on them, and who is free. What
a meeting produces (a recording, a notes document, an attachment) belongs
to servers built on the Meet, Docs and Drive APIs. `get_event` names the
files attached to an event, with the Drive file id those servers take.

It is for one person who uses Google Calendar, on a Workspace or a
personal Google account, and wants an assistant to read and change that
calendar as them. You create your own OAuth client in Google Cloud once,
and nothing is hosted.

## Goals and non-goals

Goals:

- **Correct about time.** An all-day event stays a date, every timed
  write carries an IANA zone, and every read states its window and zone.
- **Nobody emailed by surprise.** `notify` is required on any write that
  can reach a guest, and `scope` on any write to a repeating event, with
  no default for either.
- **A write never overwrites what it did not see.** Every change is a
  patch under the etag of the read that planned it.
- **Safe by default.** What cannot be taken back, and every share beyond
  your organization, is registered only when you turn it on. Read-only
  mode removes every write, and the writes take `dry_run`.
- **Act as you, and log nothing about your calendar.** Every call runs as
  the signed-in account, and the logs never carry event content or
  addresses.

Non-goals:

- **What a meeting produces.** Recordings, notes documents and the files
  attached to an event belong to the Drive and Docs servers. Google Tasks
  is a separate API.
- **Push notifications.** Google's watch methods need a public HTTPS
  endpoint, which a stdio server cannot offer. `list_changes` hands back
  a sync token instead.
- **A local cache or sync loop.** The server keeps no copy of your
  calendar between calls.
- **Migration and administration.** Importing events from another system
  and transferring a calendar's ownership as an administrator are out.
- **Parsing natural language into events.** Google's quick-add hides
  which fields it set and in which zone it read the time; the model
  calling this server parses better.
- **A hosted service.** It speaks stdio only, so Claude on the web and on
  mobile cannot reach it.
- **Asking you before a call.** Your client approves each call; the
  server asks nothing.

## What makes this one different

Calendar tooling often gets these wrong. This server is built around
getting them right, and a test enforces each:

- **An all-day event is a date, not a time.** It never becomes an
  instant, so it cannot land on the wrong day for anyone west of UTC.
  Every timed event carries a real IANA zone, not just an offset, because
  an offset cannot expand a repeating event across a daylight-saving
  change.
- **Every read says what it read.** The absolute window, the time zone,
  where that zone came from, and whether the list was truncated. A
  schedule that does not say which Thursday it is showing can be read as
  any Thursday.
- **Availability is not a list of events.** An event you cannot see the
  details of still makes someone busy, and an event marked "free" does
  not. A calendar that could not be read is reported as *unknown*, never
  folded into "free".

The design and the decisions behind it are in `docs/architecture.md`.

## Install

```
go install github.com/mmedum/google-calendar-mcp/v5/cmd/google-calendar-mcp@latest
```

Or take a signed archive from the
[latest release](https://github.com/mmedum/google-calendar-mcp/releases/latest).
`docs/release.md` gives the commands that verify it.

## Set up

You need your own Google Cloud project and OAuth client. Nothing
deployer-specific is baked into this repository.

1. Create a project and enable the **Google Calendar API**.
2. Configure the OAuth consent screen and, on an External app, add the
   scopes `docs/gcp-setup.md` lists; an Internal app needs no list. A gate
   checks that file against the code, so it cannot fall behind what
   `login` asks for.
3. Create an OAuth client of type **Desktop app** and download the JSON.
4. Sign in:

```
google-calendar-mcp login --client-secret ~/path/to/client_secret.json
google-calendar-mcp doctor
```

`doctor` checks the client JSON, the stored token, the granted scopes and
whether the API answers, and names what is missing.

### Signing in over SSH

The OAuth callback reaches the loopback interface of the machine running
`login`, while your browser is on your own machine. Forward the port
first. Use `--no-browser`: it prints the authorization URL and the exact
`ssh -L` line to run, with the port it actually picked.

## Configure your MCP client

```json
{
  "mcpServers": {
    "google-calendar": {
      "command": "google-calendar-mcp",
      "env": {
        "GCAL_CLIENT_SECRET": "/path/to/client_secret.json"
      }
    }
  }
}
```

Every setting is in `docs/configuration.md`.

### Claude Desktop

Every release also carries a `.mcpb` bundle. Open it and Claude Desktop
installs the server and asks for the settings a client's configuration
would carry: your OAuth client JSON, which may be left empty to use the
one `login` recorded, the profile, read-only and the switches. Deleting
a calendar or every event on one, publishing it and sharing it outside
your organization (`enable_destructive`), and an
event that declines every meeting it overlaps (`enable_decline_all`)
stay off unless you turn them on. Sharing inside your organization
(`sharing`) stays on, as on a server you configure by hand, and
read-only leaves every switch nothing to register. This server asks
nobody before a call. Claude Desktop approves each tool, with Allow once
or Always allow, and a tool you always allow then runs unasked, so leave
a switch off unless you need its tools.

The bundle covers macOS, Windows and Linux on both architectures: macOS
through a universal binary, Windows through amd64, and Linux through a
small launcher that picks the binary at start. It does not log you in.
Install the binary as well, run `google-calendar-mcp login` once, and
the bundle uses the same login.

## Tools

| Tool | What it does |
|---|---|
| `list_calendars` | Every calendar this account can see, with ids, time zones and your access level. Start here. |
| `get_calendar` | One calendar in full, including who it is shared with and its default reminders. |
| `list_events` | The events on one or more calendars in a window, or only the kinds you name, such as out of office, or the event an invitation email is about. |
| `search_events` | Free-text search across a window, over titles, descriptions, locations and guests. |
| `get_event` | One event, with its guests and their responses, what guests may do, your reminders, and the files attached to it. |
| `list_instances` | The occurrences of one repeating event, with the dates that were moved or removed. |
| `list_changes` | What changed since you last looked, including **deletions** — which a list cannot report, because a deleted event simply stops matching. Hands back a sync token to pass in next time, or reads what changed since a moment you name. |
| `check_availability` | When people are busy and when they are free, from Google's free/busy service rather than from a list of events. Takes an optional working-hours mask. |
| `get_settings` | The account's time zone, week start and color palette. |
| `create_event` | Create an event, one-off or repeating, with guests, optional guests, rooms, your reminders, its visibility, what guests may do, and a Google Meet link if you ask for one. Or a status event on your primary calendar: out of office, focus time or a working location. |
| `create_event_declining_all` | An out-of-office or focus-time event that declines every meeting it overlaps, the accepted ones too. Behind its own flag. |
| `update_event` | Change an event, including your reminders, its visibility and what guests may do, or add a Google Meet link. Only the fields you pass are touched. |
| `cancel_event` | Cancel an event, or one occurrence of a repeating one. |
| `move_event` | Move an event to another calendar, which changes who organizes it. |
| `respond_to_event` | Answer an invitation: accepted, declined or tentative. |
| `create_calendar` | Create a calendar of your own. |
| `manage_calendar` | Rename or re-zone a calendar, set your own color and name for it, or add and remove it from your list. |
| `list_sharing` | Who can see a calendar, and what each of them can see. |
| `share_calendar` | Give a person or a group inside your organization access below owner, or change the access they have. |
| `unshare_calendar` | Take somebody's access away. |
| `publish_calendar` | Publish a calendar to the internet, or open it to a whole domain. Gated. |
| `widen_calendar_access` | Share with somebody outside your organization, or make somebody an owner. Gated. |
| `delete_calendar` | Delete a calendar and every event on it. Gated. |
| `clear_calendar` | Delete every event on your primary calendar. Gated. |

`GCAL_READ_ONLY=true` registers only the reads, the rows above
`create_event`, and requests only read scopes, so the API itself refuses
a write.

`manage_calendar` covers what the API splits across two resources and
people do not: **the calendar** — its title, description, location and
time zone — is what everybody it is shared with sees, while **your
subscription** to it — the color, the name you give it, whether it is
hidden, what you are emailed about — is yours alone. Unsubscribing
removes it from your list; it deletes nothing and nobody else notices.

These rules run through every write:

- **`notify` is required** whenever the write can reach another person,
  and there is no default in either direction. `none` is refused outright
  when a guest is outside your organization — such a guest may have no
  Google Calendar for the event to appear in, so email is the only way
  they can learn of it.
- **`scope` is required** when the event repeats: `instance`, `series` or
  `this_and_following`. "Move the standup to 10:30" is three different
  operations and the API makes them look like one.
- **Every write is a patch under `If-Match`**, so it is refused as
  `[stale]` rather than overwriting somebody who changed it first. Adding
  a guest reads the list and adds to it; it never replaces it.

`dry_run: true` on any of them reports what would change and how many
guests would be emailed, without writing.

Above 200 guests Google stops propagating individual responses, so every
result about such an event says that the RSVPs it lists are incomplete.

## Resources

For clients that attach context rather than call tools, the same content
is published as resources:

| URI | What it carries |
|---|---|
| `gcal://calendars` | The calendar list, as `list_calendars` returns it. |
| `gcal://calendars/{calendar_id}` | One calendar, as `get_calendar` returns it. |
| `gcal://calendars/{calendar_id}/events/{event_id}` | One event, as `get_event` returns it. |

They carry no handles and take no arguments, which is why there is no
resource for a schedule: a window and a zone are not optional here, and a
URI with nowhere to state them would have to invent both.

## Working hours

`check_availability` answers over a window, and a window is one interval
— so "next week, 09:00 to 17:00" cannot be asked for as one, and the
longest gap in the answer is a fifteen-hour overnight one that passes any
`min_minutes`. `working_from`, `working_to` and `working_days` mask the
free gaps to a working week instead. There is no default: without them
every hour of the window counts, and the result always says which was
used. The mask is applied in the zone the answer is rendered in, day by
local day, so 09:00 is still 09:00 on the Sunday the clocks change.

## Meeting links

`create_event` takes `conference: true` and asks Google for a Google Meet
link. The link normally comes back with the event; Google documents the
conference as generated asynchronously, so the result may instead say it
is still being made, and then you read the event again for it. Either
way the result says which, and never reports a link it does not have.
`update_event` takes `add_conference` to add one to an event that has
none.

## Safety

- The tools that remove something Calendar cannot bring back,
  `delete_calendar` and `clear_calendar`, and those that widen who can
  see a calendar, `publish_calendar` and `widen_calendar_access`, are
  not registered at all unless `GCAL_ENABLE_DESTRUCTIVE=true`. Each still
  needs `confirm: true` on the call. The wider shares also take the
  calendar's title as `list_calendars` shows it, so the call names the
  calendar it opens. `cancel_event` is deliberately not behind
  that flag: canceling a meeting is what a calendar is for, Google keeps
  the record, and a gate everybody turns on protects nobody. What guards
  it instead is the required `scope` and the required `notify`.
- A cancellation with `notify: none` removes the meeting from your
  calendar and leaves it on your guests'. The result says so every time.
- Sharing is the one act here whose effect leaves your account, so every
  sharing tool requires `notify` like every other write — and here
  Google's own default is to email, the opposite of its default on an
  event. `share_calendar` takes named people and groups inside your
  organization, below owner: an address is inside when its domain is your
  account's own, and a personal Google account has no organization, so
  every address is outside it. Publishing to "anyone" or a whole domain is
  `publish_calendar`, and sharing outside or making an owner is
  `widen_calendar_access`; `share_calendar` still lowers the access of
  anybody the calendar is already shared with. Removing a public rule afterward stops new
  readers and takes nothing back from whoever already looked. Removing
  access notifies nobody, because Google offers no way to ask for it:
  they are not told, they find the calendar gone.
- `GCAL_SHARING=off` removes the sharing tools entirely, the wider ones
  included.
- An out-of-office or focus-time event that declines every meeting it
  overlaps, the ones you already accepted included, tells each organizer
  and is not taken back by deleting the event. `create_event` refuses
  `auto_decline: all`; `create_event_declining_all` makes it, and is
  registered only with `GCAL_ENABLE_DECLINE_ALL=true`, a flag of its own
  so that turning it on arms nothing else.
- Logs carry the method, tool, outcome and duration, and a failed call's
  error class. Never an email address, event title, description, location,
  search term or error message. A debug log is safe to paste into a bug report by
  construction.

Your MCP client approves each call; the server does not ask you itself.
claude.ai and Codex prompt for the tools marked destructive, which is
every write but `create_calendar` and `create_event`, and VS Code
prompts for every tool not marked read-only, which is every tool that
writes. In Claude Code, `delete_calendar`, `clear_calendar` and
`publish_calendar` ask on every call whatever your rules say. Deleting a
whole calendar's events cannot be undone, and removing a public rule
cannot take back what was already seen. Where Claude Code cannot ask, as
with `-p`, it refuses them. For the
rest, list the calls you want to approve one by one under
`permissions.ask` in your settings, with the name you gave the server in
place of `google-calendar`. For example, the writes that share, cancel,
move, decline or delete:

```json
{
  "permissions": {
    "ask": [
      "mcp__google-calendar__share_calendar", "mcp__google-calendar__unshare_calendar",
      "mcp__google-calendar__publish_calendar", "mcp__google-calendar__widen_calendar_access",
      "mcp__google-calendar__cancel_event", "mcp__google-calendar__move_event",
      "mcp__google-calendar__create_event_declining_all",
      "mcp__google-calendar__delete_calendar", "mcp__google-calendar__clear_calendar"
    ]
  }
}
```

## Getting help

If something does not work, run `google-calendar-mcp status` and
`google-calendar-mcp doctor`. `status` says which account is signed in,
where the token lives and which scopes were granted. `doctor` calls
Google and names each check that failed, and what to do. Most first-run
trouble is an API that was never enabled or a scope that was never
granted. [`docs/troubleshooting.md`](docs/troubleshooting.md) has the
fixes.

If that does not explain it,
[open an issue](https://github.com/mmedum/google-calendar-mcp/issues).
Never paste event titles, guest lists, calendar ids or your OAuth client
JSON into one. Security problems go through [`SECURITY.md`](SECURITY.md),
privately.

## Versioning

Tool names, their output fields and the resource URIs are stable within
a major version. A change needing you to act, such as an added scope,
another login or a different setting, is marked **Breaking:** in
[`CHANGELOG.md`](CHANGELOG.md), which is also what each release's notes
are made from.

## Development

`make check` runs everything CI runs. `make live` drives the built binary
against a real account, and `make evals` scores a model against the
tools; both are run by hand.
[`CONTRIBUTING.md`](CONTRIBUTING.md) covers the gates, the live run and
the review flow.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): the request flow, the
  package layout, and the design decisions a contributor should not
  undo.
- [`docs/configuration.md`](docs/configuration.md): every setting.
- [`docs/gcp-setup.md`](docs/gcp-setup.md): the Google Cloud project and
  OAuth client, step by step.
- [`docs/troubleshooting.md`](docs/troubleshooting.md): symptoms and
  their fixes, then rotating the client secret, revoking access and
  running two accounts.
- [`docs/security.md`](docs/security.md): the threat model.
- [`docs/release.md`](docs/release.md): how a release is cut and
  checked.

## License

Apache 2.0. See [`LICENSE`](LICENSE).
