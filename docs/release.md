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
