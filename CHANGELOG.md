# Changelog

All notable changes to this project are documented here.

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- The release itself: `.goreleaser.yaml` and
  `.github/workflows/release.yml`. Six platform archives,
  `checksums.txt`, an SBOM per archive, a keyless cosign signature over
  the checksums, and `actions/attest-build-provenance` over every
  published file. Built with `-trimpath` and a `mod_timestamp` taken from
  the commit, so rebuilding a tag reproduces it byte for byte.

  The bundle is packed in the universal binary's post hook — the one
  point in the pipeline where every binary exists and `checksums.txt` has
  not been written yet — and named in **both** `checksum.extra_files` and
  `release.extra_files`. Both, or it ships unsigned, or it is hashed and
  never published, and neither looks any different on the release page.

  The release body is the CHANGELOG section for the tag, written by
  `gates release-notes` and passed with `--release-notes`. There is no
  `changelog:` block, deliberately: `disable` is read in that pipe's
  `Skip`, which runs before `Run`, so the notes file is never opened and
  the body collapses to the footer alone with every step still green.

  A tag is only a pointer, so the workflow refuses to publish a commit
  whose `ci` run was not green.
- `make release`, which holds the release config against the bundle it
  packs, on every commit rather than on release day. Each staged glob
  must resolve to exactly one directory the build matrix produces; the
  post hook must pack to the path the Makefile names; the bundle must be
  both checksummed and uploaded; the archives must exclude the universal
  binary; and the signature must pass `--bundle`, which cosign 3 made
  required. Twenty-nine ways of breaking it are watched failing in tests.
- One workflow reader for the gates, `scripts/gates/workflow.go`. The
  first tool-pin check hand-rolled its own and was wrong in both of the
  ways that goes wrong: it read every line of a step rather than the
  `with:` block, so a `version:` under `env:` satisfied the pin, and it
  understood one of YAML's two list styles, so a workflow it could not
  read reported no problems at all. Both are regression cases now, and
  the gate asserts a floor on how many installers it SAW.
- The gate reads the macOS binary's name from `name_template`, not from
  the build it joins. goreleaser defaults that template to the PROJECT
  name, so taking it from `binary` was right only while the two matched:
  renaming the project alone moved the file, left the gate green and
  would have failed the pack at tag time (§18 row 71). `builds[].ignore`
  is modelled for the same reason — without it a release shipping five
  archives passed a check that says six.
- `make pins` now holds the second half of its own comment. Every action
  that INSTALLS a tool must pin the tool as well — `cosign-release`,
  `syft-version`, goreleaser's `version` — because a SHA on the `uses:`
  line pins the wrapper and says nothing about what it fetches. It reads
  as complete when it is not. It also names `ci.yml` and `release.yml`
  rather than counting to two, which two other workflows satisfied.
- `gates release-notes`, which prints one version's changelog section.
  It stops at the next heading and at the link footer, and lifts the
  headings one level because GitHub renders the tag as the page's `h1`.

- Resources, for clients that attach context rather than call tools:
  `gcal://calendars`, `gcal://calendars/{calendar_id}` and
  `gcal://calendars/{calendar_id}/events/{event_id}`. Each carries what
  the matching tool returns, computed once — a resource that rendered a
  calendar its own way would be a second description of the same thing,
  drifting from the first.

  The templates use RFC 6570's reserved expansion (`{+calendar_id}`),
  which is not decoration: every secondary calendar id is an address, and
  under plain expansion the obvious URI — the at sign written as itself —
  matches nothing and comes back "not found" while the percent-encoded
  form works.
- `check_availability` takes a working-hours mask: `working_from`,
  `working_to` and `working_days`. A window is one interval and a working
  week is a daily one, so "next week, 09:00 to 17:00" could not be asked
  for and the longest gap in the answer was a fifteen-hour overnight one
  that passed any `min_minutes`.

  No default in any of the three. Days left out means every day, because
  a five-day Monday default would be a guess, and it is wrong in every
  country whose week runs Sunday to Thursday. A mask that crosses
  midnight is refused rather than guessed at. The mask is applied in the
  zone the answer is rendered in, day by local day, so 09:00 is still
  09:00 on the Sunday the clocks change, and the result says which mask
  it used in both halves.
- `create_event` takes `conference: true` and asks Google for a Google
  Meet link. The link normally arrives with the event — a live run
  watched it do so — and Google documents the conference as generated
  asynchronously, so "still being made" is a published answer too. The
  result says which, and never reports a link it does not have:
  announcing one because it was asked for would be the same failure as
  reporting `none` as silence. A link can only be attached as the event
  is created; adding one to an event that exists is refused with what to
  do instead.

  Without `conferenceDataVersion=1` Google answers 200, creates the
  event, and drops the conference in silence — confirmed live, and the
  reason the client sets that parameter from the body rather than leaving
  it to each caller.

  The request id is the event id, so a retry of a create whose answer was
  never seen cannot produce a second conference. The calendar's own
  `conferenceProperties` are read first: a calendar that publishes a list
  without Google Meet in it is refused before the write, because Google
  answers that case with 200, a failed request and an event that exists.
- Any result describing an event with more than 200 guests says that its
  RSVPs are incomplete. Above that, Google stops propagating individual
  responses, so a caller counting acceptances would be wrong with nothing
  looking wrong. Where Google stops accepting guests altogether is not
  published, and the warning says so rather than inventing a number.
- `packaging/mcpb/` — the Claude Desktop bundle: the manifest, the Linux
  launcher, a packer in Go, and the `mcpb` gate that holds the manifest
  against the files the packer stages. The gate runs on every commit
  because it needs only the staged NAMES, which are static; the packer
  runs at release time, where the binaries exist.

  All six referential checks of the standard, each watched failing: an
  entry point nobody stages, a platform command nobody stages, an
  `${user_config.x}` nobody declared, an override for a platform the
  bundle does not claim, a platform running another platform's binary,
  and a launcher choosing between names the packer does not write. A
  schema catches none of them: each one produces a bundle that installs
  and then does nothing.
- `scripts/evals` — the model-facing harness. It gives a model the
  server's own tool list over an in-memory session against the fake
  calendar and scores the three failures of `docs/architecture.md` §3: an
  all-day event created from a negative-offset zone, a weekly recurrence
  that must carry its zone across a daylight-saving change, and an
  invitation that must actually reach an external guest. The score reads
  the calendar, never the model's account of what it did.

  It is run by hand like the live driver and is not part of `make check`:
  it costs money and it is not deterministic. `-self-check` runs
  everything except the model, and asserts every task FAILS on a calendar
  nobody touched — a scorer that passes there is scoring nothing.

- Calendars and sharing: `create_calendar`, `manage_calendar`,
  `list_sharing`, `share_calendar`, `unshare_calendar`, and the two
  gated tools `delete_calendar` and `clear_calendar`. The surface is
  twenty tools. `GCAL_SHARING=off` removes the three sharing tools, the
  read among them.

  `manage_calendar` covers what the API splits across two resources and
  people do not. **The calendar** — its title, description, location and
  time zone — is what everybody it is shared with sees; **your
  subscription** to it — the colour, the name you give it, whether it is
  hidden, what you are emailed about — is yours alone. The result says
  which of the two it changed, every time. Unsubscribing removes it from
  your list: it deletes nothing, touches no events, and nobody else
  notices.

  It takes no `etag`, deliberately. Those two halves are two resources
  with two etags, so one parameter could stand for only one of them
  while appearing to protect both; each patch is made under the etag of
  the read that produced it.
- Sharing shows exposure **before and after**, because "shared with
  somebody" is not an answer to "who can see this". Roles are explained
  rather than echoed — `writerWithoutPrivateAccess` and `freeBusyReader`
  do not say what they mean — and a public rule is shouted and listed
  first.

  `notify` is required on every share and has no default. Google's own
  default here is to EMAIL, the opposite of its default on an event, and
  `external_only` is refused as `[unsupported]`: the ACL notification is
  a single switch with no internal/external split, and rounding it
  either way would email the wrong set of people.

  Sharing with "anyone" publishes the calendar to the whole internet and
  needs `allow_public: true`. Removing the rule afterwards stops new
  readers and takes nothing back from whoever already looked, and the
  result says so both ways.
- `unshare_calendar` takes no `notify`, and the result says why: Google
  publishes no way to ask for a notification on access removal and sends
  none. The person is not told — they find the calendar gone.
- `delete_calendar` refuses the primary calendar, which Google does not
  delete, and `clear_calendar` refuses anything but the primary, which
  is what Google documents it for. Both are unregistered without
  `GCAL_ENABLE_DESTRUCTIVE=true` and both still need `confirm: true` on
  the call — and the refusal for a missing `confirm` names what would be
  destroyed, which is why `confirm` is not a schema-required field.
- `get_settings` reports the **calendar** colour palette alongside the
  event one. They are different sets of ids, and `manage_calendar`'s
  `color_id` indexes the calendar one, which had no published source
  before.

- The write path: `create_event`, `update_event`, `cancel_event`,
  `move_event` and `respond_to_event`, taking the surface to thirteen
  tools. `GCAL_READONLY=true` registers the first eight and requests only
  read scopes.

  Three rules run through all five, and none of them has a default.
  **`notify`** is required whenever the write can reach another person,
  and `none` is refused outright when a guest is outside the organiser's
  domain — such a guest may have no Google Calendar for the event to
  appear in, so email is the only way they can learn of it. **`scope`**
  is required when the event repeats: `instance`, `series` or
  `this_and_following`, because the same words mean three different
  operations. And **every write is a patch under `If-Match`**, refused as
  `[stale]` rather than overwriting somebody who changed it first.

  `dry_run: true` on any of them reports what would change and how many
  guests would be emailed, without writing. A result says what the server
  ASKED Google to send, never what anybody received: the API reports
  nothing about delivery.
- `create_event` generates the event id itself, so a retry after a
  failure nobody saw the answer to collides with a 409 rather than
  creating a second meeting. When the answer never arrived, the failure
  is `[ambiguous_outcome]` naming the id to read.
- `cancel_event` deletes a whole event and marks one occurrence
  cancelled, and the result says which of the two happened. It is not
  behind `GCAL_ENABLE_DESTRUCTIVE` — a gate everybody turns on protects
  nobody — but it is annotated destructive, so a client can still decide
  to ask. With `notify: none` the result says the guests keep the
  meeting, because deleting quietly removes it from your calendar and
  leaves it on theirs.
- `update_event` with `scope: this_and_following` performs the two-call
  pattern Google has no operation for: the original series is ended
  before the target occurrence and a new one starts at it. The result
  says both rules, that it was two calls, and that exceptions after the
  target were reset — which Google does and no caller expects.
- An occurrence can be addressed as the series id plus `original_start`,
  its scheduled start. That address survives somebody moving the
  occurrence, which its own id does not tell you.

- The live driver cancels events that have guests instead of deleting
  them silently. Its cleanup used `sendUpdates=none`, which removes an
  event from the organiser's calendar and leaves it on everyone else's —
  so a day of probe runs left meetings on two real calendars that nobody
  could get rid of. `-sweep-spikes` removes the kept spike events the
  same way, cancelling them to their guests.
- The live driver no longer mails anyone unless asked. Spikes A and B
  sent four real invitations on every run with guest addresses
  configured, which a phase that runs the driver dozens of times would
  have turned into dozens of invitations to a colleague. They need
  `-spike-notify` per run, like `-spike-ceiling`, so a variable left in
  a shell profile cannot do it by accident.
- `schema-diff` has a baseline before the first tag. It compared the
  tool surface against the last git tag and there is no tag, so it
  printed "no previous tag" and passed on every run since the project
  started — inert through exactly the phases that add the most tools. It
  now falls back to `testdata/schema-baseline.json`, written by
  `make schema-baseline`.
- Spike B is answered, and §4.3 refuses `none` when a guest is outside
  the organiser's domain. A non-Google guest invited with `none`
  received nothing, in a run where the same address had just received
  two other invitations — and such a guest has no Google Calendar for
  the event to appear in, so mail was the only way they could learn of
  it. The event exists with them attached and they cannot discover it
  (§18 row 44).
- A result says what the server asked for, never what a guest received.
  `all` looked like it did not reach out-of-domain guests — three runs,
  both orderings, the same answer. Putting a non-Google address on the
  same events showed one send at two receivers: it arrived at one and
  not the other, so Google sent it and the receiving provider dropped
  it. Three consistent runs were consistent because the instrument was
  (§18 row 42).
- Spike A is answered: `externalOnly` follows the organiser's domain,
  and `sendUpdates=none` mailed nobody on insert. The second does not
  soften §4.3 rule 3 — Google warns mail "might still be sent", so one
  silent run is not a promise of silence — but it does retire the fear
  that `none` is routinely noisy (§18 rows 40 and 41).
- Spikes A and B, which set up §15's notification questions and
  deliberately do not answer them: who received mail is visible in an
  inbox and nowhere in any API response, so the driver creates the
  events, says what to look for, and the verdict is written down by
  hand. Guest addresses come from `GCAL_LIVE_GUEST_INTERNAL`,
  `GCAL_LIVE_GUEST_EXTERNAL` and `GCAL_LIVE_GUEST_NONGOOGLE` and never
  enter the repository.
- Spikes E and F, and both are answered. **E**: "this and following"
  resets exceptions after the target, confirmed on a real series — so
  §4.2's warning is accurate and phase 2 must print it. The split came
  from `Set.Split` in `internal/recur`, which makes this the first live check of
  phase 1's scope arithmetic. **F**: two concurrent inserts of one
  client-generated id gave one 200 and one 409, so the collision is
  caught; `ambiguous_outcome` stays, because the API declines to
  guarantee that and the class also covers a retry after a transport
  failure.
- The live driver reuses its scratch calendar. `-keep` leaves it and the
  next run adopts and empties it, spending no calendar-creation quota —
  which matters because that quota counts creations and is not refunded
  by deleting. Event ids are generated per run, since a deleted event
  does not release its id.

- Phase 1: recurrence and availability.
- `list_instances` — the occurrences of one repeating event, with the
  dates that were moved and, on request, the ones that were cancelled. A
  cancelled occurrence is how a single date leaves a series, so the
  result says when it is hiding them.
- `check_availability` — busy intervals and free gaps from
  `freebusy.query`, not from a list of events: a list misses everything
  whose details the caller cannot read and ignores events marked free. A
  calendar that could not be read is reported unknown and never folded
  into free, and the free gaps say how many calendars they were computed
  from. `min_minutes` drops gaps too short to use.
- `internal/recur`, the recurrence model: RFC 5545 rules parsed once for
  the whole server, EXDATE and RDATE, prose explanations, and the three
  scopes of §4.2 with the "this and following" arithmetic phase 2 needs.
  Expansion walks dates and carries the wall clock, so a weekly 09:00
  stays 09:00 across a daylight-saving transition; the table tests cover
  both hemispheres and Lord Howe's 30-minute shift.
- Free-gap arithmetic in `internal/model`, beside the busy intervals it
  works on. Overlapping and touching busy blocks merge, so no gap is
  reported between two meetings that run into each other.
- Two gates this repository claimed to have and did not: `api-fields`,
  one verdict per published field of `Event`, `Calendar`,
  `CalendarListEntry` and `AclRule` (81 fields, 56 modelled, 25 written
  off), and `live-cover`, which fails on a tool with no step in the live
  driver. `make check` is nineteen targets.
- Golden files for the renderers, in `testdata/golden/`, which §13 asked
  for and phase 0 left as an empty directory. `go test ./internal/render
  -update` rewrites them.
- Live driver: steps for both new tools, and spikes C (a recurrence
  written with no time zone), H (free/busy on a calendar nobody can
  read) and I (the 50-calendar ceiling at 51). None of the three has run
  yet.

### Changed

- `check_availability` has its own ceiling of 100 calendars per call
  rather than `GCAL_MAX_CALENDARS`: free/busy answers for 50 calendars
  in one request where a schedule read spends one per calendar. The
  refusal says which limit it is.
- Recurrence rules are explained by `internal/recur` rather than by a
  second parser in the renderer, so a rule reads the same way in a
  result and in a guard. "every 2 weeks on Monday and Wednesday" instead
  of "on Monday, Wednesday", and ordinals ("2nd Tuesday", "last day").

- Phase 0: the read surface, the repository's own gates, and the design.
- Six tools: `list_calendars`, `get_calendar`, `list_events`,
  `search_events`, `get_event`, `get_settings`.
- `login`, `logout`, `status` and `doctor` subcommands. Loopback OAuth on
  the 127.0.0.1 literal with PKCE S256, refresh token to the OS keyring
  with a warned 0600 file fallback, and `--no-browser` for signing in
  over SSH.
- `internal/when`, the time model: an all-day event is a date and never
  becomes an instant, and every timed event carries an IANA zone rather
  than just an offset. Table tests cover daylight-saving transitions in
  both hemispheres, a 30-minute transition, a non-hour offset and a zone
  with no transitions at all.
- Zone resolution in a fixed order — the call, the calendar, the
  account's settings — which refuses rather than falling back to the
  machine's own zone. Every read names which source it used.
- A closed error vocabulary of twelve classes, held from both sides by
  `make classes`.
- A verdict for all 38 published Calendar v3 methods in
  `testdata/api-coverage.tsv`, held by `make api-coverage` against the
  committed discovery snapshot.
- The repository's own gates as Go: coverage floor per package, error
  classes, API coverage, leak scan, transcript redaction, workflow pins,
  make/CI parity, stdio smoke, schema diff and staleness.
- `internal/redact` and `scripts/livecal`, the live driver. It creates a
  scratch calendar, reads only what it wrote, and deletes it; every
  print goes through one redactor, which the transcript gate holds.

### Fixed

- **The packer could never have staged a macOS binary.** Its glob was
  `dist/*darwin*universal*/`, and goreleaser names that directory
  `<id>_darwin_all` — the id comes first, so the glob matched nothing.
  Every gate was green, because the packer only runs at release time and
  there was no release to run it. `make release` reads the build matrix
  now, so the two cannot drift again.
- The bundle carried no version in its filename, and the binary inside it
  reported a different version from the manifest beside it. The ldflags
  stamped `{{ .Tag }}`, which is `v0.0.0` in a rehearsal while everything
  else carries the snapshot version — so the one place a person would
  check was the one place that could not be checked until a tag existed.
  Both come from `{{ .Version }}` now, and a snapshot build agrees in all
  five places.
- The staleness gate could not see a root file. Its path extractor only
  matched `cmd/`, `internal/`, `scripts/`, `docs/`, `testdata/`,
  `packaging/` and `.github/`, so `.goreleaser.yaml` named in the docs
  was not unchecked but UNSEEN — half of why §16 could list a release as
  built while no such file existed. It matches root files by shape now.
  Deriving the roots from the repository instead was tried and reverted:
  a token counts as a path only if its root exists, so a missing file
  files itself as prose and excuses itself (§18 row 70).
- README's Status section still said "Phase 0: the read surface. Six
  tools" with four phases built and twenty tools published. The staleness
  gate reads the tool table, the settings and the documented paths; a
  claim in prose that names no path is invisible to it, which is the same
  blind spot that let the missing release scaffolding sit in §16 as done.
- `manage_calendar` could report a change it had not made. Both halves
  listed their changes before sending them, and the sentence that says
  what stood when the second half failed is built from that list — so a
  rename refused as `[stale]` was reported as having landed, with no
  rollback available for something that never happened.
- `get_calendar` answered `[forbidden]` and nothing else for a calendar
  this account does not own. Reading who a calendar is shared with needs
  OWNER access, and only a missing SCOPE was being turned into a note.
  The card is a successful read either way, and now says which of the two
  kept the sharing list out of it.
- A forced `cancel_event` or `respond_to_event` did not say it was
  forced. Both take `force`, both then write under `If-Match: *`, and
  only `update_event` and `move_event` carried the sentence saying a
  change somebody else made was overwritten unseen.
- A dry-run `create_event` with `conference: true` reported a conference
  with no video link in its structured half. That is a real state an
  event can be in — a phone-only or third-party conference — and not
  this one: a dry run wrote nothing.
- Above 200 guests the warning counted the wrong people. Google's limit
  is on its own attendees field, which carries the organiser and the
  rooms, so an event Google had already stopped tracking could go
  unqualified while the same result printed a larger number beside it.
- `gcal://calendars/{calendar_id}/events/` — an event URI with an empty
  id — answered with the calendar card and its sharing list rather than
  refusing. A client building a URI from an empty id got a different
  resource back, with no error.
- A dry-run `manage_calendar` changing both the shared title and your own
  name for a calendar you had already renamed printed your PREVIOUS
  private name as the one everybody else sees.
- An all-day event made a calendar read `busy 2026-03-20 00:00-00:00` in
  `check_availability` — a block of no length, on the one kind of event
  that occupies the whole day. Free/busy reports such an event as
  midnight to midnight, and the busy list printed the end time without
  its date; the free gaps beside it already carried that rule. Found by
  reading a live transcript, which is the only place it showed.
- `delete_calendar` would have been refused on every call. A calendar is
  two resources with two etags — the calendar and your subscription to
  it — and one field carried whichever of them the last read produced, so
  the wrong one went out under `If-Match` and came back 412, which the
  caller reads as somebody else having edited it. The two are separate
  fields now, and each write reads the version it is held to immediately
  before making it.
- Renaming a calendar you had renamed for yourself erased your own name
  for it, and the result said "you renamed this; others see" followed by
  the same words twice. The name you give a calendar is a different field
  from its title and survives a rename of the calendar.
- A calendar created in a session could not be found by its name for the
  rest of that session. `create_calendar` did not add it to the cached
  calendar list, so resolving it by the title the same call had just
  given it answered "no calendar called that".
- A `manage_calendar` call that changed the calendar and then failed to
  change your own view of it returned a bare error. It says which change
  already stands, because there is no rollback and a retry would
  otherwise redo it — and never says it under `dry_run`, where nothing
  landed.
- A dry run of "subscribe to this calendar and set my colour on it"
  failed, telling the caller to pass `subscribe: true`, which they had.
- A dry run showed the calendar's old title above a change list saying
  the title changed. Both halves describe the same plan now.
- `manage_calendar` offered an unsubscribe that could not work on a
  calendar you made. Google refuses to let a calendar's data owner
  remove it from their own list, which nothing published says and the
  first live run found. The refusal now names the two things that do
  work: `hidden: true` keeps it out of your way, `delete_calendar`
  removes it for everybody.
- Being refused the sharing rules because you do not own the calendar
  was reported as a missing OAuth scope, with advice to log in again
  that could not have helped. A missing scope and a refusal are
  different answers and now read differently.
- A successful `move_event` reported the event as cancelled. Google's
  move answers with `status: cancelled` while the event sits confirmed on
  its new calendar, so the result said the opposite of what had happened
  — in the one word a caller acts on. The event is read back from the
  destination now.
- `move_event` is made under `If-Match` like every other write. It was
  not, because `events.move` is a POST with no body and nothing Google
  publishes says the header applies; asking the API directly, a stale
  etag is refused with 412. It takes `etag` and `force` now.
- A write reaching only the signed-in account demanded a notification
  choice. Google does not set its `self` flag on the account's own
  attendee row on a secondary calendar, so the caller counted as their
  own guest.
- A cancellation `dry_run` said "Deleted the event" under the words
  "nothing was written".
- `list_instances` returned the occurrences in whatever order Google sent
  them, which is not date order: a cancelled 24 March came back after
  7 April. They are sorted now, after the budget cut rather than before
  it, so which occurrences come back is unchanged and only their order
  differs.
- Passing `notify` on a write with no guests reported "Asked Google to
  notify nobody, of 0 guests", which is a warning about nothing.
- `move_event` applied the recurrence scope to the wrong event.
  `scope: series` on an occurrence id moved that one occurrence while the
  result said it had moved the series, and `scope: instance` on a series
  id moved every occurrence. `respond_to_event` had the matching gap: it
  would have answered for a whole series when told to answer for one
  occurrence. Choosing a scope and applying it now have one owner.
- An event's end was compared to its start as text. Either side of a
  daylight-saving change two timestamps carry different offsets, so a
  45-minute event across the autumn fold was refused as ending before it
  began, while one that genuinely did was accepted and sent to Google.
  They are compared as instants.
- Passing an `etag` together with `scope: series` was refused as
  `[stale]` every time, and re-reading returned the same etag it had just
  rejected. The etag is a statement about the event you read; the write
  may land on that event's series, and those are now two different
  checks.
- `notify: none` was refused for a colleague on any shared calendar. Such
  an event is organised by the calendar rather than by a person, and its
  id has a domain of its own, so everybody counted as outside the
  organisation.
- A write refused because somebody else had edited the event was reported
  as the event being already cancelled, sending the caller to look for
  something deleted instead of re-reading and trying again.
- `dry_run` on `update_event` and `cancel_event` showed the event
  unchanged where it should show what the change would make of it — a
  cancellation showed the event alive under the words "Deleted the
  event".
- `api_requests` did not count what a call spent resolving the calendar
  and the time zone, so a create reported one request and made four, and
  a dry run reported none while making three.
- The first of the two writes behind `scope: this_and_following` — the
  one that removes the later occurrences from everybody's calendar — was
  sent with no notification choice at all, while the result reported the
  choice the second one carried.
- A `this_and_following` split dropped the conference link from the new
  series without saying so. It still drops it, because this server does
  not write conference data yet, but the result now says it did.
- The in-memory Calendar used by tests now resolves Google's `primary`
  alias. It never did, and nothing noticed because the fixture gave a
  calendar the literal id `primary` — which no real account has, since a
  primary calendar's id is the account's email address. The first write
  test against a realistic fixture failed with "no calendar with that
  id". The same fixture used event ids containing hyphens, which
  base32hex forbids, so an occurrence address could not be exercised
  against it at all.

- The plaintext token fallback is restricted on Windows, where it never
  was. Go's file modes do not map to Windows ACLs, so a file written
  0600 landed at 0666 — readable by any account on the machine — while
  both warnings said "mode 0600" on every read and every save. The file
  now carries an explicit access list granting only the signed-in
  account, replacing what it inherited rather than adding to it, and the
  warnings describe the mechanism that actually applies (§18 row 47).
- `make check` passing locally did not mean passing on a clone. Three
  directories named in the docs existed only as empty ones, which git
  does not carry, so the staleness gate had been checking a tree nobody
  else could have. It now requires a path that does not exist to name
  the phase that builds it. Windows also checked the tree out with CRLF,
  which made gofmt list every file; a `.gitattributes` pins eol=lf.

- `externalOnly` splits on the organiser's Workspace domain, not on the
  guest's calendar system, so `dry_run` reports how many guests are
  outside that domain. Google documents the parameter as "notifications
  are sent to non-Google Calendar guests only"; spike A gave one guest
  inside the domain and one outside it, both on Google Calendar, and
  `externalOnly` mailed the outside one and skipped the inside one. The
  documentation is wrong about its own parameter, and the design said so
  first (§18 row 40).

- `list_instances` refuses an occurrence id instead of answering with an
  empty series. It used to explain the mistake only when Google returned
  an error; Google returns 200 and expands whatever occurrence the id
  names, and a cancelled one expands to nothing — so the tool reported
  "No occurrences" for a series that has three. An event id is base32hex
  and cannot contain an underscore, so the occurrence shape is
  recognised before the call and refused with the series id to use.
- A cancelled occurrence no longer appears in a series listing. Google
  documents that `showDeleted=false` does not filter cancelled instances
  when recurrences are not expanded, and it sends them with no start and
  no summary — so `list_events` with `no_expand` showed a row with no
  date and no title, and counted it. Cancelled events are filtered by
  the server now rather than by the parameter.
- Continuing a truncated multi-calendar read lost events. A Google page
  token is scoped to one calendar, and `list_events` kept whichever
  calendar produced one last and handed that single token back for all
  of them — so a continuation resumed the wrong calendar from an
  unrelated offset and dropped the others' pages, while the result said
  it was resumable. `next_page_token` now carries one token per
  calendar, and names only the calendars with more to read. It is still
  one opaque string, so nothing about the tool surface changed.
- The event budget was applied twice on a multi-calendar read, once per
  calendar and again to the combined list. The second cut discarded
  events the page token had already moved past, so they were reachable
  from nowhere. The budget is shared out across the calendars before
  they are read, and nothing fetched is thrown away.
- `search_events` accepts `page_token`. It was rendering "Pass
  page_token to continue" for a parameter it did not have.
- An impossible time zone is refused as `[invalid]`, not
  `[unavailable]`. The latter is retryable, so a caller was told to
  retry a request that could never succeed.
- An all-day event sorts to the front of its day east of UTC. Timed
  events were ordered by their UTC instant and all-day events by their
  local date, while the renderer groups by local date — so in
  Asia/Tokyo a day rendered `08:00`, `all day`, `10:00`.
- The event-id rule has one owner. `gcal.ValidEventID` and
  `gcal.SplitOccurrenceID` replace a copy in the live driver and a
  second, separate copy in the service, each of which depended on the
  other's premise.
- `doctor` could print an access token. The tokeninfo URL carries the
  token as a query parameter, and a transport failure stringifies the
  whole URL into the error `doctor` reports — which is the output a user
  pastes into a bug report. The URL no longer survives the error.
- The live driver never prints the body of a result that reaches past
  the calendar it created. `list_calendars` is account-wide, and the
  redactor matches shapes — an address, an id, a URL — while a calendar's
  display name has none, so a dozen real ones reached the transcript.
  Which steps are safe is derived from each step's own arguments against
  the ids the driver invented, not set by hand: the first draft carried
  a per-step flag and missed `get_settings` the same day it was written.
- Spike I's readable half is built, behind `-spike-ceiling`, and answers
  with the reason it cannot answer: Google refuses to create more than 38
  calendars on an account (403 `quotaExceeded`), so a free/busy query of
  51 readable calendars cannot be assembled. The flag stays off by
  default — the limit counts creations and deleting them does not refund
  it, so running the probe spends the quota the driver's own scratch
  calendar needs.
- Spike I stated a verdict it had not established, twice. It sent one
  calendar id 51 times, and Google deduplicated the response to one
  entry, which the spike called a silent truncation at the ceiling. Its
  second draft sent 51 distinct ids but also set `calendarExpansionMax`
  to 50 — so a trimmed response would have been the driver's own cap
  reported as Google's. It now sends distinct ids with no cap, and says
  what it cannot settle.
- The in-memory Calendar reproduces Google where it had been guessing:
  `events.instances` on an occurrence id answers 200 with that
  occurrence rather than 400, and a cancelled instance survives a
  non-expanded list. Both are why the two defects above had no test.

- `check_availability` spent an HTTP request per calendar reference
  before it asked anything. The calendar list is cached once, hidden
  calendars included, and an address is used as the id it already is —
  free/busy is the one read that works on a calendar this account cannot
  open. Asking about 55 calendars went from 168 requests to 4, and the
  count the result reports is now the count it spends.
- `check_availability` reported free time it could not know about. With
  every calendar unknown, the text said no free time could be computed
  while the structured half offered the whole window as free. The
  service decides now, so both halves say the same thing.
- `list_instances` showed fewer marks than `list_events` on the same
  event. The two line renderers had separate tag lists and the
  occurrence view had stopped showing four of them: out-of-office, an
  invented end time, a guest list Google truncated, and an event that
  does not make anybody busy.
- An `UNTIL` that is an instant is honoured on an all-day series. The
  timed path applied it and the all-day path ignored it.
- Recurrence expansion: six defects, all found by review after the
  tests were green.
  - An `RDATE` is filtered by the window, the exclusions and the limit
    like any other occurrence. They were appended after the walk, so a
    date decades outside the requested window came back inside it.
  - `BYDAY` is a set, not an order. The weekly walker yielded its days
    in the order they were written, so `BYDAY=WE,MO` and `BYDAY=MO,WE`
    — the same rule — gave different answers.
  - `BYDAY` and `BYMONTHDAY` on a monthly rule intersect, as RFC 5545
    says: "the 13th, when it is a Friday", not whichever was read first.
  - A yearly rule honours `BYDAY`. It was never read, so "the fourth
    Thursday in November" silently used the start's day of the month.
  - A series that ends exactly at the read limit is complete, not
    truncated. It was reported as endless, in the refusal text a scope
    decision is made from.
  - "This and following" counts what the rule generates, not what is
    visible. An excluded date made the truncated series lose an
    occurrence; an added one made it overlap the new series.
- A read that stops at its budget with more to come says so and hands
  back a page token. `list_events` decided truncation from the overflow
  alone, so a read that stopped exactly at its budget reported itself
  complete; `list_instances` asked for a whole page, kept `max_events`
  of it and returned that page's token, skipping everything in between
  while claiming to be resumable.
- A free gap that crosses midnight shows both dates. `17:00-09:00` read
  as ending before it began.
- `login` reports which account signed in. `tokeninfo` returns an email
  only when an email scope was granted and this server asks for none, so
  the account was blank and `status` had a field that could never
  populate. It now comes from the primary calendar's id.
- `status` lists the scopes Google granted, not the ones login asked
  for. The two differ exactly when a scope was refused, which is the
  case worth seeing.
- Tests can no longer reach the real OS keyring. A test redirected the
  config directory and the environment, could not redirect the keyring,
  and revoked the maintainer's own Google grant. `TestMain` substitutes
  it for the whole `cmd` package, with a decoy test holding that.

### Notes

- The phase plan, and what is still owed, are in `docs/architecture.md` §16.
- **Verified live** against a real Workspace account on every phase;
  the last run was 85 steps with none failing, transcript read. Spike D
  confirmed all-day stability from UTC-10 and UTC+13; a weekly series
  held its wall clock across the 29 March transition.
- Spike G confirmed its positive half. The negative half is not probed
  on purpose: Google's grant is per OAuth client and user, so proving a
  refusal would mean revoking this server's access and logging in again
  to re-confirm what the discovery document states. §18 row 24 has the
  reasoning and why the consequence is contained.
