# Changelog

All notable changes to this project are documented here.

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

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

### Added

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

### Notes

- Writing events, recurrence, availability and sharing are phases 1 to 3.
  `docs/architecture.md` §16 has the plan.
- **Verified live** against a real Workspace account: 19 steps, none
  failed, transcript read. Spike D confirmed all-day stability from
  UTC-10 and UTC+13; a weekly series held its wall clock across the
  29 March transition.
- Spike G confirmed its positive half. The negative half is not probed
  on purpose: Google's grant is per OAuth client and user, so proving a
  refusal would mean revoking this server's access and logging in again
  to re-confirm what the discovery document states. §18 row 24 has the
  reasoning and why the consequence is contained.
- CI has not yet run on macOS or Windows.
