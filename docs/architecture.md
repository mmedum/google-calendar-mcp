# Architecture — google-calendar-mcp

**Status: phase 6 is built; v1.0.0 is being cut (2026-09-17).** Phases 0 to 4 —
the scaffolding and the time model with the six read tools; `internal/recur`,
`list_instances` and `check_availability`; `internal/plan`, the five event
writes, `If-Match`, the client-generated id and `dry_run`; the two calendar
tools, the three sharing tools and the two gated ones; and the three
resources, the working-hours mask, the conference parameter, the attendee
warning, `scripts/evals` and the `.mcpb` bundle — are built, verified live
and committed. Phase 5 is the release those four had nowhere to ship in:
`.goreleaser.yaml`, `.github/workflows/release.yml`, the `release` gate
that holds one against the other, and `gates release-notes`. Phase 6 is
`list_changes` and §17.1, the last open decision, now closed. The surface
is **twenty-one tools and three resources**; `make check`
is green across **twenty-two** targets; the live driver's last run was 85
steps against a real account with none failing.

**The release is rehearsed and has never run for a tag.** Two `--snapshot`
builds produced the six archives, the bundle and `checksums.txt`, and the
version agrees in all five of §10b's places. Signing and provenance are
the part no rehearsal reaches: both need an OIDC token only a real
workflow run has, so the first tag is the first time they execute. Watch
that run.

**What phase 5 cost and taught, in one line each.** The packer's macOS
glob **could never have matched anything**: goreleaser names that
directory `<id>_darwin_all`, so the id comes first and
`dist/*darwin*universal*/` reads the two words in the wrong order (§18
row 64). Every gate was green with it in the tree, because the packer
only runs at release time and there was no release — which is the whole
argument for the `release` gate reading the build matrix on every commit.
The binary's own `--version` was the one of §10b's five version checks a
rehearsal could not perform, because the ldflags stamped `{{ .Tag }}` and
a snapshot has no tag (§18 row 65). And the `pins` gate's failure message
said "expected at least CI and release" while the check counted to two,
which `ci.yml` and `codeql.yml` satisfied — row 62 one level down, a
message that names a file beside a check that counts them.

**The cleanup pass found worse than the phase did**, which is the second
lesson. The tool-pin check this phase added — the half the standard calls
the one that reads as complete when it is not — **was itself that**: it
read every line of a step rather than the `with:` block, so a `version:`
under `env:` satisfied it, and it understood one of YAML's two list
styles, so a workflow it could not read reported no problems (§18 row
69). Both were found by running it, not reading it. And the "more
general" fix for the staleness gate's blind spot — derive the documented
roots from the repository instead of listing them — is **circular**: a
token counts as a path only if its root exists, so a missing file files
itself as prose (§18 row 70). Shape, never existence.

**What phase 4 cost and taught, in one line each.** Spike M asked what
Google does with a conference create request and answered both halves
the code depended on: **without `conferenceDataVersion=1` the conference
is dropped in silence** — 200, event created, no link (§18 row 61) — and
**with it the link arrives on the insert itself**, which refuted the
"read the event again" wording this phase had written into the tool
description, the result note and three comments (§18 row 60). A URI
template written the obvious way would have made **every secondary
calendar unreachable as a resource**, because simple expansion does not
match the at sign every calendar id carries (§18 row 59). And §16's
phase 0 entry claims goreleaser and a release workflow that **never
existed**, so the bundle this phase built is packed by hand rather than
by a signed release (§18 row 62).

**Still owed**, said here rather than left implied: a second MCP client
and the bundle installed from a real desktop, both deferred by decision,
and spike G's negative half from phase 0. **§17 is closed** — every
decision in it is decided and built. `list_changes` has been driven live: **88 steps,
none failed**, and the run cost two findings (§18 rows 73 and 74) that no
test against a fake could have produced.

**What phase 3's live runs cost and taught. Three runs, and the second is
the one worth reading.** The first failed two steps and both came from
the same thing nobody knew: **Google refuses to let a
calendar's data owner remove it from their own list** (§18 row 55). So
`manage_calendar` had been offering an unsubscribe that could not work on
any calendar the caller had made, and the second failure was the first
one's consequence — the calendar was still subscribed, so subscribing
again took the "already there" path the step was not written for. The
refusal is translated now into the two things that do work, and the
driver holds the translation instead of the operation, because every
calendar it has is one it made and the working path would mean reading
past what it wrote (§9.1).

**Both spikes answered on the first run.** K: `calendars.clear` on a
secondary calendar is **refused with 400**, so `clear_calendar`'s guard
is the API's rule rather than this server's caution — and the third
possibility that probe was built to catch, accepted-and-did-nothing, did
not happen. L: `calendars.patch` and `acl.patch` both refuse a **stale
`If-Match` with 412**, so §4.4 covers the calendar and sharing writes
without qualification, exactly as spike J settled it for `events.move`.

**Then the second run, which passed 77 steps and lost a verdict.** Spike
L came back UNDETERMINED on a question it had answered an hour earlier:
it patches the scratch calendar's description to move the etag on, the
description was a fixed string, and on the second run it was already
that value — so **Google left the etag alone and no stale one could be
built**. Two things came out of that. The spike carries the run's own
mark now, because a probe that only works on a calendar it has never
touched is a probe that works once. And the claim it tripped over —
that a patch rewriting a field to itself still moves the etag — was in
`internal/plan`'s comments as the REASON a no-op field is never sent,
and is refuted for calendars (§18 row 58); the rule survives on its own
merits, because a change list should describe the write rather than the
request, and the comments say that instead. The third run is 77 steps,
none failed, spike L answering on a calendar it had already patched.

**What phase 2's live runs cost and taught.** Six runs. The first failed
five steps: three were the driver's own assertions, which grepped a whole
rendered page for a date or a clock time and so were satisfied by a probe
event that had nothing to do with them. One was a step that cancelled an
occurrence the seed had already cancelled. The fifth was real — Google's
`self` flag is not set on the account's own attendee row on a secondary
calendar, so a write that reached nobody demanded a notification choice
(§18 row 50). Then the transcript, read rather than counted, showed two
more that every gate had passed: **a successful move reported itself as
`[cancelled]`**, because `events.move` answers with that status while the
event sits confirmed on the destination (§18 row 48); and a cancellation
dry run said "Deleted the event" under the words "nothing was written".
And spike J settled the one question phase 2 had left open with a
disclaimer: **`events.move` does honour `If-Match`** (§18 row 49), so
§4.4 has no exception and `move_event` takes an etag like everything
else.

**Ten spikes are answered and one half is not (§15).** A, B, C, D, E, F,
H, I, J, K and L have run. Only spike G's negative half is still owed,
and phase 3 built the `GCAL_SHARING=off` path it needs, so it is now one
command rather than an errand. K and L both answered on their first run
and both changed what a result may claim: `clear` on a secondary
calendar is refused by the API rather than by this server's caution, and
a stale `If-Match` is refused with 412 by the calendar and sharing
writes as it is by every other one.

**What the live work cost and taught, in one line each.** The first run
failed one step and was quietly wrong about two that passed. Spike I
reported a verdict about a ceiling it never reached. A field's published
description misled twice, on `showDeleted` and on `sendUpdates`. Three
consistent runs of spike A were consistent because the instrument was,
and only a second receiver on the same event separated sending from
delivery. The driver deleted guest-carrying events with `none` for a
day and left meetings on two real calendars that the organiser could no
longer withdraw. Nine of §18's forty-four rows were written or rewritten
on 2026-09-16, four of them correcting something this document had
asserted earlier the same day.

**Still owed:** spike A's `externalOnly` arm and spike B, which need an
out-of-domain guest and a non-Google one and cannot be scored by any
driver (§15); spike G's negative half, whose `GCAL_SHARING=off` path
phase 3 has now built.

**Corrected in phase 5: CI HAS run on macOS and Windows**, and this
paragraph said otherwise while row 46 — three screens down — described
that very run failing on all three platforms. Checked against the
repository's own run history: the latest `ci.yml` run is green on
`test (ubuntu-latest)`, `test (macos-latest)` and `test (windows-latest)`
alike. What remains untested there is narrower and worth naming: the
keyring, the file fallback's permissions and path handling, none of
which a compile covers.

**What phase 1 found in phase 0's own record.** Three gates this
document listed as part of `make check` did not exist: `api-fields`,
`live-cover` and `mcpb`. `parity` did not notice, because it compares
`make check` with CI and both were equally short — a list of gates is
not a gate. `internal/gcal`'s doc comment said a gate held its field
list; it did not. `testdata/golden/` was an empty directory while §13
described golden files as part of the test surface. Phase 1 built the
first two gates and the golden files; `mcpb` belongs with the bundle in
phase 4. The first thing `live-cover` did was fail on both tools phase 1
had just added, which is the argument for it.

**What the live run found that the gates could not.** Two defects, both
in code that reads as obviously correct. Event ids are base32hex —
lowercase `a`–`v` and the digits, so `w`, `x`, `y` and `z` are refused —
and the driver's own ids used `y`; Google answered "Invalid resource id
value" naming neither the field nor the rule. And `login` never reported
which account had signed in, because `tokeninfo` returns an email only
when an email scope was granted and this server asks for none, leaving
`status` with a field that could never populate. Neither is the kind of
thing a unit test against a fake would have produced, which is why §13
says green gates are not done.

**A test signed the maintainer out of their own Google account, and the
repair is the interesting part.** `TestLogoutWithNothingStored`
redirected the config directory and the environment, and could not
redirect the OS keyring — so it found the real refresh token under the
`default` profile, revoked the grant at Google and deleted it. The
sibling servers document this exact failure, and the warning is quoted
in this repository's own `internal/credentials` source: it was written
here *before* it happened. A rule stated is not a rule kept. The fix is
structural rather than a reminder — `TestMain` substitutes the keyring
for the whole `cmd` package before any test runs, so reaching the real
one is impossible rather than discouraged, and a decoy test fails if a
refactor drops it.

**What the gates caught while being built, which is the argument for
having them.** The class gate found six classes it called dead; the gate
was wrong about four of them (it excluded the file where `classify`
emits most of the vocabulary) and right about two. The parity gate
reported a divergence that did not exist, by matching CI for a literal
string instead of mapping targets to the steps that run them. The
coverage gate double-counted every block, because `-coverpkg` makes each
test binary emit a profile for every package. The staleness gate found
the README naming two tools that do not exist and this document naming a
package path that does not. The smoke gate found the server refusing to
start without credentials — so a client could not list its tools before
login, and every host would have logged that as a crash. The race
detector found a data race in the test fake. Each of those was a rule
that read as true and was false in the code, which is what the standard's
preamble says to expect.

**Everything here was checked against the Calendar API v3 discovery
document** (`www.googleapis.com/discovery/v1/apis/calendar/v3/rest`,
revision 20260826, fetched 2026-09-15 and refetched unchanged
2026-09-16), the Calendar guides, and the public MCP calendar servers
named in §1. §18 is the evidence log, 54 rows. More than a third of them
refute an assumption this design started out holding; phase 3 added four
before it wrote any code, one of which removed a parameter rather than
adding one; and one reversed the single most consequential decision in
this document — §4.3, where the maintainer's own proposed default
turned out to be the thing Google warns in writing can lose a user's
events.

**The three things worth reading first**, because they are where this
server differs from every calendar integration surveyed:

- **§4.1 — a date is not a time.** All-day events are carried as dates
  and never as instants. Every other calendar tool surveyed normalises
  them to UTC midnight, which puts them on the wrong day for every user
  west of UTC.
- **§4.2 — the recurrence scope is required, never inferred.** "Change
  the event" is three different operations on a recurring series and
  Google's API makes them look like one. The server refuses to guess
  which was meant.
- **§4.3 — notification is a required choice with no default.** Not
  "never notify", which Google warns can lose events; not Google's own
  default, which is unspecified, inconsistent between methods and free
  to change.

§2 is what the platform forces, §8a the verdict on all 38 published
methods, §16 the phase plan, §17 the decisions still open.

This document is written so that whoever picks the work up can start
from the repository alone: read the status line, §16 for the phase, §17
for anything open, and begin.

## 1. Mission and scope

A production-grade, Go, stdio MCP server that lets Claude work with
**time**: read a schedule and know exactly which hours in which zone it
is looking at, find when people are free, create and change events
without silently dropping the guests already on them, and say afterwards
what changed and who was told. Single binary, per-user OAuth against the
user's own Google account, Workspace or consumer.

**The repository is self-contained and meant to be distributed.** Every
deployer creates their own Google Cloud project and OAuth client;
nothing deployer-specific is baked into the code, the repository or the
release artifacts (§9, §12).

**Scope is the calendar, every capability.** In: calendars and the
subscribed list, events and their recurrences, availability, sharing,
colours and the user's own settings. Out (**decided**): everything a
meeting *produces* rather than *is* — the Meet recording, the notes
document, the file attached to an event — which belongs to the servers
built on the Drive and Docs APIs. Google Tasks is a separate API and a
separate server. A conference attached to an event is a Calendar field
and is in scope; the Meet API's recordings and transcripts are not
(§17.3).

**The boundary sentence, for tool descriptions:** this server answers
*when*, and hands off *what*.

Tool names are chosen so a client can run this server beside the Drive,
Docs and Sheets ones without a collision: `search_events` and
`list_calendars` here, where those say `search_files`,
`search_documents`, `search_spreadsheets`.

### Why build it (research summary, verified 2026-09-15)

Eight public Google Calendar MCP servers were surveyed. Every one is
Node or Python. They converge on the same shape — a `createEvent`, an
`updateEvent`, a `listEvents` — and they converge on the same three
defects, which §3 states as requirements. Those defects are not
incidental: each one is the natural result of modelling a calendar as a
list of rows, which is what an API client library hands you.

The gap worth building into is not more tools. It is a server that is
correct about **time**, **recurrence** and **who gets emailed** — the
three things a calendar has that a spreadsheet does not, and the three
things the surveyed servers get wrong.

### Non-goals

- **Push notifications.** `events.watch`, `acl.watch`, `settings.watch`
  and `channels.stop` need a public HTTPS endpoint Google can POST to. A
  stdio server has nowhere to put one. Out, permanently (§8a).
- **Calendar migration.** `events.import` exists to carry a private copy
  of an event between systems, preserving `iCalUID` and organiser. It is
  a migration tool, not a scheduling one, and the failure modes are
  entirely different. Out (§8a).
- **Admin operations.** `calendars.transferOwnership` requires a
  Workspace administrator privilege and `useAdminAccess: true`. This
  server authenticates as one ordinary user and never requests admin
  access. Out (§8a).
- **A local event cache or sync loop.** `syncToken` is designed for a
  client that keeps a replica. This server is stateless between calls;
  a replica it cannot invalidate is a replica that lies. §17.1 keeps the
  question open for a later phase, and §4.5 is what is done instead.
- **Natural-language event parsing.** `events.quickAdd` parses a string
  like "Dinner Thursday 7pm". The model calling this server is a better
  parser than Google's, and quickAdd hides which fields it set and in
  which zone it read the time. Out (§8a) — this is a refusal on quality
  grounds and §17.4 records the argument against it.

## 2. Hard constraints from the platform

These are not preferences. They are what the API does, and the design
either accommodates them or lies.

1. **An `EventDateTime` is `date` XOR `dateTime`.** `date` is
   `yyyy-mm-dd` and means an all-day event. `dateTime` is RFC3339 and
   requires an offset unless `timeZone` is set alongside it. The two are
   not convertible: an all-day event has no instant, and forcing one
   picks a day.
2. **`timeZone` is required on a recurring event's start and end.** The
   discovery document states it outright. A recurrence expanded from a
   UTC instant drifts an hour at every daylight-saving boundary; one
   expanded from a zoned wall-clock time does not.
3. **`events.update` is a PUT and replaces the whole resource.** Omit
   `attendees` and every guest and every RSVP is gone. Omit `recurrence`
   and the series is flattened. `events.patch` is the partial one.
4. **ETags and `If-Match` are fully supported, and return 412.** Calendar
   is the one Google Workspace API that documents optimistic concurrency
   properly. `If-Match: *` forces the write through without a read.
5. **`sendUpdates` has no consistent default.** On events it is
   effectively `none`/false. On `acl.insert`, `acl.patch` and
   `acl.update` the equivalent `sendNotifications` defaults to **true**.
   Sharing a calendar emails somebody unless you say otherwise; creating
   an event does not unless you say otherwise. Same API, opposite
   defaults.
6. **`none` is not a guarantee of silence.** Every method's
   documentation carries the sentence "some emails might still be sent
   even if you set the value to false".
7. **Google warns against `none` on `events.insert` specifically.** Its
   own enum description: "Using the value `none` can have significant
   adverse effects, including events not syncing to external calendars
   or events being lost altogether for some users." No other method
   carries this warning.
8. **"This and following" is two API calls, and it resets exceptions.**
   Google's recurring-events guide: set `UNTIL` on the original, insert
   a new series from the target. "Changing all following instances
   resets any exceptions happening after the target instance."
9. **`singleEvents` changes what `events.list` returns.** False (the
   default) returns recurring *parents* with their `recurrence` and no
   instances. True returns the expanded *instances* and no parents.
   `orderBy: startTime` requires `singleEvents: true`.
10. **`freebusy.query` covers at most 50 calendars** per request
    (`calendarExpansionMax`, maximum value 50), and returns per-calendar
    errors rather than failing the whole query.
11. **A client may supply an event `id` on insert**, base32hex, 5–1024
    characters, unique per calendar. But: "Due to the globally
    distributed nature of the system, we cannot guarantee that ID
    collisions will be detected at event creation time." Nearly
    idempotent, not idempotent (§11).
12. **`eventType` cannot be modified after creation**, and `fromGmail`
    events cannot be created at all. Birthdays, focus time, out-of-office
    and working-location events are not ordinary meetings and carry their
    own property blocks.
13. **Cancelled events are hidden by default.** `events.list` returns
    them only with `showDeleted: true` or on an incremental sync;
    `events.get` always returns them.
14. **Quotas: 10 000 requests/minute per project, 600/minute per user**,
    on a sliding window, returning 403 or 429 `usageLimits`.
15. **`acl.list` is not covered by `calendar.readonly`.** It needs
    `calendar.acls` or `calendar.acls.readonly`. `acl.get` *is* covered
    by `calendar.readonly`. Reading one rule and reading the list of them
    take different scopes (§18).

### What the API cannot do (so we don't promise it)

- **No server-side "this and following".** It is a client-side pattern
  built from two calls, and it loses exceptions (§2.8).
- **No transactional multi-event write.** There is no batch endpoint in
  the union sense; creating three events is three requests, and the
  second can fail after the first succeeded.
- **No search across calendars.** `events.list` takes one `calendarId`.
  Searching several is several requests, fanned out by this server.
- **`q` is a free-text match, not a field query.** It has no documented
  syntax, no field scoping and no guarantee about which fields it reads.
  A result set from `q` is a suggestion (§7.2).
- **No way to know whether mail was actually sent.** The API reports
  nothing about notifications. `sendUpdates` is a request, and §2.6 says
  it is not even an honoured one.

## 3. Requirements distilled from other servers' failures

Each row is a defect found in a surveyed server or its issue tracker,
and the requirement it forces here.

| Failure seen | Requirement |
|---|---|
| All-day events normalised to UTC midnight, rendering on the wrong day for every user west of UTC | A `date` is carried as a date end to end and never becomes an instant (§4.1) |
| Recurring events written with a UTC instant, so a weekly 09:00 drifts to 08:00 after a daylight-saving change | Every recurring write carries an IANA `timeZone`; the server refuses a recurrence without one (§4.1) |
| `sendUpdates` never set, so invitations that reported success reached nobody | `notify` is required on every write that can reach a person; there is no default (§4.3) |
| "Update the event" applied to a whole series when one instance was meant, or the reverse | `scope` is required on every event write: `instance`, `series` or `this_and_following` (§4.2) |
| `events.update` used for a partial change, silently dropping attendees and RSVPs | The server never calls `events.update`. Patch, with `If-Match` (§4.4) |
| Availability answered by listing events, missing events whose details the caller cannot see | Availability comes from `freebusy.query`, which sees busy blocks without details (§4.6) |
| The server's own timezone (or the container's UTC) used as the user's | The zone is read from the user's settings or the calendar, never from the process, and every read names the zone it used (§4.1) |
| A truncated event list presented as the whole schedule | Every read states its window, its zone and whether it was truncated (§4.5) |

## 4. Core design bets

### 4.1 Time is always zoned, and a date is not a time

The single largest source of defects in this category of software. Three
rules, held by tests and by the type system.

**An all-day event is a date.** `internal/when` has two distinct types:
a `Date` (`yyyy-mm-dd`, no zone, no instant) and a `Zoned` (an instant
plus the IANA zone it was expressed in). There is no conversion from
`Date` to `Zoned` in the package, because there is no correct one — the
caller must supply a zone and say so, and only the renderer does that,
for display, labelled.

**Every `dateTime` carries its IANA zone, not just an offset.** An
offset is a fact about one moment; a zone is a rule. `+02:00` is not
enough to expand a recurrence correctly across a daylight-saving
boundary, which is why §2.2 makes `timeZone` mandatory there. The server
sends `timeZone` on every write, recurring or not, so the same code path
is exercised every time rather than only on the rare path.

**The zone is read, never assumed.** In order: the zone the caller named
on the call; else the target calendar's `timeZone`; else the user's
`timezone` setting from `settings.list`. Never the process's local zone
and never UTC-as-a-fallback. Every read names which of the three it used
and what it resolved to. This is the direct analogue of the sibling
Sheets server's "sheet names are read, never assumed", and it exists for
the same reason: the default that looks harmless on the maintainer's
machine is wrong on everyone else's.

A consequence worth stating: **the server has no opinion about "today"**
until it has resolved a zone. A tool that takes a relative window
resolves the zone first and prints the absolute window it derived.

### 4.2 The recurrence scope is required, never inferred

"Change the 10:00 standup to 10:30" is three different operations and
the API makes them look like one. A tool that takes an event id and a
patch cannot tell which was meant, and the surveyed servers guess.

Every event write takes a required `scope`:

- **`instance`** — one occurrence. Addressed by the instance's own id, or
  by the series id plus an `original_start`. Creates an exception.
- **`series`** — the parent event, and therefore every occurrence that is
  not already an exception.
- **`this_and_following`** — the two-call pattern of §2.8. The server
  performs it, and its result says in plain words that **exceptions
  after the target instance were reset**, because Google's guide says
  they are and no caller expects it.

There is no default. A write against an event that has a
`recurringEventId` or a `recurrence` field and no `scope` is refused with
`[invalid]` and a message naming the three choices and what each would
do here — including, for `series`, how many occurrences it would reach.

Google's own guidance is enforced as a warning, not a refusal: "Do not
modify instances individually when you want to modify the entire
recurring event." When a caller writes the same field to the same value
across several instances of one series, the result says a `series` write
would have been one call.

### 4.3 Notification is a required choice, and the server has no default

**This bet reversed during design, and the reversal is the point.** The
maintainer's proposal — and the obvious reading of the sibling Drive
server's "no mail unless `notify` is set" — was to default `sendUpdates`
to `none` everywhere. Checking it against the discovery document refuted
it: Google's own enum description for `none` on `events.insert` says
"Using the value `none` can have significant adverse effects, including
events not syncing to external calendars or events being lost altogether
for some users." A safe-looking default is the one documented to lose
data.

The opposite default is no better. Google's own defaults are
**inconsistent between methods** (§2.5): events default to not
notifying, ACL rules default to notifying. Adopting "whatever Google
does" means the same `notify`-shaped decision has opposite outcomes in
two tools, for a reason nobody can see from the call.

So: **`notify` is a required parameter on every write that can reach
another person, and the server refuses the call if it is absent.** No
default, in either direction. The refusal names who would be reached.

Five supporting rules:

1. **The refusal is specific.** `[invalid] this event has 4 guests; pass
   notify to say whether they are emailed` — with the count, never the
   addresses (§9).
2. **A write that reaches nobody does not ask.** An event with no
   attendees on a calendar shared with nobody has no `notify` decision to
   make, and demanding one is friction with no safety in it. The
   parameter is required only when the write can actually reach a person.
3. **A result says what the server asked for, never what a guest
   received.** `none` is never reported as silence, because Google says
   some mail may go out anyway (§2.6). `all` is never reported as
   delivery, because a notification Google sends can still fail to
   arrive: spike A watched one invitation reach one of its two guests
   and not the other, the difference being the receiving provider and
   nothing in the request (§18 row 42). The server knows what it asked
   for; it does not know what landed, and will not say it does.

   **And `none` on a cancellation is worse than quiet — it is a lie.**
   Deleting an event with `none` leaves it on the guests' calendars
   while removing it from the organiser's (§18 row 43). `cancel_event`
   therefore says, in its result, that the guests still have the meeting
   unless they were notified.
4. **`none` is refused when a guest is outside the organiser's domain**,
   not merely warned about. Such a guest may have no Google Calendar at
   all, and then mail is the only channel that exists: spike B invited a
   non-Google address with `none`, it received nothing, and there was no
   calendar for the event to land in. The guest cannot discover the
   event by any means, while the organiser's copy shows them invited.
   That is §2.7's "lost altogether" with the mechanism visible, and it
   is structural rather than a defect — so it earns a refusal rather
   than a warning a caller can skim (§18 row 44).
5. **`dry_run` shows the blast radius before anything is sent**: how
   many guests would be notified, how many of them are outside the
   organiser's own domain and so are the ones `externalOnly` reaches, and
   what the event would look like afterwards.

   **The axis is the organiser's Workspace domain, and it took a live
   probe to establish that.** This document first said "internal versus
   external"; the discovery document appeared to refute it, describing
   `externalOnly` as "notifications are sent to non-Google Calendar
   guests only", so the wording was changed to split by calendar system;
   then spike A mailed the out-of-domain guest and skipped the
   same-domain one, both of them on Google Calendar (§18 row 40). The
   original wording was right and the documentation was wrong.

   The count is computed from the organiser's primary calendar id, which
   is the address, against each attendee's domain — never printed, only
   counted (§9).

The ACL tools carry the same parameter with the same requirement, which
is what makes the two consistent where Google's defaults are not — but
**not the same vocabulary, and phase 3 found out why.** `acl.insert` and
`acl.patch` take `sendNotifications`, a boolean, so `external_only` has
nothing to map to: it is refused as `[unsupported]` rather than rounded
to one of the two, because rounding it would email either more people or
fewer than the caller asked for (§18 row 52). And `acl.delete` publishes
no such parameter at all — Google sends nothing when access is removed,
so `unshare_calendar` takes no `notify` and says in its result that
nobody is told (§18 row 51).

There is also no "reaches nobody" exemption on a sharing change, and
that is the honest position rather than an oversight: a group address
expands to people this server cannot count, a domain rule covers
everybody in it, and the public scope covers the internet. The reach is
not knowable here, so the choice is always the caller's.

### 4.4 A write never destroys what it cannot see

The sibling Sheets server had to record a deviation here: a rectangle of
cells cannot be minimally diffed, so it refuses instead. Calendar does
not need the deviation. The API supports partial updates and optimistic
concurrency properly, so the standard's rule is implementable as
written.

- **`events.update`, `calendars.update`, `calendarList.update` and
  `acl.update` are never called.** They are PUTs. §8a writes each of them
  off by name, and a gate holds the client to it: the four methods have
  no client function, and adding one fails the API-coverage check.
- **Every write is a patch**, carrying only the fields the caller asked
  to change.
- **Every patch carries `If-Match` with the etag from the read that
  produced the plan.** A 412 becomes `[stale]` with "re-read and try
  again", never a silent retry — a retry here would apply the caller's
  intent to a resource somebody else has since changed.
- **`If-Match: *` is available and is not the default.** It exists for
  the caller who genuinely means "whatever it says now", and saying so is
  an explicit flag.
- **Guest lists are read before they are written.** Adding an attendee is
  a read-modify-write on the array, never a replacement of it, so an RSVP
  that arrived between the read and the write is reported as `[stale]`
  rather than overwritten.
- **`events.move` carries `If-Match` too, and finding that out is the
  argument for §15.** It is not a patch — a POST with the destination in
  the query string and no body — and nothing Google publishes says the
  header applies. So this server sent none and its result told callers
  the protection was absent, which was honest about the uncertainty and
  wrong about the API. Spike J sent a stale etag and got **412**, so the
  rule has no exception: every write here is made under `If-Match`
  (§18 row 49).

### 4.5 Every read states its window, its zone and its completeness

The analogue of the Sheets server's "every read shows addresses". A
model asked "am I free Thursday afternoon" must not be able to answer
from a different Thursday, a different zone, or the first page of three.

Every read result carries: the absolute window queried, the IANA zone
the times are rendered in and where that zone came from (§4.1), which
calendars were read, how many events matched, how many are shown, and a
continuation token when there are more. A truncated read says so in the
text a model sees, not only in a structured field it may not be shown
(§10 and the standard's note on `content` versus `structuredContent`).

Reads are budgeted in events, not requests, with the budget stated in
the result.

### 4.6 Availability is computed, never inferred from a list

`freebusy.query` returns busy intervals for calendars whose *details* the
caller cannot read. Answering "is this person free" by listing their
events misses everything they have marked private, and ignores
`transparency` — an event marked "free" is not busy, and a list-based
answer counts it.

So availability is `freebusy.query`, fanned out in batches of 50 (§2.10),
with two rules: a calendar that returned an error is named in the result
as **unknown**, never folded into "free"; and the result distinguishes
"no busy blocks" from "could not read this calendar", because a model
that cannot tell those apart will book over somebody.

### 4.7 One tool call is one API request

Held everywhere except where it cannot be, and the exceptions are named
in the tool description rather than hidden: `this_and_following` is two
writes (§2.8), an availability query across more than 50 calendars is
one request per batch (§2.10), and a cross-calendar search is one request
per calendar (§2, *no search across calendars*). Each of those says in
its result how many requests it made.

**The calendar and sharing writes are two to four requests each, and the
reason is the same in every case: a write is preceded by the read that
produces its etag and its before-state.** `manage_calendar` is one patch
per axis it touches; `share_calendar` reads the ACL first, because §7.6's
before-and-after is the answer the caller asked for; the destructive two
read the calendar for the version they are held to. The counts are
pinned by a test that compares what the result reports with what the fake
served, because the number in a result was kept by hand once and drifted
to 2 while the call spent 168.

### 4.8 Own wire types, raw REST

No `google.golang.org/api`. The wire types live in `internal/gcal`,
hand-written for the fields actually used, and `internal/gapi` calls the
REST endpoints directly. §8b is the field-level coverage gate that keeps
"the fields actually used" an argued list rather than an accident.

### 4.9 Results say what changed, and who was told

Every write returns: what the resource looked like before, what changed,
what it looks like now, how many people were notified and by which
`sendUpdates` value, the new etag, and — where §2.6 applies — that
silence is not guaranteed. A write result that says only "ok" is a
defect.

## 5. Module layout

Derived from `go list ./...` and checked by the staleness gate, per the
standard's "derive the package map, or delete it".

- `cmd/google-calendar-mcp/` — subcommands (`login`, `logout`, `status`,
  `doctor`) and server wiring.
- `internal/config/` env plus bound flags; `internal/credentials/`
  keyring → file → env; `internal/userconfig/` non-secret profile state;
  `internal/fileperm/` restricting a file to the account that wrote it;
  `internal/auth/` loopback OAuth and the token source.
- `internal/gcal/` wire types; `internal/gapi/` the raw REST client, with
  `caltest/` the in-memory Calendar used by tests.
- `internal/when/` dates, zoned times, windows and the zone-resolution
  order of §4.1. No network, no clock of its own — the clock is injected.
- `internal/recur/` RRULE parsing and formatting, instance expansion for
  what the server must reason about locally, and the three scopes of
  §4.2.
- `internal/model/` the server's view of a calendar, an event and a busy
  interval; `internal/render/` text output; `internal/plan/` the typed
  write ops and the guards of §4.2, §4.3 and §4.4 — no network, no
  client, so every guard is testable on its own;
  `internal/service/` orchestration and policy;
  `internal/tools/` the MCP tools; `internal/server/` SDK wiring and the
  schema dump.
- `internal/redact/` the log and transcript redactor.
- `scripts/gates/` the repository's own checks, as Go; `scripts/livecal/`
  the live driver, which also carries the live probes of §15;
  `scripts/evals/` the model-facing scoring harness, run by hand.
- `packaging/mcpb/` the bundle's manifest and Linux launcher; the packer
  and its gate are two subcommands of `scripts/gates` (§12).
- `testdata/` synthetic fixtures, renderer goldens, the API surface
  snapshot and coverage records of §8a and §8b, and the recorded
  tool-schema baseline.

## 6. Addressing

### 6.1 Referring to a calendar

Three forms, in this order:

1. **`primary`** — the authenticated user's own calendar. The API's own
   alias; passed through.
2. **A calendar id.** For a user's primary calendar this is their email
   address; for a secondary calendar an opaque id. Ids are the contract.
3. **A title**, resolved against `calendarList.list`. A title matching
   more than one calendar is `[ambiguous]` with the candidates and their
   ids, never the first match — the sibling Drive server's rule, and the
   failure mode is the same here.

A calendar the user is not subscribed to does not appear in
`calendarList.list` and cannot be resolved by title. Its id still works.
The refusal says so, because "not found" for a calendar the caller can
read is a lie.

### 6.2 Referring to an event

By **event id plus calendar id**. An event id is unique per calendar, not
globally, so an id without a calendar is not an address.

An instance of a recurring series is addressed either by its own id or by
the series id plus `original_start` (§4.2). The server accepts both and
says which it used, because `originalStartTime` is the stable one:
§2 confirms it identifies the instance even after the instance is moved.

`iCalUID` is accepted on lookup (`events.list` takes it) and is never
used as the primary address: one `iCalUID` is shared by every occurrence
of a series, which is exactly the ambiguity §4.2 exists to remove.

### 6.3 Referring to a time

Callers pass one of:

- an **all-day date**, `yyyy-mm-dd`;
- a **zoned time**, RFC3339 with an offset, plus an optional IANA zone
  name that takes precedence for recurrence expansion;
- a **window**, two of the above, resolved and echoed absolutely.

Relative expressions ("next Tuesday") are the model's job, not the
server's — the model knows the user's intent and the server does not.
What the server does is make the resolution checkable: it echoes the
absolute window and the zone it used, so a model that resolved "Tuesday"
wrongly can see it did (§4.5).

### 6.4 Referring to a recurrence

An RRULE string, RFC 5545. The server parses it to validate and to
explain it back in prose ("every two weeks on Tuesday, 10 times"), and
sends the caller's string. It does not compose RRULEs from structured
fields: the round trip through a struct is where a `BYDAY` gets lost.

### 6.5 Error classes

Closed vocabulary, derived from the code by `scripts/gates classes` and
asserted in both directions — every class the code emits is listed here,
every class listed here is emitted somewhere. The standard's six, plus
five this API forces:

| Class | Means | Caller should |
|---|---|---|
| `invalid` | the request is malformed or under-specified | fix the arguments |
| `not_found` | no such calendar or event | check the address |
| `auth` | not signed in, or the scope is missing | run `login` |
| `forbidden` | signed in, but the ACL role is insufficient | ask for access |
| `conflict` | the resource state refuses this operation | read and reconsider |
| `stale` | the etag moved under you (412) | re-read and retry |
| `ambiguous` | a title matched several calendars | pass an id |
| `blocked` | a guard refused what the API would have allowed | pass the override, or don't |
| `rate_limited` | 403/429 `usageLimits` | back off (§11) |
| `unavailable` | a transient upstream failure | retry |
| `unsupported` | the API cannot do this | see §2 |
| `ambiguous_outcome` | a write may or may not have landed | read before retrying |

`ambiguous_outcome` earns its place from §2.11: a client-supplied event id
makes an insert *nearly* idempotent, and Google declines to guarantee the
collision is caught. A retry that might double-book is not a retry the
server performs silently.

`stale` and `conflict` are kept apart deliberately. They ask the caller to
do different things, and collapsing them into `conflict` — which is what
the standard's six-class list would do — loses the one piece of
information that makes 412 actionable.

## 7. Reading and writing

### 7.1 Locating and describing

`list_calendars` is the entry point and is cheap. It returns, per
calendar: id, title, the IANA zone, the caller's `accessRole`, whether it
is primary, whether it is selected and hidden in the UI, and its colour.
The zone is on this result specifically so the zone-resolution order of
§4.1 can be followed without a second call.

`get_calendar` adds the description, the sharing exposure (§7.6) and the
default reminders.

### 7.2 Reading events

`list_events` takes a calendar (or several), a window, and a required
decision about recurrence: `expand` (`singleEvents: true`, instances,
orderable by start time) or `series` (`singleEvents: false`, parents with
their RRULEs). §2.9 makes these return different things, and the surveyed
servers pick one silently; the caller picks here, and the result says
which it got.

Defaults that are decided: cancelled events are excluded unless asked
for (§2.13); `maxResults` is the API's 250 per page and the server pages
to its own event budget (§4.5); `eventTypes` is unfiltered, but the
renderer marks birthdays, focus-time, out-of-office and working-location
events as what they are rather than as meetings (§2.12).

`search_events` is `events.list` with `q`, fanned out across the named
calendars. Its description states plainly that `q` is undocumented
free text with no field scoping (§2), so a model treats an empty result
as "found nothing" rather than "there is nothing".

`get_event` returns one event whole. `list_instances` expands one series.

### 7.3 Availability

`check_availability` is `freebusy.query` (§4.6), batched at 50, returning
busy intervals per calendar plus an explicit **unknown** for any calendar
that errored. It also reports the free gaps in the window, computed from
the busy set, in the resolved zone — because the question behind the
question is almost always "when can we meet", and making the model do
interval arithmetic over a list is how a meeting gets booked at 02:00.

Three details are decided, all in phase 1:

- **A calendar missing from the response is unknown**, exactly like one
  that errored. That is what a query truncated at `calendarExpansionMax`
  looks like from here: no busy list, no error, no row.
- **An address is not resolved before it is asked about.** Free/busy is
  the one read that works on a calendar this account cannot open, so
  resolving the reference first would refuse the query the tool exists
  for. A title is still resolved, because a typo that silently became an
  id would come back "unknown" and read as a real answer.
- **Its own ceiling.** One call asks about at most 100 calendars, not
  `GCAL_MAX_CALENDARS`, because the cost is different: a schedule read
  spends a request per calendar and this spends one per fifty. The
  refusal says which limit it is, so nobody changes the wrong setting.

`min_minutes` drops gaps too short to use. Working hours are NOT applied
(§17.2).

### 7.4 Writing events

`create_event`, `update_event`, `cancel_event`, `move_event`,
`respond_to_event`.

Every one of them: takes `notify` when the write can reach a person
(§4.3); takes `scope` when the target is recurring (§4.2); patches rather
than replaces, under `If-Match` (§4.4); and returns a before/after with
the notification report (§4.9).

`create_event` generates the event id client-side (§2.11) so a retry
after an ambiguous failure is nearly idempotent, and reports
`ambiguous_outcome` rather than retrying when it is not sure.

`cancel_event` is `events.delete` for a single event and a
`status: cancelled` patch for one instance of a series — two different
API shapes behind one honest verb, with the result naming which happened.
It is **not** behind the destructive flag: cancelling a meeting is an
ordinary calendar action, it notifies by the same `notify` rule, and
Google keeps the record. §9 has the line.

`respond_to_event` sets the caller's own `responseStatus` and comment. It
is separate from `update_event` because RSVPing is not editing, the
permissions differ, and a model that conflates them will try to RSVP by
patching the whole attendee array.

### 7.5 Calendars and the subscribed list

`create_calendar`, `manage_calendar`. The second covers three things the
API splits across two resources and which users do not distinguish:
renaming or re-zoning the calendar itself (`calendars.patch`),
subscribing and unsubscribing (`calendarList.insert` / `delete`), and the
per-user overrides — colour, hidden, selected, notification settings
(`calendarList.patch`).

The tool description names the distinction that matters:
**unsubscribing removes it from your list; it does not delete it or
affect anybody else.** Deleting is `delete_calendar`, gated (§9).

**`manage_calendar` takes no `etag`, and the reason is the API's shape.**
An event is one resource with one etag, so §4.4's round trip — read it,
decide, write under the version you read — has one thing to hold. A
calendar is two, each with its own etag and its own method, so a single
`etag` parameter could stand for only one of them while appearing to
protect both. Each patch carries the etag of the read that produced it,
made immediately before, and the result shows before and after for every
field it changed, so a value the caller did not expect is visible rather
than silently overwritten.

**`delete_calendar` refuses the account's primary calendar**, because
Google's own description is "Deletes a secondary calendar"; and
**`clear_calendar` refuses anything but the primary**, because Google's
is "Clears a primary calendar". Neither refusal is invented: both quote
the method's published description, and **spike K then asked the API** —
`clear` on a secondary calendar is refused with 400, so that guard is
Google's rule rather than this server's caution (§18 rows 53 and 56).

**And a third refusal came from the live run rather than from any
document: the data owner of a calendar cannot remove it from their own
list.** Google answers 403, so `manage_calendar`'s unsubscribe could not
work on a calendar the caller made. It translates that into the two
things that do — `hidden:true`, or `delete_calendar` — rather than
passing on a sentence about data ownership (§18 row 55).

### 7.6 Sharing

`list_sharing`, `share_calendar`, `unshare_calendar` over the ACL
resource, following the sibling Drive server's sharing rules because the
hazard is identical.

- **Exposure is shown before and after.** A share result says who could
  see this calendar before and who can now.
- **`notify` is required** (§4.3) — and here Google's default is *true*
  (§2.5), so the requirement is what stops a quiet reshare from becoming
  a surprise email, and equally what stops a deliberate one from being
  silently suppressed. It is `all` or `none`: the ACL parameter is a
  boolean, so `external_only` is refused as `[unsupported]` rather than
  rounded (§18 row 52). It is required on every call, with no "reaches
  nobody" exemption, because the reach of a sharing change is not
  countable from here — a group address expands to people this server
  cannot see, and a domain rule covers everybody in one.
- **Removing access notifies nobody, and the result says so.**
  `acl.delete` publishes no `sendNotifications`, and `acl.patch`'s own
  description says there are no notifications on access removal. So
  `unshare_calendar` takes no `notify` at all and tells the caller that
  the person is not informed — they find the calendar gone (§18 row 51).
- **A share reads the rules before it writes.** That is what makes the
  before-and-after possible, and it also decides the scope type of an
  address the calendar already knows: "user" and "group" are the same
  shape, so an existing rule is Google's own answer to a question the
  server would otherwise guess at. When it does guess, the result says
  it guessed.
- **A public calendar needs an explicit flag.** `scope.type: default`
  means "anyone", and it is the one ACL value that cannot be undone from
  the other side. It requires `allow_public: true` on the call.
- **Roles are explained, not echoed.** `writerWithoutPrivateAccess` and
  `freeBusyReader` are not self-explanatory; the renderer says what each
  can actually see.
- **`GCAL_SHARING=off` removes the three tools**, as `GDRIVE_SHARING=off`
  does in the sibling.

## 8. Tool surface

Twenty tools. Read-only mode registers the first eight and requests only
the read scopes (§10).

| Tool | API methods | Notes |
|---|---|---|
| `list_calendars` | `calendarList.list` | entry point; carries the zone (§7.1) |
| `get_calendar` | `calendars.get`, `calendarList.get`, `acl.list` | exposure needs the ACL scope (§2.15) |
| `list_events` | `events.list` | `expand` vs `series` required (§7.2) |
| `get_event` | `events.get` | |
| `list_instances` | `events.instances` | |
| `search_events` | `events.list` (`q`) | fanned out; `q` is unscoped (§2) |
| `check_availability` | `freebusy.query` | batched at 50; free gaps (§7.3); working-hours mask (§17.2) |
| `get_settings` | `settings.list`, `colors.get` | the user's zone and week start |
| `create_event` | `events.insert` | client-side id (§2.11); optional Meet link (§17.3) |
| `update_event` | `events.patch` | `scope` + `notify` + `If-Match` |
| `cancel_event` | `events.delete`, `events.patch` | not gated (§7.4) |
| `move_event` | `events.move` | between calendars |
| `respond_to_event` | `events.patch` | RSVP only (§7.4) |
| `create_calendar` | `calendars.insert` | |
| `manage_calendar` | `calendars.patch`, `calendarList.insert/patch/delete` | three axes (§7.5) |
| `list_sharing` | `acl.list` | |
| `share_calendar` | `acl.insert`, `acl.patch` | `notify` required; public needs a flag |
| `unshare_calendar` | `acl.delete` | |
| `delete_calendar` | `calendars.delete` | **gated**, needs `confirm` |
| `clear_calendar` | `calendars.clear` | **gated**, needs `confirm` |

Resources, for clients that attach rather than call: `gcal://calendars`
(the list), `gcal://calendars/{+calendar_id}` (the card) and
`gcal://calendars/{+calendar_id}/events/{+event_id}` (one event). They
carry the same content as the matching tools, computed once, and no
handles.

The `+` is RFC 6570's reserved expansion and is not decoration. Under
simple expansion a template matches unreserved characters only, and
every secondary calendar id is an address — so the obvious URI, with the
at sign written as itself, matches nothing and reads back "not found"
while the percent-encoded form works (§18 row 59). Reserved expansion
also matches a slash, so the calendar template matches an event URI too
and the SDK routes to whichever template matches first; both templates
therefore share one handler that parses the URI rather than depending on
registration order.

There is no resource for a schedule, and that is the rule rather than an
omission: a resource takes no arguments, a read must state its window and
its zone (§4.5), and a URI with nowhere to put them would have to invent
both.

### 8a. Every published method, with a verdict

The API-coverage gate of the standard, instantiated. Two files:
`testdata/api-surface.json`, written by `gates api-diff` from the
discovery document and edited by nobody; and
`testdata/api-coverage.tsv`, one hand-written verdict per method. The
offline gate holds three things: a published method with no verdict
fails, a client call with no row fails, and a verdict on a method that no
longer exists fails.

All 38 methods of revision 20260826 are accounted for. 24 used, 14
written off.

| Method | Verdict | Reason |
|---|---|---|
| `acl.delete` | used | `unshare_calendar` |
| `acl.get` | out | `acl.list` returns every rule; a single-rule read has no caller |
| `acl.insert` | used | `share_calendar`, new rule |
| `acl.list` | used | `list_sharing` and `get_calendar`'s exposure |
| `acl.patch` | used | `share_calendar`, role change |
| `acl.update` | out | PUT, replaces the rule whole — §4.4 |
| `acl.watch` | out | needs a public webhook — §1 non-goals |
| `calendarList.delete` | used | `manage_calendar` unsubscribe |
| `calendarList.get` | used | `get_calendar`, per-user overrides |
| `calendarList.insert` | used | `manage_calendar` subscribe |
| `calendarList.list` | used | `list_calendars` |
| `calendarList.patch` | used | `manage_calendar`, colour and visibility |
| `calendarList.update` | out | PUT — §4.4 |
| `calendarList.watch` | out | webhook |
| `calendars.clear` | used | `clear_calendar`, gated |
| `calendars.delete` | used | `delete_calendar`, gated |
| `calendars.get` | used | `get_calendar` |
| `calendars.insert` | used | `create_calendar` |
| `calendars.patch` | used | `manage_calendar`, title and zone |
| `calendars.transferOwnership` | out | needs Workspace admin privilege and `useAdminAccess` — §1 |
| `calendars.update` | out | PUT — §4.4 |
| `channels.stop` | out | webhook |
| `colors.get` | used | `get_settings`; names the colour ids |
| `events.delete` | used | `cancel_event`, whole event |
| `events.get` | used | `get_event` |
| `events.import` | out | migration, not scheduling — §1 |
| `events.insert` | used | `create_event` |
| `events.instances` | used | `list_instances` |
| `events.list` | used | `list_events`, `search_events` |
| `events.move` | used | `move_event` |
| `events.patch` | used | every event write — §4.4 |
| `events.quickAdd` | out | the model parses better and quickAdd hides what it set — §17.4 |
| `events.update` | out | PUT, drops attendees and recurrence — §4.4 |
| `events.watch` | out | webhook |
| `freebusy.query` | used | `check_availability` |
| `settings.get` | out | `settings.list` returns all of them in one request |
| `settings.list` | used | `get_settings`, the user's zone |
| `settings.watch` | out | webhook |

### 8b. Field coverage

Methods are the coarse axis; this API keeps its capability in the `Event`
resource, which has 44 properties. A second gate, `gates api-fields`,
holds one verdict per published field of `Event`, `Calendar`,
`CalendarListEntry` and `AclRule`: modelled in `internal/gcal`, or
written off with a reason. Without it, "we support events" hides the fact
that `attachments`, `extendedProperties`, `gadget` and the four
event-type property blocks were never considered.

Phase 1 writes the record: 81 fields, 56 modelled and 25 written off,
each with a reason. `api-diff` records the field list from the discovery
document alongside the methods, so a field Google adds arrives as a gate
failure naming it. The write path fills verdicts in as the phases reach
them, and a reason of the form `planned=N` names the phase that will.

Phase 0 was supposed to write this record and did not, while
`internal/gcal`'s own doc comment said a gate held the list. Nothing
noticed, because `parity` compares `make check` with CI and both were
equally short.

## 9. Confidentiality, security, safety

Nothing deployer-specific ever enters the repository: no calendar ids or
URLs, account or attendee email addresses, organisation names, Cloud
project ids, OAuth client ids or secrets, and no title, description,
location or guest list from a real calendar. This holds for code, docs,
fixtures, goldens, transcripts, commit and tag messages, pull requests
and logs.

A calendar is worse than a spreadsheet here, and the difference shapes
the gate. **Every event carries other people's email addresses**, and an
attendee list is personal data about people who never consented to this
repository existing. A leak here is not an embarrassment, it is a
disclosure.

### 9.1 What may never enter the repository, and what stops it

Two structural rules, not matters of care:

1. **Fixtures are generated, never recorded.** A fixture copied from a
   live response is itself the leak, whatever a scanner says about it.
2. **The live driver reads only what it wrote**, on a scratch calendar it
   created and **empties at the start of every run** — so the only
   content that can reach a log, a golden or a test failure is content
   the driver invented.

   The emptying is what carries the guarantee, not the deleting. A run
   deletes the calendar afterwards only if it created it; `-keep` leaves
   it, and the next run adopts and empties it instead of making another.
   That is not tidiness, it is §18 row 36: the calendar-creation quota
   counts creations and is not refunded by deletion, so a phase with many
   live runs cannot afford one calendar per run. Event ids are generated
   per run for the same reason — a deleted event does not release its id,
   and Google answers a re-insert with 409.

The leak gate is an allow-list, and it is anchored on shapes the server's
own generated fields cannot take — an `@` with a dot-suffixed domain, a
known URL prefix, a literal keyword before an id — per the standard's
warning about a four-character fixture value colliding with a timestamp.
The gate's exemption list is asserted, so a third entry is an argued
decision rather than a quiet widening.

**Logging.** Method, tool, outcome, duration, a truncated calendar id.
Never: an email address, an event title, description or location, a
search term, or an attendee count in a context that identifies the
event. A search term reaches a log through a request URL, so transport
errors are stripped of their query string before they are logged. A test
fails if any forbidden value appears in a log at any level.

**Destructive tools are unregistered** unless
`GCAL_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true` on
the call. The two behind it are `delete_calendar` and `clear_calendar`.
`clear_calendar` deserves a note: it deletes every event on the user's
**primary** calendar and cannot be undone. It is the single most
destructive call this API offers.

**Where the destructive line falls, and why it is not where it first
looks.** `cancel_event` is not gated. Cancelling a meeting is what a
calendar is for; it is reversible in practice (the event is retained and
visible with `showDeleted`); and gating it would put a flag between the
model and the most ordinary write there is, training people to set
`GCAL_ENABLE_DESTRUCTIVE=true` permanently — which would then also arm
`clear_calendar`. A gate everybody turns on protects nobody. The
protection `cancel_event` gets instead is §4.2's required `scope`, so
cancelling a series can never be a slip of the wrist, and §4.3's required
`notify`.

**Read-only mode** (`GCAL_READONLY=true`) registers only the eight read
tools and requests only the read scopes.

## 10. Auth, config, process model

Settled by the standard's §3b and not re-derived: loopback IP literal on
a random port, `http://127.0.0.1:{port}/callback`, PKCE with S256, no
out-of-band flow, refresh token to the OS keyring with a 0600 file
fallback and a warning. `--no-browser` prints the URL, and the README
carries the SSH port-forwarding section, verified against the binary's
own output.

**Scopes**, generated from the code and gated (the standard's rule that a
scope list a person must paste into a consent screen is an input, not a
description):

- Read-only: `calendar.readonly`, `calendar.settings.readonly`,
  `calendar.acls.readonly`. The third is separate because §2.15 —
  `acl.list` is not covered by `calendar.readonly`, which is the kind of
  thing that surfaces as a 403 from one tool weeks after setup.
- Read-write: the above plus `calendar.events`, `calendar.calendars`,
  `calendar.calendarlist`, `calendar.acls`. Deliberately **not** the
  broad `calendar` scope, which grants everything including the two
  gated tools regardless of the flag.

Config is env plus bound flags, prefix `GCAL_`. The client JSON path is
`--client-secret` / `GCAL_CLIENT_SECRET`, the same name as in every
sibling.

Both halves of every tool result are sent whenever a tool declares an
output schema — `content` as the readable presentation and
`structuredContent` as the machine one, never the same bytes — because a
client shows one or the other and the failure to guard against is the
half that carried the substance being filtered away.

## 11. Reliability

**Retries follow the HTTP method, and the exception is named.** GET,
PUT, DELETE and PATCH are retried with truncated exponential backoff —
`min((2^n) + jitter, 32s)`, which is Google's own documented algorithm.
`freebusy.query` is a POST and is a pure read, so it is retried; the call
site says why.

`events.insert` is the one that cannot be. §2.11 makes it *nearly*
idempotent — the server generates the id — but Google declines to
guarantee collision detection, so an ambiguous failure is reported as
`ambiguous_outcome` with the id it used, so the caller can look rather
than double-book.

`events.move` is a POST that is not idempotent and is not retried.

**`events.patch` IS retried, and the cost is a duplicate notification.**
A patch is a stated end state, so a retry lands in the same place — but
it carries `sendUpdates`, so a retry after a 429 or a 503 can ask Google
to mail the guest list a second time. The alternative is failing a write
that would have succeeded, on a transient error, and leaving the caller
to decide whether it landed. A second invitation is an annoyance; a
write reported failed that actually succeeded is the `ambiguous_outcome`
this design works hardest to avoid. So the retry stays, and this
paragraph exists so it is a decision rather than an accident.

**Rate limiting.** 600 requests per user per minute on a sliding window
(§2.14). The fan-out tools — `search_events` across calendars,
`check_availability` across batches — are where a single tool call can
spend dozens of requests, so they are the ones with a concurrency limit
and a budget stated in the result. A 403 or 429 `usageLimits` becomes
`[rate_limited]` and is retried with backoff; exhausting the retries
reports how many requests the call had already spent.

**Clock skew.** The server's own clock is used only for token expiry and
for resolving a relative window, and in the second case the resolved
absolute window is always printed (§4.5), so a skewed container clock
shows up in the output instead of silently shifting a query.

## 12. Distribution and setup

As the standard's §10 and §10b, with nothing calendar-specific: six
platform archives, `checksums.txt`, an SBOM per archive, a keyless cosign
signature over the checksums, `actions/attest-build-provenance`, built
with `-trimpath` and `mod_timestamp`. A `.mcpb` bundle on every release,
packed in Go from the universal binary's post hook, named in
`checksum.extra_files` and `release.extra_files`, with the manifest
validated against the staged tree by a gate that runs on every commit —
all six referential checks of the standard, each watched failing before
it is believed. A `doctor` subcommand that checks credentials, scopes and
API reachability. The MCP registry entry last.

The one thing to say in the manifest's `long_description`, because it is
the first-run failure: the bundle does not log you in, and cannot — the
user needs their own OAuth Desktop client and one `login` from a
terminal first.

## 13. Testing

- **`internal/gapi/caltest`** — an in-memory Calendar covering calendars,
  events, recurrence expansion, ACL rules and free/busy, generated and
  never recorded (§9.1). It is the bulk of the test surface.
- **Table tests on `internal/when` and `internal/recur`**, and this is
  where the effort goes. Daylight-saving boundaries in both directions,
  the southern hemisphere, a zone with a non-hour offset, a zone whose
  rules changed, all-day events either side of UTC, and a recurrence
  crossing a transition. §4.1 is a claim and these are what make it one
  that can fail.
- **Golden files** for the renderer, so a change to how a schedule reads
  is visible in the diff.
- **A coverage floor of 80% per package**, not an average.
- **`scripts/livecal`** — the live driver, against a calendar it creates
  and deletes, with a step per tool option, held by the `live-cover`
  gate; every print routed through the one redactor, held by the
  `transcript` gate.
- **`scripts/evals`** — tasks a model must complete through the tools,
  scored. The three are the three failures of §3: an all-day event
  created from a user in a negative-offset zone, a weekly recurrence that
  must survive a daylight-saving change, and an invitation that must
  actually reach an external guest. The model is given the server's own
  tool list over an in-memory session against `caltest`, and the score
  reads the CALENDAR rather than the model's account of what it did — a
  model that says it created the all-day event and wrote a timestamp has
  failed. It is run by hand like the live driver, because it costs money
  and is not deterministic; `-self-check` runs everything but the model
  and asserts every task fails on a calendar nobody touched, since a
  scorer that passes there is scoring nothing.

`make check` and CI run the same set, asserted by the `parity` gate:
`fmt`, `vet` (including the tagged tests), `tidy`, `lint`, `cover`,
`vuln`, `licenses`, `secrets`, `api-coverage`, `api-fields`, `classes`,
`evals-check`, `leaks`, `mcpb`, `transcript`, `live-cover`, `parity`,
`pins`, `schema-diff`, `smoke`, `staleness` — twenty-one targets.

Two of those are the deterministic halves of things that are otherwise
run by hand. `mcpb` checks the bundle's manifest against the names the
packer stages, which needs no build. `evals-check` builds each eval
task's calendar, offers the tool list and asserts every task FAILS on a
calendar nobody touched — a scorer that passes there is scoring nothing,
and without this in `check` that would be found by spending money.

`schema-diff` compares the built tool surface against the last tag, and
against `testdata/schema-baseline.json` when there is no tag. The
fallback exists because there was no tag: the gate reported "no previous
tag" on every run from the first commit onwards, which is the whole
stretch where the surface changes most — inert exactly when it was most
needed. `make schema-baseline` records the current surface, and
refreshing it is the deliberate act of saying the change has been looked
at, which is what tagging says at a larger scale. The gate reports and
never fails: removing a tool is sometimes right, and the definition of
done says a person reads this one.

`mcpb`, the bundle manifest gate, is the one this list named before it
existed. It was built in phase 4 with the bundle, and runs in `make
check` and CI because it needs only the staged NAMES, which are static;
the packer beside it needs binaries and runs at release time. Three
gates were named here while absent — `api-fields`, `live-cover` and
`mcpb` — and all three exist now. A list of gates is not a gate, which
is the same mistake, one level up, that this family of checks exists to
catch, and §18 row 62 is the same mistake again on the release
scaffolding.

**Green gates are not done.** Anything touching the write path or a
response shape gets a live run before it counts, and **the transcript is
read** — a sibling's driver twice reported success while its results were
wrong.

## 14. Confirmed decisions and their consequences

Not to be reopened without a reason written into §18.

1. Go, one language, no interpreter in `make check`.
2. Raw REST, own wire types (§4.8).
3. Patch always, PUT never (§4.4) — and therefore four methods written
   off in §8a and a gate holding it.
4. `notify` required, no default, on every write that can reach a person
   (§4.3).
5. `scope` required on every write to a recurring event (§4.2).
6. A date is never an instant (§4.1).
7. Availability from `freebusy.query`, never from a list (§4.6).
8. `cancel_event` is not behind the destructive flag; `delete_calendar`
   and `clear_calendar` are (§9).
9. No webhooks, no local replica, no migration, no quickAdd (§1).
10. Tool names disjoint from the three sibling servers (§1).

## 15. What must be verified live

Spikes, each a question the documentation does not answer, run against a
real account before the phase that depends on it ships. A spike that
prints something looking like an answer to a question it did not ask is
the failure mode here — a sibling's spike did exactly that — so each one
states its question and its verdict separately.

- **Spike A — the notification truth.** §2.6 says some mail is sent even
  with `none`. Which mail? Create an event under each of `none`,
  `externalOnly` and `all`, and record who actually received what.
  §4.3's wording depends on the answer.

  **Three guests, not two, and the third is the one that matters.** This
  spike was written as "an internal and an external guest", which embeds
  the assumption §18 row 40 refutes: `externalOnly` means *non-Google
  Calendar*, not *outside the organisation*. So it needs a guest inside
  the domain, a guest outside it who is still on Google Calendar, and a
  guest who is not on Google Calendar at all — and only the third
  exercises `externalOnly`.

  **No driver can return a verdict here.** Who received mail is visible
  in an inbox and nowhere in the API, so the driver creates the events,
  says exactly what to look for, and the verdict is written into §18 by
  hand. A spike that scored itself green on this would be scoring
  something else.

  **Answered 2026-09-16, over four runs, and the fourth is the one that
  made the first three readable.** Three guests in the end: one inside
  the organiser's domain, one outside it on Gmail, one outside it and
  not on Google Calendar at all.

  - `externalOnly` reached **both** out-of-domain guests and not the
    same-domain one, whatever calendar system they use. The axis is the
    Workspace domain, and the discovery document's "non-Google Calendar
    guests only" is wrong about its own parameter (§18 row 40).
  - `all` reached the same-domain guest and the non-Google guest, and
    never the Gmail guest — across three runs and both orderings. That
    looked like an API behaviour until the non-Google address was put on
    the same events, at which point one send was visible at two
    receivers and the difference turned out to be Gmail (§18 row 42).
  - `none` reached nobody, in a run where every other arm demonstrably
    did, so §2.6's "some mail is sent even with none" does not reproduce
    on insert (§18 row 41).

  **Three consistent runs were consistent because the instrument was.**
  Repetition looked like evidence and was not; what settled it was a
  second receiver on the same event, not a fourth attempt.
- **Spike B — `sendUpdates: none` on insert.** Google warns it can lose
  events (§2.7). Reproduce or fail to reproduce. If it reproduces, §4.3
  may need to refuse `none` on insert outright.

  **Confirmed 2026-09-16, and the mechanism is plainer than the warning.**
  A non-Google address was invited to an event inserted with `none`. It
  received nothing — in a run where the same address had just received
  both `all` and `externalOnly`, so the channel was working. A guest
  outside Google Calendar has no calendar for the event to appear in, so
  mail is the only way they can learn of it, and `none` removes the only
  way. The event exists with them attached and they cannot discover it.

  So §4.3 refuses `none` when a guest is outside the organiser's domain
  rather than warning about it (§18 row 44). The refusal is on the
  domain rather than on the calendar system because the domain is what
  the server can actually tell from an address.
- **Spike C — daylight saving.** A weekly recurrence at 09:00 local,
  spanning a transition, written with a zone and written without one.
  Confirm the drift and confirm its absence. **Both halves answered,
  2026-09-15.** The zoned half holds its wall clock (§18 row 23). The
  unzoned half never drifts, because Google **refuses it**: HTTP 400,
  "Missing time zone definition for start time". §2.2's "required" is
  the API's rule and not the reference page's, so the drift this spike
  was built to reproduce is unreachable through a recurrence.
- **Spike D — all-day events west of UTC.** Create one from a
  negative-offset zone and read it back from a positive-offset one.
  This is §3's first row and the most common defect in the category.
- **Spike E — "this and following".** Build the two-call pattern over a
  series that already has an exception after the target, and confirm the
  exception is reset (§2.8), so §4.2's warning is accurate. **Confirmed,
  2026-09-16.** An eight-occurrence weekly series, the sixth occurrence
  moved half an hour later, split at the fourth: the original kept
  `COUNT=3`, the new series took `COUNT=5`, and the moved occurrence came
  back at its **scheduled** time. The exception was reset, so §4.2's
  warning is accurate and `this_and_following` must say so in its result.
  The split came from `Set.Split` in `internal/recur` rather than from arithmetic
  invented in the spike, so this also holds phase 1's implementation
  against Google — a spike that computes the answer its own way tests
  nothing the write path will do.
- **Spike F — the duplicate insert.** Send the same client-generated id
  twice, concurrently, and see whether the collision is caught (§2.11).
  Determines whether `ambiguous_outcome` is the right class or an
  over-cautious one. **Answered 2026-09-16: the collision WAS caught** —
  one insert returned 200 and the other 409, from two requests in flight
  together. That does not retire `ambiguous_outcome`, and the reason is
  worth stating rather than assuming: §2.11 declines to *guarantee* this,
  so one observation is not a promise; and the class is also for the
  retry after a transport failure, where the caller never saw the first
  answer at all and Google's 409 would arrive for an event the caller
  itself created. The class stays, with one fewer reason to fear it.
- **Spike J — does `events.move` honour `If-Match`?** §4.4 puts every
  write under it, and `events.move` was the one this server could not
  place: a POST with no body, and neither the reference nor the discovery
  document says whether the header applies. The server sent none and said
  so in the result, which is an assumption wearing a disclaimer.

  **Answered 2026-09-16: it is HONOURED.** A probe event was created, its
  etag read, the event patched so that etag went stale, and the move sent
  with the stale one: **412**. So the exception was a hole rather than a
  fact, `move_event` takes an etag now, and §4.4 covers every write
  without qualification.

  The discriminator is the point. A CURRENT etag would have succeeded
  whether the header is honoured or ignored and settled nothing — which
  is the failure this section opens by naming, and which spike I
  committed once already.
- **Spike G — ACL scopes.** Confirm §2.15: that `acl.list` fails under
  `calendar.readonly` alone. It is a scope-set decision and a 403 weeks
  later if wrong. The positive half is answered; the negative half needs
  a profile granted WITHOUT the ACL scopes, and phase 3 finishes the path
  it takes: the login's scope set is derived from the configuration, so

  ```
  GCAL_SHARING=off google-calendar-mcp login --profile=noacl
  make live   # with -profile=noacl
  ```

  produces exactly that grant. The spike reads the token's own scopes
  rather than the recorded list, because a later authorization asking for
  fewer does not revoke what was already given.
- **Spike K — does `calendars.clear` work on a secondary calendar?**
  Google's description is "Clears a primary calendar", and says nothing
  about any other. Three answers are possible and each changes the tool:
  refused, which makes `clear_calendar`'s refusal the API's rule rather
  than this server's caution; cleared, which means the refusal should be
  lifted; or **accepted and did nothing**, a call that reports success
  and leaves every event in place, which is the answer worth finding.

  It runs against the driver's destination calendar with an event it put
  there itself, and counts before and after — a 2xx alone would settle
  nothing, which is spike I's mistake. It is never run against the
  account's primary calendar, and never against the scratch one, which
  holds the events spikes A and B leave for a person to read.

  **Answered 2026-09-16: REFUSED with 400.** So `clear_calendar`'s
  refusal of a secondary calendar is the API's rule rather than this
  server's caution (§18 row 56), and the third possibility — accepted
  and did nothing — did not happen.
- **Spike L — is `If-Match` honoured on the calendar and sharing
  writes?** §2.4 says ETags are supported across this API and §4.4 puts
  every write under one, but spike J showed that "the general rule" and
  "this method" are different questions. The server sends the header on
  `calendars.patch`, `calendars.delete`, `acl.patch` and `acl.delete`;
  this asks whether Google enforces it, with a STALE etag, which is the
  only discriminator that can.

  **Answered 2026-09-16: HONOURED on both.** `calendars.patch` and
  `acl.patch` each refuse a stale etag with **412**, so §4.4 covers these
  writes without qualification (§18 row 57).
- **Spike M — what happens to a conference create request?** §17.3 asked
  three things and this can answer two. Does an insert carrying
  `conferenceData` without `conferenceDataVersion=1` lose it, as the
  discovery document says version 0 does? And does the insert's own
  answer carry the link, or a pending request the caller has to come back
  for? Both change what `create_event` may say.

  The third question — what a domain that FORBIDS conferencing does — is
  **not answerable here**, and the spike says so rather than implying
  otherwise. It needs a calendar whose `allowedConferenceSolutionTypes`
  excludes Meet, and that is set by a domain's policy rather than by
  anything this driver can arrange. The server refuses such a write from
  the published list before it reaches the network, so the untested path
  is a refusal rather than a request; what the spike does instead is
  record what this account's own calendar publishes, which is the input
  that refusal reads.

  **Answered 2026-09-16, and one half went the other way.** Without the
  version parameter Google answered **200, created the event, and
  dropped the conference in silence** — success with the one thing the
  caller asked for missing (§18 row 61). With it, the insert answered
  `status: "success"` with the link already in it, which refutes the
  assumption that a caller usually has to read the event again (§18
  row 60). The pending path stays in the code because Google publishes
  it; it is no longer what the result says to expect.
- **Spike H — free/busy on an unreadable calendar.** Query a calendar the
  user cannot read and confirm the per-calendar error shape §4.6 depends
  on. **Confirmed, 2026-09-15.** The query succeeds; the unreadable
  calendar comes back as its own entry carrying an error, which the
  server reports UNKNOWN with "Do not treat this as free"; and the free
  gaps say they were computed from 1 of 2 calendars. §4.6 stands on the
  shape the API actually returns.
- **Spike I — the 50-calendar ceiling.** Confirm `calendarExpansionMax`
  behaves as documented at 50 and at 51. It goes through the driver's own
  API rather than the tool, because the server batches at 50 and would
  never produce the request the spike is about. **Answered 2026-09-15,
  on the second attempt — and the first attempt is the lesson.** It sent
  one calendar id 51 times. Google keys the response by calendar id, so
  51 copies came back as one entry, and the spike read "fewer than asked
  for" as a silent truncation and reported it as its verdict. It was
  deduplication. That is precisely the failure this section opens by
  naming, committed by a spike written to avoid it, and no gate can
  catch it — only reading the transcript did. The second draft made the
  same mistake one level down: it sent 51 distinct ids and also set
  `calendarExpansionMax` to 50, so a response carrying 50 would have
  been the driver's own cap read as Google's ceiling. With distinct ids
  and no cap, all 51 come back, neither refused nor trimmed. Fifty of them were
  unreadable, so it does not settle 51 readable calendars on its own.
  That half is built and gated behind `-spike-ceiling`, and **it has been
  run: Google refused to create the 39th calendar**, 403 `quotaExceeded`,
  "Calendar usage limits exceeded" (§18 row 36). An account cannot hold
  enough calendars to reach the ceiling, so the readable half is not
  merely unrun but unrunnable, and §2.10 stays unsettled for readable
  calendars by decision. The flag stays, and stays off: the limit counts
  creations and is not refunded by deleting them, so running it spends a
  quota the driver's own scratch calendar needs (§18 rows 12 and 36).

## 16. Delivery phases

Each phase is one session and ends in a tagged release that waits for an
explicit "go". The next session starts from this repository alone.

**Phase 0 — skeleton, gates, time and reading (v0.0.1). Built and run
live 2026-09-15.** Done: the scaffolding, the gates, `internal/when`
with its table tests, the read client and the in-memory Calendar, the
six read tools, both API records, the redactor and the transcript gate,
and `make check` green on Linux across seventeen targets. The live
driver ran twice against a real Workspace account: 19 steps, none
failed. Spike D passed from UTC-10 and UTC+13; the DST assertion passed
on real data. Reading the transcript found the two defects the status
line names, neither of which any test against a fake would have
produced. Outstanding: **spike G's negative half**, and CI has never run
on macOS or Windows. Not tagged: `main` is the maintainer's. The
scaffolding of §12 and §13: Makefile, golangci, govulncheck,
go-licenses, gitleaks, the CI and CodeQL workflows, Dependabot, issue and
PR templates, `CONTRIBUTING.md`. This list said "goreleaser, CI, CodeQL
and release workflows" until phase 5, and two of those four never
existed (§18 row 62); phase 5 built them and the list says what was
built.
`login/logout/status/doctor`; `config`, `credentials`, `userconfig`,
`auth`; `gapi` with the read methods; **`internal/when` complete with the
table tests of §13**, because everything else stands on it; `render` for
the calendar card and the schedule; `caltest` with calendars, events and
free/busy; tools `list_calendars`, `get_calendar`, `list_events`,
`get_event`, `search_events`, `get_settings`; the API surface snapshot
and both coverage records of §8a and §8b; the gates green on three
platforms, including the closed error-class gate, which is built now
rather than later because it costs an afternoon and is the difference
between a vocabulary and a habit. Spikes D and G. A live run whose
transcript is read.

**Phase 1 — recurrence and availability (v0.1.0). Built and run live
2026-09-15.** `internal/recur`: RFC 5545 rules parsed once for the
whole server, expansion that walks dates and carries the wall clock
(§2.2), EXDATE and RDATE, and the three scopes of §4.2 with the
`this_and_following` arithmetic phase 2 will call. `list_instances` and
`check_availability`, taking the read surface to eight tools. The
free-gap arithmetic of §7.3, in `internal/model` beside the busy
intervals it works on. The `expand`/`series` decision was already
built in phase 0.

Phase 1 also repaid two of phase 0's debts, both of them gates this
document listed as done while they did not exist: **`api-fields`** (§8b,
81 verdicts) and **`live-cover`**, which holds every published tool to
having a step in the live driver — it caught both new tools immediately.
`testdata/golden/` was an empty directory; the renderers have golden
files now. Outstanding: **the `mcpb` bundle gate**, which belongs with
the bundle in phase 4, and **spike G's negative half** from phase 0.

Spikes C, H and I have run and are answered in §15. Spike G's negative
half is still owed, and still needs a profile granted without the ACL
scopes; §18 row 24 argues why the cost of leaving it is contained.

**What the live run found, which is the argument for having it.** Three
runs against a real account: 29 steps, and the first run failed one and
was wrong about two more that passed.

The failure was `list_instances` accepting an **occurrence** id as a
series id. The refusal was reactive — it explained the mistake when
Google answered 404 or 400 — and §18 row 31 had recorded, honestly, that
nobody knew what Google actually did. What it does is answer **200 and
expand that occurrence**; a *cancelled* occurrence expands to nothing,
so the call succeeded with an empty list and the tool reported "No
occurrences" for a series that has three. A wrong answer, confidently
phrased, from the reactive design. The server reads the id's shape now:
an event id is base32hex, so an `_` cannot occur in one, and an
occurrence id is refused up front naming the series to use instead.

The second defect passed its step. `list_events` with `no_expand` showed
a row reading `(undated) (no start) (no title)`, and counted it.
`showDeleted=false` does not filter a cancelled **instance** when
`singleEvents` is false — the discovery document says so in the
parameter's own description — and Google sends such an instance bare,
with no start and no summary. The server had passed the parameter and
trusted it. It filters cancelled events itself now. `caltest` had been
hiding them, which is why no test could have caught it, and it
reproduces Google's behaviour now instead.

The third was in a spike, and it is the one worth remembering. **Spike I
reported a verdict it had not established**: it sent one calendar id 51
times, Google deduplicated to a single entry, and the branch reading
"fewer came back than were asked for" announced a silent truncation.
§15's opening paragraph names that exact failure mode, and the spike
written under that warning committed it. Distinct ids give the real
answer, and the spike now states what it cannot settle as well as what
it can.

And a fourth, which is neither a defect in the server nor in a test:
**the transcript printed a dozen of the account's real calendar names.**
§9.1 promises the driver reads only a calendar it created, but
`list_calendars` is account-wide by nature, and the redactor is anchored
on shapes — an address, an id, a URL — while a display name has none. No
pattern could have caught them (§18 row 34).

The first fix for it was a flag on the step, and **it was wrong in the
way §9.1 forbids**: a flag is a matter of care, and the review found the
proof in the same commit — `get_settings` is account-wide too and had
not been marked. What ships instead derives the answer from the step's
own arguments. A step names the calendars it reads; if every one of them
is an id this driver invented, the body can only hold what the driver
wrote, and a step naming none is account-wide by construction. It fails
closed, and on the next run it withheld `get_settings` without anybody
marking it.

**What the phase 1 review found, and what was left.** Thirteen defects,
none of which the test suite caught — every gate was green when the review
started, and the reviews that found them ran the code rather than
reading it.

Six were in `internal/recur`, and they share a shape: the expansion was
right about the hard thing (a wall clock across a transition) and wrong
about the ordinary ones. An `RDATE` was appended after the walk and so
escaped the window; `BYDAY` was walked in the order it was written, so
`WE,MO` and `MO,WE` answered differently; `BYDAY` and `BYMONTHDAY` were
treated as alternatives where RFC 5545 intersects them; a yearly rule
ignored `BYDAY` entirely, which is every "fourth Thursday in November"
holiday; a series ending exactly at the read limit was reported as
endless, in the refusal text §4.2 makes a scope decision from; and
"this and following" counted visible occurrences where a COUNT counts
generated ones, so an excluded date lost an occurrence and an added one
made the two halves overlap. Each is now a test written as the
reproduction that found it.

Two were about paging, and they are the same mistake twice: a page is
the unit Google's token points past. `list_instances` asked for a page
of 250, kept the caller's `max_events`, and handed back the token for
the whole page — losing everything in between while saying the read was
resumable. `list_events` decided truncation from the overflow alone, so
a read that stopped exactly at its budget called itself complete. The
fake was complicit: it ignored `maxResults`, so neither could be
reproduced against it. It honours it now, which is what Google does.

And two in the tool surface: `check_availability` resolved every calendar
reference through `ResolveCalendar`, which re-listed the account's
calendars once per reference because the cache only ever held the list
*without* hidden calendars — and then spent two more failing round trips
per address that was not in it. Asking about 55 colleagues cost 168 HTTP
requests to set up a query the result reported as 2, which is §4.7's
"one tool call is one API request" failing quietly in the direction the
result cannot show. One cached list and no resolution for an address
takes it to 4, and a test now fails if the count creeps back. A second
defect: with every calendar unknown, the text said free time could not
be computed while the structured half still offered the whole window as
free — the two halves of one result disagreeing about exactly the thing
§4.6 exists to prevent. The decision moved into the service, where both
halves read it.

Three smaller ones, all of them things a reader would have seen and a
test did not: `list_instances` and `list_events` had separate tag lists,
so an occurrence never said it was out of office, had no end time set,
or carried a guest list Google had truncated; an `UNTIL` that is an
instant was honoured on a timed series and ignored on an all-day one;
and a free gap crossing midnight rendered as `17:00-09:00`, which reads
as ending before it began.

Four suggestions were considered and not taken, recorded here rather
than lost:

- **One generic expansion driver in `internal/recur`.** `ExpandDates`
  and `ExpandTimes` share the date walker — the part that matters — but
  repeat the loop around it. The duplication had already caused one
  drift, an instant `UNTIL` honoured on the timed path and ignored on
  the all-day one; that is fixed and tested. The generic version is
  worth doing when phase 2 gives the all-day path its first caller,
  which is also when it can be tested against real use.
- **One table-driven record gate.** `api-coverage`, `api-fields` and
  `live-cover` are the same shape three times: a published set, a
  hand-written verdict set, failure in both directions, a reason on
  every write-off, a tally. Collapsing them restructures phase 0's gates
  and phase 1 is not the place. The concrete gap that review found *is*
  fixed: `api-coverage` never implemented the "a client call with no row
  fails" direction this document and CLAUDE.md rule 11 both promised.
- **`live-cover`'s exemptions as a TSV**, like the other two records.
  Kept as a map in the driver's own source deliberately: an exemption
  says why a tool cannot be driven, and it belongs beside the steps it
  is an exception to.
- **`MaxFreeBusyCalendars` as a setting.** §7.3 decided the two ceilings
  differ, not that both should be configurable. One more knob whose
  right value is the API's own limit is a knob nobody should turn.

**Phase 2 — writing events (v0.2.0). Built and run live 2026-09-16.** `internal/plan` — the typed draft, the patch body and the three
guards — plus `create_event`, `update_event`, `cancel_event`,
`move_event` and `respond_to_event`, taking the surface to thirteen
tools. The three declared-but-unemitted error classes are all emitted
now, so `gapi.Planned` is empty and the class gate holds all twelve from
both sides for the first time.

**What the phase decided, beyond building what §7.4 listed.**

- **`gcal.EventPatch`, with every field a pointer.** `Event`'s own
  `omitempty` strings cannot tell "leave it" from "clear it", so a patch
  built from `Event` makes "remove the location" unsayable while looking
  like it worked. A nil field is absent from the JSON; a non-nil field
  pointing at an empty value clears it.
- **An all-day `end` is the LAST day, inclusive, on the way in.** Google's
  wire end is exclusive and the renderer has always shown it inclusive,
  so taking it inclusive here is what makes the value a caller reads the
  value a caller writes. The conversion is in one place and the tool
  descriptions say it.
- **`unsupported` is emitted where THIS server cannot build the
  operation, not where Google is guessed at.** §2.8 says there is no
  server-side "this and following": the only way to have one is to
  truncate the original series and insert a new one, which takes the
  right to rewrite the series. An attendee answering an invitation has no
  such right and a move between calendars has nothing to split, so for
  those two the operation does not exist. That is a fact about this
  construction rather than an unverified claim about the API, which is
  what rule 13 asks for.
- **`Set.Split` was refactored rather than copied** for the all-day path.
  §16's phase-1 note said the generic version was worth doing when phase
  2 gave the all-day path its first caller; it did, so `splitAt` now owns
  the COUNT arithmetic that was wrong twice, and `Split` and `SplitDates`
  are two ways of counting the head. `Reach` and `ReachDates` went the
  same way.
- **`cancel_event` is its own tool Kind.** It registers without
  `GCAL_ENABLE_DESTRUCTIVE` — §9's argument stands — but its annotation
  says destructive, because a client deciding whether to ask a person
  deserves the truthful hint. The flag and the hint are different
  questions and the code now treats them that way.
- **The insert is ambiguous only when the answer is.** A 400 or a 403
  definitely did not land. A transport failure or a 5xx may have, so
  those become `[ambiguous_outcome]` naming the id to read. A
  `this_and_following` write that truncates and then fails to insert gets
  the same class with a sharper sentence: the later occurrences are gone
  until somebody recreates them.

**What the fake was hiding, which is the phase's own lesson.**
`caltest` did not resolve Google's `primary` alias, and nothing noticed
because `Seed` gave a calendar the literal id `primary`. No real account
has one — a primary calendar's id is the account's email address, which
is also what §4.3.5 splits the guest count on. The first write test
against a realistic fixture failed with "no calendar with that id". A
second fixture problem was the same shape: `Seed`'s event ids contain
hyphens, which base32hex forbids, so an occurrence address — series id,
underscore, scheduled start — could not be exercised against it at all.
Both are fixed, and both are the §13 point restated: a fake that is
easier than the API makes a whole path untestable while every test is
green.

**The live run, which is where three more defects came from.** Six runs,
55 steps, and the last of them green. Three of the first run's five
failures were the driver's own: an assertion that greps a whole rendered
page for a date or a clock time can be satisfied by a line it is not
about, and a probe seeded on 19 March at 13:00 satisfied two of them. An
assertion now looks at the rows carrying the event's own title, and at
those with a clock on them when it is asking about a clock. A fourth was
a step cancelling an occurrence the seed had already cancelled.

The fifth was real and is §18 row 50: **Google's `self` flag is not set
on the account's own attendee row on a secondary calendar**, so §4.3.2's
"a write that reaches nobody does not ask" counted the caller as their
own guest and `respond_to_event` was refused for reaching one person —
itself. `model.Event.Guests` takes the account's address now and
excludes it whatever the flag says.

Then the transcript, which is the part §13 insists on, showed three the
count could not. **A successful move reported itself as `[cancelled]`**,
because `events.move` answers with that status while the event sits
confirmed on the destination (§18 row 48) — the one word a caller acts
on, exactly inverted; and a cancellation dry run printed "Deleted the
event" under "DRY RUN — nothing was written", a result contradicting
itself in six lines. And `list_instances` returned its occurrences in
whatever order Google sent them, which is not date order — a cancelled
24 March after 7 April, in the list a caller reads to find out which
dates are gone. They are sorted after the budget cut rather than before
it, so which occurrences come back is unchanged and only the order
differs; sorting first would keep a different set than the page token
accounts for, which is phase 1's defect one tool over. The fake
reproduces the move's answer now, so that one is held offline.

And **spike J closed the question this phase had left open with a
disclaimer.** `move_event` took no etag, because `events.move` is a POST
with no body and nothing Google publishes says `If-Match` applies. A
stale etag is refused with 412 (§18 row 49). The exception was a hole,
not a fact; §4.4 covers every write now.

**And §4.3 was finally exercised live through the tools.** Every write
step above has no guests by construction, and spikes A and B go through
the driver's own REST calls rather than through the server — so the
notification path a caller actually uses had been green offline and
unproven live for the whole phase. Five steps, armed by `-spike-notify`
alongside the spikes because they are the only ones here that reach a
person, now drive `create_event`, `update_event` and `cancel_event` with
a real guest: the invitation, the reschedule, the proper withdrawal, and
§18 row 43 through the tool — a cancellation with `notify: none` that
leaves the meeting on the guest's calendar and reports that it has. The
last one is a deliberate mess: the account cannot withdraw what it
cancelled quietly, which is the finding rather than a side effect, so the
run says at the end who has to delete it.

What those steps CANNOT do is score themselves, and the driver says so
rather than guessing: who received what is visible in an inbox and
nowhere in the API (§15). With only a same-domain guest configured, the
`externalOnly` arm and spike B's non-Google case stay untested — that
needs the other two addresses of §15's spike A.

The write steps need a second scratch calendar for `move_event`, which
spends one more of the creation quota of §18 row 36, once, and is adopted
on every run after.

**What the reviews found, and it is the argument for running them.**
Every gate was green before they started, and `/simplify` and
`/code-review high` between them turned up seventeen defects, two of
which were live bugs in the tool surface.

The two bugs share a shape: **choosing a scope and applying it are two
decisions, and only the first had an owner.** `plan.Scope` parsed the
caller's word, and then each operation wrote out "which event does that
actually mean" by hand. `move_event` had no version of it at all — so
`scope:series` on an occurrence id moved one occurrence while the result
printed `scope: series`, and `scope:instance` on a series id moved the
whole series. `respond_to_event` was missing the refusal, so
`scope:instance` against a series id would have patched the whole
series' attendee array, which is the exact thing its own description
says it exists to prevent; only a fixture with no guests on the parent
hid it. `plan.Target` owns it now, and all four operations go through
one service helper.

Five more that mattered, each fixed with the reproduction as a test:

- **An RFC3339 string was compared as a string.** `end <= start` on
  formatted timestamps, and either side of a daylight-saving fold the two
  carry different offsets — so a valid 45-minute event across the
  Copenhagen fall-back was refused and a genuinely backwards one was
  accepted and sent. §4.1's argument for carrying a zone rather than an
  offset, failing inside the guard written to enforce it. They are
  compared as instants now.
- **`api_requests` was a field incremented by hand**, so it missed
  everything the shared setup spent: a create reported 1 and made 4, and
  a **dry run reported 0 while spending three**. It is read off a counter
  in the context that the client increments, so it counts the retries of
  §11 too and cannot drift. A test asserts reported equals served for
  every write. The read path still hand-counts, and §11 now says so
  rather than a comment implying otherwise.
- **An etag plus `scope:series` was refused every time.** The caller's
  etag came from the occurrence they read; the write was redirected to
  the parent and compared against the parent's. Unrecoverable, too —
  re-reading the occurrence returns the same etag that was just rejected.
  The caller's etag is now checked against the event they addressed, and
  `If-Match` carries the etag of the event actually written.
- **`notify:none` was refused for a colleague** on any secondary
  calendar. Such an event is organised by the CALENDAR, whose id is an
  address with a domain of its own, so every guest counted as outside the
  organisation and §4.3.4 fired with a sentence that was simply false.
  The organiser is now taken as a person only when it is not the calendar
  being written.
- **A 412 on a delete was reported as "already gone".** `classify` maps
  410 and 412 to the same class and the handler matched on the class, so
  a write refused *because somebody had edited the event* told the caller
  their meeting no longer existed. It matches on the status now.

And the smaller ones, which are the ordinary yield: a dry run on update
and cancel printed the event unchanged while the change list said
otherwise — cancel printed it alive under the words "Deleted the event";
the truncate half of a `this_and_following` write sent no `sendUpdates`
while the result said all guests had been notified; the note quoted the
computed rule rather than the one the new series carries; a split
silently dropped the conference link, because Google ignores
`conferenceData` without `conferenceDataVersion=1`; a misspelled `scope`
was accepted in silence on a non-repeating event; a one-sided time change
was never crossed against the end that stays; the live driver could
address the operator's own primary calendar when its second scratch
calendar failed to appear, which §9.1 forbids structurally; and the fake
read its event map outside its own mutex.

The duplication `/simplify` found is worth one line each, because the
cost is always the same: the `EventPatch` fold was written out in the
service AND in the fake, so a field added to one would have made a test
green over behaviour that never happened; "who counts as a guest" — the
rule §4.3.2 refuses on — had a definition in `model` and another in
`plan`, so one result could have quoted two guest counts; the recur
sentinel was hand-wrapped at four sites, two of which stripped the
package prefix and two of which did not; and the dry-run verb was
assigned at eight sites, of which three said "nothing was sent" and five
forgot. Each now has one owner.

**Not taken:** a generic wrapper around the five tool handlers, which
would have saved fifteen lines at the cost of making the write
registrations read differently from the read ones; and dropping the
instance read on a series-scoped write, which costs one request and is
what produces the refusal that tells a caller their `original_start`
named no occurrence.

**Phase 3 — calendars and sharing (v0.3.0). Built 2026-09-16; NOT yet
run live.** `create_calendar`, `manage_calendar`, `list_sharing`,
`share_calendar`, `unshare_calendar`, `delete_calendar` and
`clear_calendar`, taking the surface to twenty. `GCAL_SHARING=off`
removes the three sharing tools, the read among them.

**Four things the discovery document settled before any code was
written**, which is rule 13 working as intended: three of them changed
the tool surface and one of them removed a parameter.

- **`acl.delete` publishes no `sendNotifications`**, and `acl.patch`'s
  own description says there are no notifications on access removal. So
  `unshare_calendar` has no `notify` and says nobody is told (§18 row
  51). A `notify` parameter there would have been a choice with no
  effect, reported as though it had one.
- **The ACL notification is a boolean**, so `external_only` is refused as
  `[unsupported]` (§18 row 52). Rounding it either way emails the wrong
  set of people.
- **`calendars.clear` is documented for the primary calendar and
  `calendars.delete` for a secondary one**, which is where both guards
  come from (§18 row 53).
- **`CalendarNotification.method` has exactly one published value**, so
  `manage_calendar` takes notification TYPES and fills the method in
  rather than offering a choice with one option (§18 row 54).

**What the phase decided.**

- **No `etag` on `manage_calendar`.** §7.5 has the argument: one
  parameter cannot honestly stand for two resources' etags. Each patch
  carries the etag of the read that produced it.
- **`list_sharing` is its own Kind.** It reads, so its annotations say
  read-only; `GCAL_SHARING=off` removes it with the other two, because a
  deployment that turned sharing off turned off the surface rather than
  the writes. Read-only mode drops it too, which keeps §8's "the first
  eight" true rather than "the first eight plus a judgement call".
- **`confirm` is not required in the tool schema**, on either gated
  tool. The SDK validates a required field before the handler runs, so
  the caller would have received "missing properties: [confirm]" instead
  of the sentence naming what would be destroyed — which is the whole
  point of asking. `notify` is schema-optional for the same reason.
- **Access is checked before the confirmation.** "Pass confirm" is
  useless advice to somebody whose access would have refused the call
  anyway.
- **Two role thresholds, each where the evidence puts it.** Sharing
  needs owner, because Google's role description says owner is the one
  that can "modify access levels of other users". Changing the calendar
  itself needs writer, which is the strongest claim the documentation
  supports: a reader plainly cannot, and whether a writer may rename a
  calendar is not documented either way, so that half is left to Google
  to answer rather than guessed at here. The first draft used owner for
  both, and `unparam` caught it — a linter finding an unargued
  constraint.
- **The per-user half needs no access at all.** The colour, the name you
  give a calendar and what you are emailed about work on a calendar you
  can only read, so `prepare`'s write guard would have been wrong here.
- **A write that changes the calendar list drops the cached list.** The
  cache is what keeps a resolution from costing a request (§7.1), and a
  stale one answers the next tool call with a name that no longer
  exists.
- **The exposure after a share is computed, not re-read.** The rule
  Google returned is folded into the list that was read before it, so
  "after" is what Google stored rather than what was asked for, and the
  result costs one request rather than two.

**What the reviews found, and it is the argument for running them.**
`make check` was green before they started. `/simplify`'s four passes and
the phase's own re-read turned up six things that mattered, three of them
defects a caller would have met:

- **A rename wiped the caller's own name for the calendar.**
  `mergeCalendar` wrote a second copy of `model.FromCalendarList`'s
  rename rule and inverted it, so renaming a shared calendar you had
  renamed for yourself printed `Team planning (you renamed this; others
  see "Team planning")` — a line that contradicts itself, with the user's
  own name gone. One rule, one owner: the merge defers to the model's.
- **`model.Calendar.ETag` stood for two resources.** It held the
  subscription's etag when the calendar came from the list, which is
  nearly always, and the calendar's when it came from `calendars.get` —
  so `calendars.delete` was sent the wrong one and would have been
  refused **on every call**, reported to the caller as somebody else's
  edit. The field is two fields now, each set only by the read that
  produced it, and the fake gives a calendar and its subscription
  different etags so the mistake fails a test instead of passing one.
  The three writes with no read of their own make one, because these
  tools have no `force` and a cached etag would be a `[stale]` nobody
  could get past.
- **A calendar created in a session could not be found by its name.**
  `create_calendar` left the cached calendar list untouched, so
  resolving the calendar by the title the same call had just given it
  answered "no calendar called that" — for the life of the process, with
  no way for the caller to recover. The cache is corrected by every write
  now rather than emptied, which also stopped each write making the next
  tool call pay for a whole list read.
- **The result told callers to pass an etag no tool accepts.** The line
  was copied from the event renderer, where `update_event` takes the
  value back. Removed rather than special-cased: the first fix was a
  verb-exception table, which is the same mistake one level up.
- **Two notification sentences were written in the service**, and both
  made the flat promise "Nobody was emailed" that `plan.ShareReport`
  exists to avoid making (§2.6). All four now come from one function that
  takes the outcome.
- **A failure after a landed change said nothing about it.**
  `manage_calendar` can be two patches; the second failing left the first
  in place and returned a bare error, so a caller could not tell that
  half the write was done and a retry would redo it. There is no
  rollback to offer, so the refusal names what already stands.

**And one in the live driver, which had not run yet.** Every phase 3 step
addressed the probe calendar through arguments built at run time — and
when `create_calendar` failed, those arguments were EMPTY. A tool call
naming no calendar resolves to the account's **primary** one, so a run on
an account whose calendar-creation quota was spent (§18 row 36, an
ordinary occurrence) would have pointed `manage_calendar`,
`unshare_calendar` and `clear_calendar` at the operator's own calendar.
The confirmation guard would have caught the last one, which is luck
rather than design. A step now declares why it cannot run and is skipped
before the call, which is §9.1's "structural rather than careful" applied
to writing rather than to printing.

**And what `/code-review high` found afterwards**, on a tree where every
gate was green and `/simplify` had already run. Seven, of which four were
defects a caller would have met and one was a regression the cleanup
itself had introduced:

- **The cache fix had made the calendar list a data race.** Keeping the
  cached list correct rather than dropping it meant editing it in place —
  and `allCalendars` hands that slice to its callers without copying,
  while the SDK serves every tool call in its own goroutine. A
  `list_events` resolving a calendar could read the array a
  `manage_calendar` was compacting, and a compaction mid-iteration can
  make a resolution miss a calendar or see one twice. The mutators build
  a new slice and swap it, which is the invariant the whole-list drop had
  for free and the cleanup took away.
- **A dry run of "subscribe and set my colour" failed outright**, with
  advice the caller had already followed: the entry does not exist yet,
  so reading it answered 404 and the refusal said to pass
  `subscribe: true`. The dry run supplies the entry it would be patching.
- **A failed dry run reported a change as standing.** The partial-write
  sentence did not check `dry_run`, so a dry run that stopped half way
  said the subscription was in place and could not be rolled back.
- **A dry run's two halves disagreed.** The calendar block was replaced
  by the subscription read from before the calendar patch, so the header
  showed the old title above a change list saying the title changed —
  the same shape of contradiction phase 2 found on a cancellation, in a
  different result.
- **A plain 403 was reported as a missing scope.** `acl.list` needs owner
  access, and `classify` already separates the two — a missing scope is
  `[auth]`, a refusal is `[forbidden]` — so somebody who simply does not
  own the calendar was told to log in again, which cannot help.
- **The notify refusal offered a choice it then refused.** The empty case
  fell through to the event vocabulary, which lists `external_only`; a
  caller following the list would be refused twice.
- **And the two etag fields were dead.** Splitting them made the misuse
  impossible and left nothing reading either, because a write cannot use
  a cached etag anyway — these tools have no `force`, so a version
  minutes old is a `[stale]` with no way past it. `model.Calendar` has no
  etag at all now, and each write holds the one it read as a local.

**Considered and not taken**, recorded rather than lost:

- **Splitting `tools.Kind` into an effect and a gate.** The enum encodes
  both what a tool does to the world, which decides its annotations, and
  which flag it registers behind — and two of its seven values exist only
  because those disagree (`Cancelling`, `SharingRead`). `Def{Effect,
  Gate}` would remove the vocabulary, and the argument for doing it now
  rather than at three values is a fair one. Not in this phase: it
  restructures phase 0's registration at the end of a long phase, and the
  two switches are held by tests that assert every combination.
- **The structured results' round trip.** `CalendarDetail` holds
  `[]model.Sharing`, converts it to the output rows, and the renderer
  converts back so it can recompute two derived strings.
  `CalendarsResult` has done the same since phase 0. The new results take
  the other convention — they carry rendered text — and unifying them is
  worth doing, but it moves phase 0's own tests and is not phase 3's to
  do.

**The live run, which found the thing no document contains.** 77 steps,
two failures, both from the data-owner rule of §18 row 55. The calendar
steps create a probe calendar through `create_calendar`, rename it, set
the per-user overrides, meet the unsubscribe refusal, share it with an
address in a domain that cannot resolve, change that rule's role,
unshare it, and delete it through `delete_calendar` — having first been
refused for want of `confirm`. Everything else passed first time,
including the guards that never reach Google.

**What the run could NOT exercise, said here rather than left implied.**
Subscribing to a calendar this account does not own is the one path in
phase 3 with no live coverage: `calendarList.insert` is only reachable
for somebody else's calendar, and §9.1 forbids the driver reading past
what it wrote. It is held against the fake, and the live step holds the
"already subscribed" half instead.

The probe calendar costs one creation from the quota of §18 row 36 per
run, which is the price of driving `create_calendar` and
`delete_calendar` at all, and a run that stops early leaves it behind —
so the driver sweeps it on the way out.

**Phase 4 — the model's experience (v1.0.0). Built and run live
2026-09-16.** The three resources of §8; the working-hours mask, the
conference parameter and the attendee warning §17 was holding;
`scripts/evals` with the three tasks of §3; and `packaging/mcpb` with
the bundle gate §13 named before it existed. The surface is twenty tools
and three resources; `make check` is green across twenty-one targets;
the live run is **85 steps, none failed**, 7 undetermined by default,
being the five that reach a real person and spikes A and B.

**Reading the transcript found what the count did not**, as it has in
every phase. `check_availability` printed `busy 2026-03-20 00:00-00:00`
for an all-day event — a block of no length, on the one kind of event
that occupies the whole day. Free/busy reports such an event midnight to
midnight and the busy list printed the end time without its date, while
the free gaps two lines below already carried that rule. Every gate was
green, the step passed, and only the body showed it.

**What spike M cost and taught, which is the phase's own lesson.** It
asked two things about a conference create request and both mattered.
Without `conferenceDataVersion=1` Google answers **200, creates the
event and drops the conference in silence** — the exact shape this
server exists to refuse, and the reason the client sets that parameter
from the body rather than trusting twenty call sites to remember it
(§18 row 61). And the insert's own answer carried the link, with
`status: "success"`, which **refutes what this phase had written
everywhere**: the tool description, the result note and the field
comments all said a caller usually has to read the event again. They say
the opposite now, and the pending path stays in the code because Google
publishes it as a status — one observation retires an expectation, not a
possibility (§18 row 60). The spike also said plainly what it could not
settle: a domain that forbids conferencing needs a calendar this account
cannot be made to have.

**The URI templates, which no test would have caught.** A resource
template written the obvious way — `gcal://calendars/{calendar_id}` —
matches unreserved characters only, and every secondary calendar id is
an address. A model pasting the id `list_calendars` just gave it would
have got "not found" from every calendar but the primary. Reserved
expansion fixes it and brings its own consequence: `{+calendar_id}`
matches a slash too, so the calendar template also matches an event URI
and the SDK routes to whichever template matches first. Both templates
share one handler that reads the URI, rather than resting on
registration order (§18 row 59).

**What the bundle is and is not.** `packaging/mcpb` has the manifest,
the Linux launcher, a packer in Go and the `mcpb` gate, with all six
referential checks of the standard watched failing in tests. What it
does not have is a release to ship in: **§16 listed goreleaser and a
release workflow among phase 0's work and neither ever existed**
(§18 row 62). So the bundle is packed by `make mcpb-pack` from a built
tree and installed by hand, and the signed-release wiring —
`checksum.extra_files`, `release.extra_files`, the universal binary's
post hook — is owed rather than done.

**What `/code-review high` found, on a tree where every gate was green
and `/simplify` had already run.** Twelve, of which eleven are fixed and
one is written down.

- **`manage_calendar` reported a change that had not been made.** Both
  halves appended to the change list BEFORE their API call, and the
  partial-write sentence — "that change stands, there is no rollback" —
  is built from that list. A rename refused with 412 told the caller it
  had landed. The list is what the call DID now, appended after the
  write.
- **`get_calendar` refused the whole card for a calendar somebody else
  owns.** `acl.list` needs owner access; a plain 403 is `[forbidden]`
  and only `[auth]` was being turned into a note, so the tool answered
  nothing at all for a colleague's calendar while its own doc comment
  promised the opposite. Both now carry the card and say which sentence
  applies.
- **The crowd warning counted the wrong population.** Google's threshold
  is on its own `attendees` field, which carries the organiser's row and
  the rooms; counting "guests" stayed silent on an event Google had
  already stopped tracking, and disagreed with the `Guests (202)` the
  same result printed.
- **A dry run reported a conference state it had invented.** `After` was
  built from the request body, so the create request read back as "a
  conference with no video link" — a real state, and not this one.
- **A forced `cancel_event` did not say it was forced.** `update_event`
  and `move_event` carry §4.4's sentence and the other two did not,
  including the write where what is overwritten is gone.
- **`gcal://calendars/primary/events/` answered with the CALENDAR.** An
  empty event segment fell through to the calendar card, sharing list
  and all: a different resource from the one the URI names, with no
  error.
- And five smaller ones: the driver's gap assertion could not see a
  cross-midnight row, which is precisely what a mask that failed to
  apply produces; `caltest`'s read path mutated shared state under a
  lock that protected nothing a reader sees; spike M reused one request
  id across both halves, when a repeated id is itself a variable; a dry
  run printed the user's PREVIOUS private name as the one others see;
  and a field verdict still said §17.2 was open.

**Outstanding, and named rather than implied.** A second MCP client and
the bundle exercised from a real install are deferred by decision: both
need a desktop this session did not have. Spike G's negative half is
still owed from phase 0. §17.1, incremental sync, stays open. And §18
row 63 records what the review found in phase 2's split arithmetic: the
expansion runs in the zone the CALLER asked to see, and two attempts to
make that produce a wrong rule did not.

**Phase 5 — the release (v1.0.0). Built and rehearsed 2026-09-16.**
`.goreleaser.yaml` and `.github/workflows/release.yml`, which §12
specified and §16 listed among phase 0's work while neither existed
(§18 row 62). Six platform archives, `checksums.txt`, an SBOM per
archive, a keyless cosign signature over the checksums, and
`actions/attest-build-provenance` over every published file; `-trimpath`
and a `mod_timestamp` from the commit, so rebuilding a tag reproduces it.
The bundle phase 4 built is packed in the universal binary's post hook
and named in both `checksum.extra_files` and `release.extra_files`. The
surface is unchanged; `make check` is twenty-two targets.

**The gate is the point, not the config.** A release config is read once
and then trusted, and the half that binds it to this repository — the
staged globs, the pack path, the bundle reaching `checksums.txt` — is
exactly the half nothing would notice being wrong until a tag. So
`gates release` holds it on every commit: each staged glob must resolve
to exactly ONE directory the build matrix produces, the post hook must
pack to the path `MCPB_OUT` names, the bundle must be both checksummed
and uploaded, the archives must exclude the universal binary, and the
signature must pass `--bundle`. Twenty-nine ways of breaking it are watched
failing in tests, as §10b asks for the manifest.

**What the rehearsal found, which reading could not.** The packer's macOS
glob was `dist/*darwin*universal*/` and goreleaser writes
`dist/<id>_darwin_all/`, so it matched nothing and `mcpb-pack` would have
failed on the one file the macOS half of the bundle is (§18 row 64). Two
more came from checking §10b's five version places rather than assuming
them: the bundle's filename carried no version at all, and the binary
inside it reported `v0.0.0` while the manifest beside it reported the
snapshot version — the ldflags stamped `{{ .Tag }}`, and a snapshot has
no tag (§18 row 65). Both now come from `{{ .Version }}`, and a snapshot
agrees in all five places.

**What is deliberately not here.** The MCP registry entry, which §12
puts last: it needs a `server.json`, a namespace and a publisher
identity, and the registry does a HEAD on the bundle's download URL
before it accepts an entry, so it cannot be built before a release
exists. And the tag itself — signing and provenance need an OIDC token
that only a real workflow run has, so `goreleaser check` and a full local
snapshot both pass while that step is untested. The standard says to
watch the first run after any change to it, which is what is owed next.

**Phase 6 — incremental sync (v1.0.0). Built 2026-09-17.** `list_changes`,
which closes §17.1, the last open decision. The surface is **twenty-one
tools and three resources**; the read block is nine, and `GCAL_READONLY`
keeps it.

**It exists for one thing a list cannot do: report a deletion.** A
deleted event stops matching a window, so its absence from `list_events`
is indistinguishable from never having existed. Sync answers with a
cancelled tombstone. Everything else the tool does — a token in, a token
out — is in service of that.

**The caller holds the token, and the server stores nothing.** §1 says no
replica, and storing a token would make the server stateful about *since
when?*, a question belonging to whoever asked rather than to the process.
The contract is a page token's: opaque, handed back, passed in.

**What the discovery document settled, and what it cost.** `showDeleted`
may not be false alongside a token, so the read forces it true — which
means a BASELINE also picks up whatever is already cancelled, and those
are labelled "already cancelled" rather than "deleted", because there was
no "since" yet. A window, a search, an ordering and `updatedMin` are all
refused alongside a token, so the tool offers none of them. And
`nextSyncToken` arrives on the **last page only**, which collides with
§4.7's budget: a read that stops early has no token, and handing one over
would skip every page it never read. The result says so in words (§18
row 72).

**The invalidation story §17.1 was waiting for.** 410 was already mapped
to `[stale]` in phase 0, with a comment naming this section. What phase 0
could not know is that `[stale]`'s generic advice — read again and retry
— is wrong here and loops. The tool replaces the message: ask again with
no token, and treat what you were holding as unreliable rather than old.

### 16a. Found by review, and fixed

Five defects the phase 1 review turned up in code phases 0 and 1 had
already committed. All five are fixed; each is listed with what it was,
because the shape of the mistake is the useful part.

1. **Multi-calendar paging handed one calendar's token to every
   calendar.** `ListEvents` fans out, and a Google page token is scoped
   to one calendar and one query — but the fan-out kept whichever
   calendar produced a token last and passed that one back to all of
   them, discarding the rest. Continuing a truncated two-calendar read
   resumed the wrong calendar from an unrelated offset and lost the
   others' pages, while the result said "Pass page_token to continue."
   `next_page_token` is now a cursor carrying **one token per calendar**,
   naming only the calendars with more to read, so a continuation never
   re-reads a calendar that finished. It stays one opaque string, so no
   tool schema changed.

   Fixing it exposed a second defect underneath, which is why the first
   attempt still lost events: the budget was applied **twice**, once per
   calendar and again to the combined list. The second cut threw away
   events whose page token had already moved past them — unreachable
   from the cursor the caller was handed. The budget is divided across
   the calendars up front now, and nothing fetched is discarded.
2. **`search_events` advertised a `page_token` it did not accept.** Its
   input had no such field, but it returns the schedule result, which
   renders the sentence and carries the token. It takes one now, which
   1 made worth having.
3. **An invalid time zone was classified `[unavailable]`, which is
   retryable.** `Service.Zone` returned `internal/when`'s error
   unwrapped, so it never became a `gapi.Error` and fell through to the
   default class: a caller passing a zone that does not exist was told to
   retry a request that could never succeed. It is `[invalid]` now, as
   `s.window` already was, and the live driver holds the class.
4. **`sortKey` mixed UTC and local.** Timed events sorted by their UTC
   instant and all-day events by their local date, while the renderer
   groups by local date. East of UTC the two disagree — in Asia/Tokyo an
   08:00 event carries a key on the previous day — so an all-day event
   landed in the middle of its own day instead of at the front, which is
   what the function's own comment promised. The golden fixture is
   Europe/Copenhagen with the all-day event alone on its day, so it
   could not catch it.
5. **The event-id rule lived in two places.** `validEventID` was in the
   live driver and the occurrence-id grammar was in `internal/service`,
   each depending on the other's premise and neither owning it.
   `gcal.ValidEventID` and `gcal.SplitOccurrenceID` own both now, beside
   the wire types they describe, which is also where §2.11's
   client-supplied id on insert will need them in phase 2. The grammar
   is a grammar and not a policy: `get_event` still accepts an
   occurrence id, because that is how one occurrence is addressed.

## 17. Open decisions

1. **Incremental sync.** `syncToken` is designed for a client with a
   replica and this server has none (§1). But a "what changed since I
   last looked" tool is genuinely useful and the token is the only
   correct way to build it. Deferred to a phase that can carry the
   invalidation story (410, §2) honestly.

   **Decided in phase 6: `list_changes`, and the caller holds the
   token.** The server stores nothing. The token goes back to whoever
   asked and comes in on the next call, exactly as a page token does —
   it is Google's to mint and opaque to everything here. Storing it
   would make the server stateful about a question whose answer belongs
   to the asker: *since when?* Two people sharing one server do not
   share a "last looked".

   **Why it earns a tool rather than a flag on `list_events`.** A list
   cannot report a DELETION. A deleted event stops matching the window,
   so it is absent from the answer in exactly the way an event that was
   never there is absent, and no caller can tell those apart. Sync
   returns a cancelled tombstone, and that is the whole difference.

   Four rules come from the discovery document (revision 20260826) and
   each is enforced before the call rather than left to Google's 400:
   deletions are always included and **`showDeleted` may not be false**,
   so the read forces it true; a window, a search, an ordering or
   `updatedMin` cannot accompany a token, so the tool **offers none of
   them**; the other parameters must match the initial read, which is
   why a baseline uses the same `showDeleted` and therefore picks up
   what is ALREADY cancelled — labelled as that rather than as a
   deletion, because there was no "since" yet; and **`nextSyncToken`
   arrives on the last page only**.

   That last one is the interesting one, because it collides with §4.7.
   A read that stops at its budget has **no token**, and handing one
   over anyway would skip every page it never read. So an unfinished
   result says, in the text, that there is no token and why. §18 row 72.

   **The invalidation story, which is what §17.1 was waiting for.** An
   expired token is 410, which phase 0 already mapped to `[stale]`. The
   generic message there — "read again and retry" — is wrong here and
   would send the caller round the same loop, so the tool replaces it:
   ask again with **no** token, and treat what you were holding as
   unreliable rather than merely old. **Decided.**
2. **Working hours.** There is no working-hours field in the API; the
   `workingLocation` event type is adjacent but not the same thing. Free
   gaps at 03:00 are technically correct and useless. Whether the server
   takes a working-window parameter, or leaves the filtering to the
   model, is undecided. Leaning toward a parameter with no default, on
   §4.3's reasoning. **Open.**

   Phase 1 shipped `check_availability` without one, and the argument
   that should decide it is now clear. **A window is one interval;
   working hours are a daily mask.** Over a single day the two are the
   same thing and the model can narrow the window itself. Over "next
   week" they are not: the model cannot express 09:00–17:00 on five days
   as one window, so it either accepts a fifteen-hour overnight gap in
   the answer or makes five calls, which is five requests against §11's
   budget. `min_minutes` does not help — an overnight gap is the longest
   one in the list and passes any minimum.

   Adding an optional parameter later is additive, so shipping without
   one blocks nothing. The decision is which of the two the server
   should own, and the daily-mask point is the one to decide it on.

   **Decided in phase 4: the server owns it**, on exactly that argument.
   `check_availability` takes `working_from`, `working_to` and
   `working_days`, and none of them has a default — without them every
   hour of the window counts, as before. Days left out means EVERY day
   rather than Monday to Friday: a five-day default would be the server
   guessing somebody's working week, and it is wrong in every country
   whose week runs Sunday to Thursday. A mask crossing midnight is
   refused rather than guessed at, because the day a night shift belongs
   to is a choice this server would be making for somebody. The mask is
   applied in the zone the answer is rendered in, day by local day, so
   09:00 is still 09:00 on the Sunday the clocks change — the same rule
   as §4.1, one level up from an event — and both halves of the result
   say which mask was used, because a gap list with the evenings
   silently removed reads as "nobody is free then". **Decided.**
3. **Conferencing.** Creating a Meet link is a `conferenceData`
   `createRequest` with a caller-generated `requestId`, and it is in
   scope by §1's boundary. Whether it is a parameter on `create_event` or
   its own tool, and what happens when the domain forbids it, was
   undecided.

   **Decided in phase 4: a parameter on `create_event`, and nothing
   else.** A tool would be a second way to make an event, and the link
   belongs to the event's creation rather than to a separate act.
   `conference: true` sends a create request whose `requestId` is the
   EVENT id, so a retry of a create whose answer was never seen (§2.11)
   cannot make a second conference — Google ignores a repeated request
   id. The version parameter is set by the client from the body rather
   than by each caller, because without it Google drops the conference in
   silence (§18 row 61).

   Three consequences, each stated where a caller meets it. The result
   reports what came BACK — the link, or that Google is still making it —
   never what was asked for; spike M found the link usually arrives with
   the insert, so "read it again" is the other answer rather than the
   expected one (§18 row 60). A calendar whose published
   `conferenceProperties` exclude Meet is refused BEFORE the write,
   because Google's own answer there is a 200 with a failed request and
   an event that exists; an absent list is not a refusal. And attaching a
   conference to an event that already exists is refused with what to do
   instead, rather than silently dropped: `events.patch` can carry
   conference data and this server does not write it, so saying so is the
   honest half. **Decided, with the boundary named.**
4. **quickAdd, argued against.** §1 writes it off and the counter-argument
   is recorded rather than lost: it is one call where the structured path
   is several, and users type strings like that. It stays out because it
   gives no control over the zone — which is §4.1, the thing this server
   exists to get right — and because its result does not say what it
   parsed. Revisit only with a spike showing what it does with an
   ambiguous zone. **Decided, with the argument kept.**
5. **Attendee limits.** §2 notes that response status is not propagated
   above 200 guests. Whether the server warns above that threshold, and
   what it does at the hard limit, needed a number the documentation does
   not state.

   **Decided in phase 4: warn at the documented threshold, and say what
   is not known.** Any result describing an event with more than 200
   guests reports that its RSVPs are incomplete — above that Google stops
   propagating individual responses, so a caller counting acceptances is
   wrong with nothing looking wrong. The warning is derived from the
   event the write PRODUCED rather than added by whichever write grew the
   guest list, so every write that can leave an event in that state says
   so, including the ones that did not add anybody. It carries the count
   and never the addresses (§9).

   The hard limit is left alone and said out loud: Google does not
   publish where it stops accepting guests, so the warning says that
   rather than inventing a number, and no spike is run to find it — one
   would mean putting several hundred invented addresses on a real
   event. **Decided.**

## 18. Evidence log: conventions checked, changed, or rejected

Sources: the Calendar API v3 discovery document (revision 20260826,
fetched 2026-09-15), the Calendar guides for events, recurrence, sync and
quotas, RFC 5545, RFC 8252, the MCP specification, and the public
calendar MCP servers of §1. Checked 2026-09-15.

**Three tiers, and the plan says which is which rather than letting them
blur.** (1) Verified here against a primary source. (2) Taken from a
sibling server's own evidence log, which was verified there. (3)
Asserted from the documentation and **not yet probed live** — these are
what §15 exists to settle, and they are marked.

| # | Convention or assumption | How checked | Verdict |
|---|---|---|---|
| 1 | Default `sendUpdates` to `none` everywhere, as the sibling Drive server does for mail | Discovery document, `events.insert` enum description | **Refuted.** Google warns `none` "can have significant adverse effects, including events not syncing to external calendars or events being lost altogether". §4.3 was rewritten around this; it is the largest change any evidence made to this document |
| 2 | Google's own defaults are a coherent baseline to adopt | Discovery document, across methods | **Refuted.** Events default to not notifying; `acl.insert/patch/update` default `sendNotifications` to **true**. Same API, opposite defaults. §4.3 takes neither |
| 3 | `none` means no mail is sent | Discovery document, every affected method | **Refuted.** "Some emails might still be sent even if you set the value to false." §4.3.3 forbids the server from promising silence |
| 4 | `calendar.readonly` covers every read | Discovery document, per-method scopes | **Refuted.** `acl.list` requires `calendar.acls` or `calendar.acls.readonly`; `acl.get` is covered. §10 requests the extra scope; spike G confirms live |
| 5 | `events.update` is the method for editing an event | Discovery document and the events reference | **Rejected for use.** PUT, replaces whole; drops attendees and recurrence when omitted. §4.4, and §8a writes it off by name |
| 6 | ETags and `If-Match` are available for optimistic concurrency | Calendar version-resources guide | **Confirmed.** 412 on mismatch; `If-Match: *` forces through. §4.4 is buildable as the standard states it, with no deviation recorded |
| 7 | "This and following" is a single operation | Recurring-events guide | **Refuted.** Two calls, and it "resets any exceptions happening after the target instance". §4.2 performs it and says so |
| 8 | `timeZone` is optional on an event's start and end | Discovery document, `EventDateTime.timeZone` | **Refuted for recurrence.** "For recurring events this field is required." §4.1 sends it always |
| 9 | An all-day event can be modelled as midnight UTC | The surveyed servers, and an issue reproducing it | **Refuted.** It renders a day early for every user west of UTC. §4.1 keeps dates as dates; spike D confirms live |
| 10 | A client may supply an event id, making insert idempotent | Discovery document, `Event.id` | **Confirmed, with a caveat that changes the design.** Ids are allowed, but "we cannot guarantee that ID collisions will be detected at event creation time". Nearly idempotent only — hence `ambiguous_outcome` in §6.5 and spike F |
| 11 | `singleEvents` is a display preference | Discovery document and the events reference | **Refuted.** It changes what the method returns — parents or instances — and `orderBy: startTime` requires it. §7.2 makes the caller choose |
| 12 | `freebusy.query` handles any number of calendars | Discovery document, `calendarExpansionMax`; **spike I live, 2026-09-15** | **Confirmed with a limit, and the limit does not do what the batching assumed.** Maximum documented value 50, and §4.6 batches there. Live, 51 *distinct* ids with **no expansion cap set** came back as **51 entries** — neither refused nor trimmed. The cap is deliberately omitted: a spike that sets the ceiling it is measuring reports its own parameter as Google's behaviour. Fifty were unreadable, so this does not settle 51 *readable* calendars: the ceiling may count only the calendars it expands, and proving that would mean creating 51 calendars on somebody's account. The batching stays, and §4.6's rule that a calendar missing from the response is **unknown** is what the design actually rests on |
| 13 | Availability can be computed from an event list | The surveyed servers | **Rejected.** A list misses events whose details the caller cannot read, and ignores `transparency`. §4.6 |
| 14 | `calendars.transferOwnership` is a normal calendar operation | Discovery document | **Rejected for use.** Requires Workspace admin privilege and `useAdminAccess: true`, which this server never requests. §8a |
| 15 | Push notifications could give live updates | Discovery document, the four `watch` methods | **Rejected.** They POST to a public HTTPS endpoint; a stdio server has none. §1 |
| 16 | Quotas are generous enough to ignore in fan-out tools | Quota guide | **Refuted.** 600 requests per user per minute, sliding window. `search_events` and `check_availability` can spend dozens per call. §11 budgets them |
| 17 | Truncated exponential backoff, `min((2^n)+jitter, 32s)` | Quota guide | **Confirmed**, and it is Google's own stated algorithm. §11 |
| 18 | Loopback IP literal, random port, PKCE S256, no OOB | The shared standard §3b, itself from RFC 8252 §7.3 and §8.1 | **Adopted unchanged.** Tier 2: verified in the siblings, not re-derived here |
| 19 | Both `content` and `structuredContent` on every tool with an output schema | The shared standard §2, from the MCP spec and a client bug | **Adopted unchanged.** Tier 2 |
| 20 | Destructive tools unregistered behind an env flag | The shared standard §3 | **Adopted, with the line drawn differently.** `cancel_event` is deliberately *not* gated; §9 argues why a flag everybody turns on protects nobody |
| 21 | An event id may use any lowercase letter | Live probe, 2026-09-15 | **Refuted, and it cost a run.** Ids are base32hex: `a`–`v` and the digits only, so `w`, `x`, `y` and `z` are refused. The driver's ids used `y` and Google answered HTTP 400 "Invalid resource id value", naming neither the field nor the rule. `validEventID` in the driver now holds it locally, so a bad id is refused before a request is built |
| 22 | An all-day event stays on its date when read from any zone (§4.1) | **Spike D, live, 2026-09-15** | **Confirmed.** An all-day event on 2026-03-20 read back from Pacific/Honolulu (UTC-10) and Pacific/Auckland (UTC+13) rendered `2026-03-20  all day` in both, with no time of day. This is the defect every surveyed server has, and it is the reason `internal/when` has no `Date` → `Zoned` conversion |
| 23 | A recurrence written with an IANA zone holds its wall clock across a transition (§2.2) | **Live, 2026-09-15** | **Confirmed, and the transcript shows both halves.** A weekly 14:00 series spanning the 29 March European transition rendered 14:00 on 17, 24 and 31 March in Europe/Copenhagen. The same series read from Pacific/Honolulu rendered 03:00, 03:00 and **02:00** — the instant moved by an hour because Copenhagen went to UTC+2, which is exactly what a zone-as-a-rule produces and what a stored UTC offset cannot |
| 24 | `acl.list` needs `calendar.acls.readonly` (§2.15) | Discovery document; **spike G live, 2026-09-15 — positive half only** | **Confirmed from the primary source; the negative half is deliberately not probed.** With the scope granted, `acl.list` succeeds. Proving it is *refused* without the scope needs a grant that never had it — and Google stores a grant per OAuth client and user, so a later authorization asking for less does not revoke what was already given. Getting a clean negative would mean revoking this server's access at the account level and logging in again, which throws away the working login to re-confirm what the discovery document already states plainly: `acl.get` lists `calendar.readonly` among its scopes and `acl.list` does not. **The consequence is contained**, which is why this is acceptable: if §2.15 is wrong, the only cost is one scope on the consent screen that nothing needs, and `get_calendar` already reports a refused sharing read as a note rather than as an empty list (§7.1). Spike G stays in §15 and answers itself the first time anyone runs the driver under a narrower grant |
| 27 | A profile's recorded scopes are the ones the account granted | Live, 2026-09-15 | **Refuted.** `login` stored what it *asked for*, so `status` presented a request as a fact about the grant — a scope Google refused would have been listed as held. It now records what `tokeninfo` reports the token actually carries, falling back to the request only when that call fails. Spike G reads the live token for the same reason: the recorded list would let it answer confidently and wrongly |
| 25 | `tokeninfo` reports which account signed in | Live, 2026-09-15 | **Refuted.** It returns an email only when an email scope was granted, and this server asks for none — so the account was silently blank and `status` had a field that could never populate. The account is now read from the primary calendar's id, which is the address, and costs no extra scope. Asking for `userinfo.email` was rejected: a calendar server should not need to read a profile to say whose calendar it is looking at |
| 28 | The `Event` resource has 44 published properties, and the four resources this server models have 81 between them | Discovery document, revision 20260826, fetched 2026-09-15 | **Confirmed, and now held by a gate.** `api-diff` records the field list beside the methods and `api-fields` holds one verdict per field: 56 modelled, 25 written off. The count in §8b is no longer a number somebody typed |
| 29 | `colorId` is how an event's colour is set | Discovery document, `Event.eventLabelId` | **Superseded, and worth knowing before phase 2 writes a colour.** `eventLabelId` "supersedes the index-based colorId property" and refers to a label defined on the calendar (`Calendar.labelProperties`). Both are written off for now; §8b's row says so rather than leaving the newer field unmentioned |
| 30 | A daylight-saving transition is an hour | tzdata, through `internal/recur`'s table tests | **Refuted.** Australia/Lord_Howe shifts by 30 minutes, so the week containing its transition is 168h30m. The expansion walks dates and carries the wall clock, so the size of the shift never enters the arithmetic — the test asserts the gap to prove the transition was really there |
| 31 | `events.instances` takes any event id | Discovery document; **live, 2026-09-15** | **Answered, and it is none of the three guesses.** Google returns **200 and expands the occurrence the id names**. A cancelled occurrence expands to nothing, so the call succeeds with an *empty list* — and `list_instances` reported "No occurrences" for a series that has three. A refusal was owed and the API never gives one, so the server reads the id's shape instead: an event id is base32hex (row 21), so an `_` cannot occur in one, and `{id}_{yyyymmdd}[T{hhmmss}Z]` is refused up front with `[invalid]` naming the series. `caltest` reproduces the 200 rather than the 400 it used to guess. What Google does for a plain non-recurring event is still unprobed and still a choice in the fake |
| 32 | `EXDATE` and `RDATE` carry a `TZID` parameter, or a `VALUE=DATE` form on an all-day series | RFC 5545 §3.8.5, and the shape Google returns in `recurrence` | **Asserted from the specification, not probed live. Tier 3.** `internal/recur` parses both and resolves a bare local time in the series' own zone. An all-day point stays a date and yields no instant, which is §4.1 again. A series carrying anything this package cannot expand — `EXRULE`, a second `RRULE`, a frequency below DAILY — is refused rather than expanded, because a count that is quietly too large is the number a caller would put in front of a user |
| 26 | Redirecting the config directory and the environment isolates a test | Live, the hard way, 2026-09-15 | **Refuted, having been written down here first.** The OS keyring cannot be redirected by either, so `go test ./cmd/...` found the maintainer's real refresh token under the default profile, revoked the grant at Google and deleted it. `TestMain` now substitutes the keyring for the whole package, with a decoy test that fails if that is ever dropped. The sibling servers carry the same warning; having it in the source did not prevent it |
| 36 | An account can hold as many calendars as a test needs | **Live, 2026-09-16** | **Refuted, and it closes a spike by making it unrunnable.** Spike I's readable half creates 51 calendars to ask whether the free/busy ceiling counts only calendars it can expand. Google refused the **39th**: HTTP 403, `reason: quotaExceeded`, "Calendar usage limits exceeded". So the readable half cannot be run on an account at all, and §2.10's ceiling stays unsettled for readable calendars by decision rather than by neglect. **Two consequences beyond the spike.** The limit is on *creation* and is not refunded by deleting — the 38 calendars were deleted and the quota stayed spent, so a later run can fail to create even its one scratch calendar until Google resets it. And phase 3's `create_calendar` meets this exact 403: `classify` already maps `quotaExceeded` to `[rate_limited]`, which is correct and retryable, but the message reads "Google is rate limiting this account", which invites an immediate retry of something that may not succeed for a day. Phase 3 owes that message a better sentence |
| 37 | Deleting an event releases its id for reuse | **Live, 2026-09-16** | **Refuted.** A re-insert under a deleted event's id is answered 409. The live driver had fixed seed ids and emptied its scratch calendar before seeding; the emptying succeeded and the seed still failed. Ids are generated per run now — Go's `strconv.FormatInt(n, 32)` uses `0123456789abcdefghijklmnopqrstuv`, which is exactly base32hex's alphabet, so a formatted integer is a legal event id by construction rather than by inspection (row 21) |
| 38 | "This and following" preserves exceptions after the target | Recurring-events guide; **spike E live, 2026-09-16** | **Refuted, as the guide says and §4.2 warns.** An eight-occurrence weekly series with its sixth occurrence moved 30 minutes later, split at the fourth: the original kept `COUNT=3`, the new series took `COUNT=5`, and the moved occurrence returned at its scheduled time. The exception was **reset**. `this_and_following` must say so in its result, because nobody expects it. The split was computed by `Set.Split` in `internal/recur`, so phase 1's arithmetic is confirmed against Google rather than against itself |
| 39 | A duplicate client-generated id may pass undetected at creation (§2.11) | **Spike F live, 2026-09-16** | **Not reproduced, and the class stays anyway.** Two inserts of one id in flight together: one 200, one 409. The collision was caught. `ambiguous_outcome` is not retired, for two reasons worth keeping: the discovery document declines to *guarantee* detection, so a single observation is not a promise; and the class also covers the retry after a transport failure, where the caller never saw the first answer and Google's 409 would be reporting the caller's own event back at it. One fewer reason to fear the class, not a reason to drop it |
| 40 | `externalOnly` means "guests outside your organisation" | Discovery document, `events.insert.sendUpdates`, revision 20260826; **spike A live, 2026-09-16** | **Confirmed by the API and refuted by its own documentation — the probe wins (hard rule 13).** The enum description says "Notifications are sent to **non-Google Calendar** guests only", and this document was corrected to match it. Then spike A put one guest inside the organiser's Workspace domain and one outside it on a consumer Gmail account, and inserted the same event three times. Under `externalOnly` the **out-of-domain guest was mailed and the same-domain guest was not** — even though both demonstrably use Google Calendar, which is the axis the description names. The real axis is the organiser's Workspace domain. §4.3's `dry_run` can therefore split its count exactly, from the organiser's own primary calendar id, and does. **Two corrections in one day from one parameter description**: it is the second field in this phase whose published description was the misleading thing, after `showDeleted` (row 33) |
| 41 | `none` still sends some mail (§2.6) | **Spike A live, 2026-09-16** | **Not reproduced, and the rule stands anyway.** Two inserts carrying `sendUpdates=none` with two guests mailed neither of them. Google's warning is that mail "might still be sent", not that it is, so one silent run is not a promise of silence — §4.3 rule 3 keeps its refusal to report `none` as silence for the same reason spike F did not retire `ambiguous_outcome` (row 39). What this does remove is the fear that `none` is routinely noisy: it is not, on this shape of write. **Caveat held deliberately:** the out-of-domain guest also did not receive the `all` invitation, which it should have, so something filtered mail on that side and the absence of the `none` mail there is not clean evidence. The same-domain observation is clean, because that guest did receive `all` |
| 42 | `all` notifies all guests | **Spike A live, four runs, 2026-09-16** | **Confirmed — and the three runs that seemed to refute it were measuring the receiver.** An out-of-domain Gmail guest received `externalOnly` every time and `all` never, across three runs and both orderings, which was recorded here as "the API accepted the invitation and did not deliver it". The fourth run put a **non-Google address on the same events as that Gmail address**, so one send could be watched at two receivers: the non-Google guest received **both** `all` and `externalOnly`; the Gmail guest received only `externalOnly`. Same event, same moment, one delivered and one not — so Google sent the `all` notification and Gmail did not surface it. **The correction matters more than the finding.** Three consistent runs were consistent because the instrument was, and repetition looked like evidence. What broke it was not a fourth run but a second receiver, which is the only thing that could separate sending from delivery |
| 43 | Deleting an event removes it from the guests' calendars | **Live, 2026-09-16** | **Refuted when `sendUpdates=none`, and it is the sharpest argument §4.3 has.** An event created with two guests was deleted with `sendUpdates=none`. It is gone from the organiser's calendar — confirmed by listing that calendar, which now holds only later events — and it is **still on a guest's calendar**, showing both attendees and awaiting a response. The organiser believes the meeting is cancelled; the guest still has it. Found because the live driver was doing it: its own cleanup deleted guest-carrying probe events with `none` for a day and left them on two real calendars. The driver now cancels anything with attendees using `all`. For phase 2 this is `cancel_event` with `notify: none`, and §4.3's refusal to treat `none` as harmless now rests on a demonstration rather than on a warning in a document |
| 44 | `sendUpdates=none` on insert can lose an event (§2.7) | **Spike B live, 2026-09-16** | **Confirmed, and it is structural rather than a defect.** A non-Google address was invited to an event inserted with `none`. It received nothing — in a run where the same address demonstrably did receive both `all` and `externalOnly` minutes earlier, so the channel was working. A guest outside Google Calendar has no calendar for the event to appear in, so mail is the only way they can learn of it, and `none` removes the only way. The event exists, with them attached, and they cannot discover it by any means. That is §2.7's "events being lost altogether for some users" with the mechanism visible. §4.3 refuses `none` when a guest is outside the organiser's domain rather than warning about it, because a warning is something a caller skims |
| 45 | `leaks-history` protects the repository before it goes public | **Run for the first time, 2026-09-16** | **It could never have passed.** The allow-list carried `@noreply.anthropic.com`; the address in every commit's attribution trailer is `noreply@anthropic.com`, the other shape. So the gate failed on fourteen of fifteen commit messages, and had done since the first commit — unnoticed because it is the one gate `make check` deliberately does not run, being reserved for "before going public". A gate nobody runs is a gate nobody knows is broken, which is the same lesson as the three gates phase 1 found named but absent, one level further out: that failure was a list claiming a gate existed, this one is a gate that exists and was never executed. The entry is added as §9.1 requires, argued rather than widened: a vendor's non-routable no-reply address in a Co-Authored-By trailer, structurally identical to the GitHub noreply already allowed, saying nothing about a deployer, an organisation or anyone's calendar |
| 46 | `make check` passing locally means it passes on a fresh clone | **First CI run, 2026-09-16** | **Refuted twice in one run, and neither failure was platform-specific in the way "CI has never run on macOS or Windows" implied.** The `staleness` gate failed on **all three** platforms, Ubuntu included, because `internal/plan`, `scripts/evals` and a third under `scripts/` existed locally as **empty directories** and git does not carry those. The gate had been passing on a working tree that no clone could reproduce — including every contributor's. It also caught a real documentation error on the way: that third directory never existed at all — the live probes live in `scripts/livecal` — and both this document and CLAUDE.md had said otherwise since phase 0. The gate now accepts a path a later phase builds only when the text names that phase, so a plan and a stale reference are told apart by the author rather than guessed at. Separately, `gofmt` listed every `.go` file on Windows: git checked the tree out with CRLF and Go's tooling assumes LF, so a `.gitattributes` pins `eol=lf`. **The lesson is the empty directories**: a gate is only as honest as the tree it runs on, and a local tree is not the artifact anybody else gets |
| 47 | The file fallback's 0600 protects the refresh token on every platform | **First CI run on Windows, 2026-09-16**; Go's `os` documentation; `golang.org/x/sys/windows` | **Refuted, and the warning was asserting it.** Go's file modes do not map to Windows ACLs — on Windows the mode only decides the read-only attribute — so the token file was written 0600 and landed at **0666**, readable by any account on the machine, while both warnings said "mode 0600" on every read and every save. A sentence a user would rely on, false on one of three supported platforms, and the same failure as promising `none` means silence (§4.3 rule 3): claiming a guarantee the platform declines to make. **Fixed rather than documented away.** The file is given an explicit DACL granting only the current user's SID, set with `PROTECTED_DACL_SECURITY_INFORMATION` so the entries inherited from the parent directory are replaced rather than added to — a grant without that flag widens access instead of restricting it. Administrators and SYSTEM are deliberately not named: they can take ownership regardless, so listing them would only make the list longer. The ACL is applied after the rename, because on Windows it belongs to the file at its final path. A Windows-only test reads the list back and asserts it is protected and holds exactly one entry, rather than trusting the call that set it |
| 48 | `events.move` returns the moved event, so its response describes where the event landed | **Live, 2026-09-16** | **Refuted, and it made a successful move read as a cancellation.** A move that worked answered with `status: cancelled`, so the result rendered `[cancelled]` next to an event that had just been moved — the one word a caller would act on, and the exact opposite of what happened. Reading the event on the DESTINATION immediately afterwards showed it confirmed and intact, which is how the response was caught lying rather than the move. `move_event` reads the event back from the destination now and owns the extra request in its count. Every gate was green; only the transcript showed it, which is §13's whole claim about green gates |
| 49 | `events.move` cannot carry `If-Match`, because it is a POST with no body and nothing documents the header | **Spike J live, 2026-09-16** | **Refuted: it is HONOURED.** A stale etag on a move is refused with **412**. The server had been sending none and saying in the result that this was the one write without the protection — an assumption stated honestly and still wrong. §4.4 has no exception now, `move_event` takes an etag and a `force` flag like every other write, and the fake refuses a stale one so the behaviour is held offline. The lesson is narrow and repeatable: "the documentation does not say" is a question, not an answer, and asking cost one probe |
| 50 | Google's `self` flag marks the signed-in account on its own attendee row | **Live, 2026-09-16** | **Refuted on a secondary calendar, and it made a write that reached nobody demand a notification decision.** The driver put the account on its own event, on a calendar that account owns, and the attendee came back WITHOUT `self` — so §4.3.2's "a write that reaches nobody does not ask" counted the caller as their own guest and `respond_to_event` was refused with "this write reaches 1 guest". The flag appears to be relative to the calendar in the request rather than to the authenticated user, and a secondary calendar is not a person; that mechanism is a reading of one observation and the fix does not rest on it. `model.Event.Guests` takes the account's own address and excludes it whatever the flag says |
| 51 | `acl.delete` takes a `sendNotifications` like the other ACL writes, so `unshare_calendar` needs a `notify` | Discovery document, `calendar.acl.delete` and `calendar.acl.patch`, revision 20260826, refetched 2026-09-16 | **Refuted, and it removed a parameter rather than adding one.** `acl.delete` publishes no `sendNotifications` at all, and `acl.patch`'s own description says it plainly: "Note that there are no notifications on access removal." So there is no choice to offer. `unshare_calendar` takes no `notify`, and its result says what the API's silence means for a person: they are not told, they find the calendar gone. A `notify` parameter there would have been a switch wired to nothing, reported as though it had done something — the same failure as reporting `none` as silence (§4.3 rule 3), with the wire empty instead of ambiguous |
| 52 | §4.3's three choices carry over to the ACL tools unchanged | Discovery document, `calendar.acl.insert.sendNotifications`, revision 20260826 | **Half refuted: the requirement carries, the vocabulary does not.** `sendUpdates` on an event is an enum of three; `sendNotifications` on a rule is a **boolean**, so `external_only` has nothing to map to. It is refused as `[unsupported]` rather than rounded, because rounding it emails either more people or fewer than the caller asked for, and neither is the thing they said. The requirement itself carries and is stricter here: there is no "reaches nobody" exemption, because a group address expands to people this server cannot count and a domain rule covers everybody in one — the reach of a sharing change is not knowable from here, so the choice is always the caller's (§4.3) |
| 53 | `calendars.clear` empties any calendar and `calendars.delete` removes any calendar | Discovery document, method descriptions, revision 20260826 | **Refuted by the methods' own descriptions, and both guards come from them.** `clear` is "Clears a **primary** calendar"; `delete` is "Deletes a **secondary** calendar. Use calendars.clear for clearing all events on primary calendars." So `delete_calendar` refuses the primary naming `clear_calendar`, and `clear_calendar` refuses anything else naming `delete_calendar`. What the documentation does NOT say is what `clear` does when handed a secondary calendar, and the three possibilities differ: refused, cleared, or accepted-and-did-nothing. Spike K asks, against a calendar the driver filled itself, counting events on both sides — because a 2xx alone would settle nothing, which is the mistake spike I made once. **Answered in row 56**: refused with 400, so the guard is the API's rule rather than this server's caution |
| 54 | A calendar notification has a delivery method worth exposing as a choice | Discovery document, `CalendarNotification.method`, revision 20260826 | **Refuted: there is exactly one.** "The possible value is: 'email'." So `manage_calendar` takes notification TYPES — creation, change, cancellation, response, agenda — and fills the method in, rather than offering a parameter with one option and letting a caller wonder what the others are. The same reading settled the other half: `notificationSettings` is written whole, so the tool replaces the list rather than adding to it, and an empty list turns every notification off |
| 55 | A user can remove any calendar from their own calendar list | **Live, 2026-09-16** | **Refuted for a calendar you own, and nothing published says so.** `calendarList.delete` on a calendar this account had just created answered **403: "The data owner of a calendar cannot remove such a calendar from their calendar list."** So `manage_calendar`'s unsubscribe could not work on any calendar the caller made, and the tool offered it anyway. It is translated now into the two things that do work — `hidden:true` to keep it out of the way, `delete_calendar` to remove it for everybody — and classified `[unsupported]`, because no retry and no permission changes it. **Not pre-empted**, deliberately: Google's own role description separates the `owner` ROLE from the single data OWNER, and the `dataOwner` field is written off in §8b for being an address nothing here needs, so the API stays the authority on who that is and this server translates its answer. The live driver cannot exercise the working path at all — every calendar it has is one it made — so unsubscribing from somebody else's calendar is held offline against the fake and said so in §16 |
| 56 | `calendars.clear` empties whichever calendar it is given | **Spike K live, 2026-09-16** | **Refused on a secondary calendar: 400.** Google's description says "Clears a primary calendar" and this server refused anything else on the strength of that prose, which rule 13 does not accept on its own. The probe put an event on a calendar the driver owns, cleared it, and counted both sides — because a 2xx alone would have settled nothing, which is the mistake spike I made once. The refusal `clear_calendar` gives is the API's rule now rather than this server's caution, and §7.5 says which. The third possibility, accepted-and-did-nothing, is the one this was written to catch and did not happen |
| 57 | `If-Match` is honoured on the calendar and sharing writes (§2.4, §4.4) | **Spike L live, 2026-09-16** | **Confirmed on both, with a stale etag: 412 from `calendars.patch` and 412 from `acl.patch`.** §2.4 says ETags work across this API, but spike J had just shown that "the general rule" and "this method" are separate questions — `events.move` honours the header while nothing published says it applies. So the four methods phase 3 added were sending `If-Match` on an assumption. They are not any more: §4.4 covers the calendar and sharing writes as it covers the event ones, and the results may say so. The discriminator is the same one spike J needed — a CURRENT etag would have succeeded whether or not the header is read |
| 58 | A patch that writes a field the value it already holds still moves the resource's etag | **Live, 2026-09-16, second run** | **Refuted for `calendars.patch`, and it broke a spike to find out.** Spike L patched the scratch calendar's description to a fixed string, which on the second run was the value already there; Google returned the SAME etag, so no stale one could be built and the spike reported undetermined on a question it had answered an hour earlier. The claim appears in `internal/plan`'s own comments as the reason a no-op field is never sent, and one observation now contradicts it on one resource. **The rule it justifies survives the refutation on its own merits**: not sending a field that has not changed saves nothing to argue about, keeps the change list honest about what the write did, and costs nothing — so the comments say that instead of asserting a bump nobody has verified. Whether `events.patch` behaves the same way is **unprobed**, and the comment there says so rather than guessing. The spike's own text now carries the run's mark, because a probe that only works on a calendar it has never touched is a probe that works once |
| 59 | A URI template variable matches a calendar id | RFC 6570; the SDK's matcher, probed locally 2026-09-16 | **Refuted, and it would have made every secondary calendar unreachable.** `gcal://calendars/{calendar_id}` uses simple expansion, which matches unreserved characters only — and every secondary calendar id is an ADDRESS. The obvious URI, with the at sign written as itself, matches nothing and the read comes back "not found", while the percent-encoded form works; a model writing the id it was just given by `list_calendars` gets the first one. The templates use reserved expansion (`{+calendar_id}`) now, which matches both. That has a second consequence worth stating: reserved expansion also matches a slash, so the calendar template matches an EVENT URI too, and the SDK routes a read to the first template that matches. Both templates therefore share one handler that parses the URI itself, rather than depending on the order two registrations happen to be in |
| 60 | Conference data is generated asynchronously, so an insert answers with a pending request and no link | Discovery document, `ConferenceData.createRequest` and `ConferenceRequestStatus`, revision 20260826; **spike M live, 2026-09-16** | **Refuted as the usual case, and the published one is kept anyway.** The insert answered `status: "success"` with the video entry point already in it — no waiting, no second read. The documentation says the data "is generated asynchronously" and publishes "pending" as a status, so the slower answer is a thing the API may do; one observation does not retire it, for the same reason spike F did not retire `ambiguous_outcome`. What changes is every sentence that said a caller usually has to read the event again: the link normally arrives with the event, `create_event` reports what came back rather than what it asked for, and "still being made" is the other answer rather than the expected one |
| 61 | Conference data in an insert body is honoured | Discovery document, `events.insert.conferenceDataVersion`, revision 20260826; **spike M live, 2026-09-16** | **Refuted without the version parameter, and it fails silently.** `conferenceDataVersion` defaults to 0, which "ignores conference data in the event's body": the probe sent the same body twice, once with the parameter and once without, and the version-less insert answered **200 with the event created and no conference at all**. Success, with the one thing the caller asked for missing and nothing in the response saying so. The client sets the parameter from the BODY rather than taking it from each caller, so a call site cannot forget it, and `caltest` drops conference data without it exactly as Google does — a fake that accepted it would let this ship |
| 62 | The release scaffolding §16 lists as done exists | **The tree, 2026-09-16** | **Refuted: there is no `.goreleaser.yaml` and no release workflow.** §16's phase 0 entry names "goreleaser, CI, CodeQL and release workflows" among the things it built; CI and CodeQL exist and the other two never did. Nothing noticed because nothing references them: the staleness gate checks paths named in backticks in the docs, and a claim in prose naming no path is invisible to it. This is the same shape as the three gates phase 1 found named but absent, and as the empty directories of row 46 — a list of things is not the things. The bundle this phase built is therefore packed by `make mcpb-pack` and installed by hand; wiring it into a signed release is owed, and §16 says so where it used to be claimed. **Closed in phase 5**, which built both, added the `release` gate so the config is held against the packer on every commit, and rewrote phase 0's list to name what it actually built |
| 63 | A `this_and_following` split expands the series in the event's own zone | **Review, 2026-09-16; two reproduction attempts** | **Refuted as written, and the consequence is UNPROVEN.** The split reads the parent through `model.ParseWhen`, which re-renders the instant into the zone the CALL asked to be shown in and drops the wire `timeZone`; `recur.ExpandTimes` then takes its wall clock and its day walk from that. So a series read with a `time_zone` other than its own expands from a different anchor, and the head count that becomes the truncated series' `COUNT=` is computed over a different set of instants. Two attempts to make that change the answer — a weekly `BYDAY=MO` series in Asia/Tokyo split while shown in America/Los_Angeles, and a daily one near local midnight — produced the SAME rule on both sides, because the generated set and the target instant shift together and a rule's gap is wider than the shift. Recorded rather than fixed: the mechanism is real, the harm is not demonstrated, and threading the event's own zone through `Set.Split`, `Set.Reach` and `cancelFollowing` is a change to phase 2's write path that no test here can currently hold. Anybody picking it up should start with a rule whose LOCAL day walk changes the number of matches — `BYMONTHDAY=31`, or `BYDAY=-1SU` near a month boundary |
| 64 | goreleaser writes the macOS universal binary somewhere a `dist/*darwin*universal*/` glob matches | **A snapshot build, 2026-09-16** | **Refuted, and the packer could never have worked.** The directory is `dist/<id>_darwin_all/<binary>` — the id comes FIRST, so a glob reading "darwin then universal" resolves to nothing, and `mcpb-pack` would have failed on the macOS binary at the most expensive moment there is. Phase 4 wrote that glob against a tool that did not exist yet, which is why no gate could hold it; the fix is `dist/*universal*darwin*/` and, more to the point, `gates release`, which derives every directory the build matrix produces and requires each staged glob to match exactly ONE of them. The same run confirmed the other three globs resolve, and that a glob of `dist/*darwin*/` would match three directories rather than none — the failure that packs the wrong binary rather than no binary |
| 65 | A `--snapshot` rehearsal can check the version in all five places §10b names | **Two snapshot builds, 2026-09-16** | **Refuted as the config was first written, and now true.** The ldflags stamped `{{ .Tag }}`, which goreleaser resolves to `v0.0.0` in a repository with no tags, while the archives, the bundle and the manifest all carried the snapshot version — so the binary's own `--version` was the one place that disagreed, and it disagreed for a reason that would vanish on a real tag. A rehearsal that cannot exercise a check is not a rehearsal of it. `{{ .Version }}` is the same string as the tag either side of the `v`, which `internal/version` restores, so the release is unaffected and the snapshot now agrees in all five. The bundle's filename was the second: it carried no version at all, so it could not disagree and could not be checked either |
| 66 | `changelog: disable: true` is how to leave `--release-notes` in charge | **The shared standard and three sibling servers' evidence, adopted unverified here** | **Refuted there, and the block is simply absent here.** `disable` is read in the changelog pipe's `Skip`, which runs before `Run`, so `ctx.ReleaseNotes` is never assigned and the notes file the workflow just wrote is never opened: the release body collapses to the footer alone while every step stays green. A server shipped that and every release page it published was a footer with nothing above it. Not verified in this repository — it cannot be, before a tag — which is why the config carries the reason in a comment where the block would otherwise be added back |
| 67 | cosign's `--output-signature` and `--output-certificate` still produce a signature | **The shared standard and cosign's v3.0.1 release notes, adopted unverified here** | **Refuted for cosign 3.** `--bundle` moved from optional to required, and a config carrying only the older two gives cosign no output path at all: it fails with `create bundle file: open : no such file or directory` rather than degrading. It failed rather than degraded because the action was pinned and the tool it installs was not, which is the half of §9's pinning rule that reads as complete when it is not — `make pins` holds both halves now, for `cosign-release`, `syft-version` and goreleaser's own `version` |
| 68 | `go mod tidy` is a safe goreleaser `before` hook | **The shared standard, adopted unverified here** | **Refuted, and it fails only on the tag.** A hook that can rewrite `go.mod` or `go.sum` dirties the tree, and goreleaser refuses to release from a dirty tree — while `--snapshot` runs the hook and skips that check, so every rehearsal stays green and the first real tag fails. The hook here is `go mod download`; tidiness is CI's job, which runs `go mod tidy -diff` on the same commit the release is cut from. The release workflow writes its notes file outside the checkout for the same reason |
| 69 | The tool-pin half of `make pins` checks what its comment says | **Probed, 2026-09-16** | **Refuted twice over, in the first version written.** It split steps by indentation and then read EVERY line of a step for the input, so a `version:` under `env:` satisfied the goreleaser pin — and `version` is the most collidable input name there is. It also understood block sequences only, so a workflow written in flow style produced no steps, no installers and **no problems**: "looked at nothing" printing the sentence "found nothing", which is the one failure `scripts/gates` exists to refuse, committed inside the gate written to refuse it. Both were found by running the code rather than reading it. There is one workflow reader now, a YAML parse in `scripts/gates/workflow.go`, shared with the release gate; both failures are regression cases, and `pinGate` asserts a floor on how many installers it SAW rather than only on how many were wrong |
| 70 | Deriving the documented-path roots from the repository's own top-level entries is the general fix for an allow-list of them | **Tried and reverted, 2026-09-16** | **Refuted: it is circular, and weaker than the list it replaced.** `checkPaths` treats a backticked token as a path only if its first segment is a root the repository has — so a file that does NOT exist has no such root, is filed as prose, and excuses itself. The motivating case proves it: `.goreleaser.yaml` named in the docs while no such file existed would be skipped rather than flagged, which is exactly how §16 could call the release built for four phases. A probe caught it immediately, having watched the shape-based version flag the same token. The rule is therefore SHAPE, never existence — a path under a source directory, or a root file with one of the extensions a root file here actually has. `.txt` and `.json` are excluded by name because `checksums.txt` and `manifest.json` are documented and live in a release archive and a bundle rather than in this repository |
| 71 | A universal binary is named by the `binary` of the build it joins | **Probed with the two renamed apart, 2026-09-17** | **Refuted: it is named by `universal_binaries[].name_template`, which defaults to the PROJECT name.** A snapshot with `binary: gcal-probe` wrote `gcal-probe` into every ordinary target's directory and `google-calendar-mcp` into `..._darwin_all`. The release gate took that name from the build, so it was right only while `project_name` and `binary` happened to be the same string: renaming the project alone would have moved the macOS file, left the gate green and failed `mcpb-pack` at tag time — the same failure as row 64, reached through the gate written to prevent it. Found by `/code-review high`, which reasoned it out, and settled by the probe rather than by the reasoning |
| 72 | `nextSyncToken` comes back on every page of a sync read | **Discovery document, `events.list.syncToken`, revision 20260826** | **Refuted: "the LAST page of results".** So a read that stops at §4.7's budget has no token at all, and a server that handed one over anyway would give the caller a token covering pages it never saw — silent, permanent data loss from the caller's point of view, and exactly the shape this repository exists to refuse. `list_changes` therefore reports "there is NO sync token yet" in the text and clears the field, rather than treating a missing token as an empty one. The same paragraph settles three more: deletions are always in the result and `showDeleted` may not be false; `iCalUID`, `orderBy`, `privateExtendedProperty`, `q`, `sharedExtendedProperty`, `timeMin`, `timeMax` and `updatedMin` are all refused alongside a token, so the tool offers none of them; and "all other query parameters should be the same as for the initial synchronization", which is why a baseline reads with `showDeleted` true and labels what it finds "already cancelled" rather than "deleted" |
| 73 | A baseline sync read reaches the last page, and therefore gets a token, within the event budget | **Live, 2026-09-17; spike N** | **Refuted, and it made `list_changes` useless on a real calendar.** The first live run of phase 6 failed: the baseline came back with NO token. Spike N asked the API directly with six parameter sets and every one answered `nextPageToken` set and `nextSyncToken` absent — including the BARE request, and including one with `items: 0`, which is Google applying the page size before it filters. Paging to the real last page took **3 pages and 527 rows** on a scratch calendar holding twelve live events: the rest were cancelled tombstones left by earlier runs, which `showDeleted=true` must return and sync cannot be built without. So the token exists, arrives on the last page exactly as row 72 says, and the 250-event budget stopped the read three pages short of it — for ever, on any calendar with a deletion history. The fix distinguishes the two reads: a BASELINE pages past its budget, because its rows are not changes and the token covers them, and reports how many it passed over; an INCREMENTAL read still stops, because there every row is a change the caller has not seen and handing over a token would mark them as delivered. Nothing about this was visible against the fake, which had a dozen events and no history |
| 74 | A transcript is safe to paste once addresses, ids and links are redacted | **Read, 2026-09-17** | **Refuted again, and by this phase's own tool.** `list_changes` prints the sync token, and the live transcript carried a real one from the account. It is a cursor rather than a credential — it grants nothing — but it is account state with the entropy of a secret, and the leak gate refuses a string of that shape in the tree, which is the same judgement. It has no shape of its own to anchor a rule on, so the redactor anchors on the LABEL this server's own renderer prints in front of it, which is a firmer anchor than a shape: it cannot drift without the renderer changing. Row 34 found the same class in display names and fixed it with scope; this one had a label available |
| 33 | `showDeleted=false` means Google filters cancelled events out | Discovery document, `events.list.showDeleted`; **live, 2026-09-15** | **Refuted, in the one case the parameter names itself.** "Cancelled instances of recurring events (but not the underlying recurring event) will still be included if showDeleted and singleEvents are both False." The server passed the parameter and trusted it, so a `no_expand` read returned the cancelled occurrence — and Google sends such an instance **bare**, with an id, a status, its series and its original date but no start and no summary. It rendered as a row with no date and no title and was counted among the results. The service filters cancelled events itself now, in `drain`, where the budget counts what the caller sees. `caltest` had been hiding them, which is why no test caught it |
| 35 | An occurrence id is `{seriesId}_{yyyymmdd}[T{hhmmss}Z]`, and the split is safe because an event id cannot contain `_` | **Live, 2026-09-15**, plus row 21 | **Confirmed, and it had to be, because a user-visible refusal now rests on it.** `events.instances` returned ids of exactly that shape (`…_20260317T130000Z`), and row 21 establishes that an event id is base32hex — `a`–`v` and the digits — so `_` cannot occur in one. `list_instances` refuses an id matching the shape and names both the series and the occurrence's start. Recorded as its own row because row 31 establishes the API's *behaviour*, not the id *grammar*, and the live driver's own comment declines to compose an instance id on the grounds that the format is undocumented — the server adopts it, so it owes the verdict. Both halves of the rule now live in `internal/gcal` — `ValidEventID` and `SplitOccurrenceID` — beside the wire types they describe, which is where §2.11's client-supplied id on insert will need them in phase 2. The live driver calls the same function it used to keep its own copy of |
| 34 | The transcript redactor makes the live driver's output safe to paste | The first live run of phase 1, read | **Refuted for one step, and the gap is structural.** The redactor is anchored on *shapes* — an `@` with a dot-suffixed domain, a known URL prefix, a token's literal prefix (§9.1) — and **a display name has no shape**. `list_calendars` is the one step that reads past the calendar the driver created, and its body printed a dozen of the account's real calendar titles, one of them a private rename. No rule could have caught them. So the fix is scope, not pattern: a step marked `wholeAccount` never prints its body, on success or on failure, and its check reports what it verified instead. §9.1's promise — the driver reads only what it wrote — now holds for what reaches the terminal, which is where it was being broken |

### Deviations from the shared Go MCP server standard

The standard at `~/.claude/mcp-server-standard.md`, read 2026-09-15.
One deviation, and one place where this server can hold a rule a sibling
could not.

| The standard says | Here | Why |
|---|---|---|
| Errors use six classes: `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Twelve, adding `forbidden`, `stale`, `ambiguous`, `blocked`, `rate_limited`, `ambiguous_outcome` | `stale` (412) and `conflict` ask the caller to do different things; `ambiguous_outcome` is forced by §2.11, where a retry may double-book. The vocabulary is closed, derived from the code and asserted in both directions (§6.5) |
| Never overwrite: compute a minimal diff | Held as written | Recorded because the sibling Sheets server had to deviate here and a reader may expect the same. Calendar supports partial updates and `If-Match` properly (§2.3, §2.4), so the rule is implementable: §4.4 patches always and §8a writes off all four PUT methods |

Everything else is adopted as written, including the preamble's three
obligations for any rule adopted — make it a test, derive the list from
the code rather than typing it out, and have the checker assert a floor
on how much it read.
