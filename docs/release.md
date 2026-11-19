# Releasing

A maintainer releases by pushing a tag. `release.yml` builds, signs and
publishes from it, and the result is a published release, never a draft.

The tag produces the archives, their SBOMs and the `.mcpb` bundle, all
covered by `checksums.txt`, which cosign signs. The workflow attests
provenance, and `publish-mcp.yml` then publishes the MCP registry entry.

## Before the release commit

- `make live`, with the transcript read, and the checks below that it
  cannot make.
- `make api-diff`. A method or field it adds fails the API gates until
  it has a verdict.
- `make schema-refetch`, the only step that checks the vendored schemas
  against what upstream serves.
- `make release-rehearse` builds the release unsigned under `dist/`, and
  `make release-notes VERSION=Unreleased` prints the release page.

## Checks the live driver cannot make

- Install the release's `.mcpb` in Claude Desktop, after `login`, and
  call `list_calendars`.
- Call a read from a second MCP client.
- When a change touches who a write reaches, run the driver armed with
  guests you control (`CONTRIBUTING.md`). It mails them.
- When a change touches the driver's cleanup, stop one run with Ctrl-C
  and check the status event is gone.

These need a second account, or a group, that you control. Run each when
a change touches what it checks:

- **A public event on a free/busy share (D-92).** Share a scratch
  calendar for free/busy with the second account, make an event there in
  Google Calendar with visibility public, and look at it from the second
  account. If its details show, D-92's reading holds; if only busy shows,
  free/busy readers do not count, and `PublicReaders` refuses too much.
- **A move to a wider calendar (D-91).** With the second account outside
  the organization reading a scratch calendar, `move_event` an event into
  it from a calendar it cannot read, and check the refusal names it.
- **A group with an outside member (D-51).** With a group that has the
  second account, outside the organization, as a member, call
  `share_calendar` on a scratch calendar with that group, and check the
  refusal counts the member. Call `check_availability` on the group and
  check it reports the group's calendar count, since the check reads the
  same expansion.
- **A group past 50 calendars (D-34).** With a group of more than 50
  members, call `check_availability` on it and check every member is
  answered or reported unknown.
- **A widened decline-all event (D-44).** On the second account, with a
  meeting accepted next week, make an out-of-office event that declines
  all beside it with `create_event_declining_all`, then call
  `update_event` to lengthen it over the meeting. Check the refusal, and
  that the meeting is still accepted.
- **A home working location.** Call `create_event` with event_type
  `workingLocation` and a home location on the primary calendar of the
  second account, then `get_event`, and check the body Google kept.

## Steps

1. Land a `Release X.Y.Z` commit on `main`. It renames `[Unreleased]` to
   `## [X.Y.Z] - YYYY-MM-DD`, leaves an empty `[Unreleased]` above it,
   and runs `make schema-baseline VERSION=vX.Y.Z`, without which the
   schema diff fails.
2. Wait for `ci.yml` to pass on that exact commit. The `verify-ci` job
   refuses a tag whose commit is not green.
3. Tag it with what shipped, and push the tag on its own:

   ```bash
   git checkout main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z: what shipped"
   git push origin vX.Y.Z
   ```

   Push one tag at a time: GitHub creates no tag events past the third
   in one push. If the release never starts, run `release.yml` by hand
   against the tag.

Before goreleaser runs, `gates release-tag` refuses a tag whose major is
not go.mod's, and `gates release-notes` a version with no CHANGELOG
section. A prerelease skips the registry, whose entries cannot be taken
back.

## What no rehearsal reaches

Signing, attestation and the registry publish need a workflow's OIDC
token, so read the first run after any change to the pipeline.

| Fails | State afterward | Recovery |
|---|---|---|
| cosign | no release; signing precedes publish | fix it, then release the next patch version |
| attestation | release published, unattested | re-run the failed job |
| registry publish | release fine, no entry | `gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z`; do not tag again |

The rehearsal also skips the SBOMs, which need syft, so a broken
`sboms:` block fails only the tag. And `--snapshot` skips goreleaser's
dirty-tree check, so keep the `before` hook `go mod download` and the
notes file outside the checkout.

## Verify from outside

```bash
cd "$(mktemp -d)" && gh release download vX.Y.Z --repo mmedum/google-calendar-mcp
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/mmedum/google-calendar-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-calendar-mcp_X.Y.Z.mcpb --repo mmedum/google-calendar-mcp
```

`gh attestation verify` prints nothing when it passes, so read its exit
code, not through a pipe. A corrupted copy of the bundle must fail.

The version must agree across the bundle's and archives' filenames, the
bundle's `manifest.json`, `--version` and `checksums.txt`. A bundle
missing from `checksums.txt` ships unsigned and looks no different.

Last, run the README's install command outside the repository, as a user
would, and check that `google-calendar-mcp --version` names the release.

## A bad release

A bad release is fixed forward.

- Never move or delete a pushed tag. The Go module proxy caches it within
  minutes, and the checksum database records its hash.
- Ship a patch release with the fix.
- In that patch, add a `retract` directive to `go.mod` for the bad
  version: `retract vX.Y.Z // why`. The go command then skips it for
  `@latest`.
- Mark the bad version's CHANGELOG heading `[YANKED]`:
  `## [X.Y.Z] - YYYY-MM-DD [YANKED]`.
