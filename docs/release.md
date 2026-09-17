# Release runbook

`main` is released code and the tag is the maintainer's. This file is
release day; `docs/development.md` is every other day.

What the tag does is in `.goreleaser.yaml` and
`.github/workflows/release.yml`: six platform archives, `checksums.txt`,
an SBOM per archive, a keyless cosign signature over the checksums,
`actions/attest-build-provenance`, and the `.mcpb` bundle, packed in the
universal binary's post hook so it reaches `checksums.txt` and therefore
the signature. The release then calls `.github/workflows/publish-mcp.yml`
for the MCP registry entry, which is a workflow of its own and can be run
again without another tag.

## Rehearse it

This runs the release as far as a laptop can take it, and leaves the
whole tree under `dist/`:

```
make release-rehearse                  # the whole release, unsigned
make release-notes VERSION=Unreleased  # what the release page would say
```

It runs the goreleaser the tag runs: the Makefile pins the version and
`make pins` holds it against the one `release.yml` installs, so a
rehearsal cannot quietly be of a different tool.

At tag time the `[Unreleased]` heading becomes `[1.2.3]` and the same
command takes that version. `gates release-notes` fails on a version with
no section, so a tag pushed before the rename stops the release before
goreleaser runs — which is the right way round, but it means the entry
has to be written first.

## Before the tag

- `make check` on the commit being tagged, and CI green on it: the
  workflow refuses to publish a commit whose `ci` run was not green, and
  a tag is only a pointer.
- A live run of anything that touched the write path or an API response
  shape, **with the transcript read**.
- `make schema-diff`, read for anything breaking.
- `[Unreleased]` renamed to the version with the date, in a release-prep
  commit, saying exactly what shipped — with an empty `[Unreleased]`
  heading left above it, so day-2 commits have somewhere to land.
- The status line of `docs/architecture.md` says what has changed since
  the last tag and is therefore unproven again. That is the list to watch
  this run.

## Push the tag

```
git checkout main
git pull --ff-only
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

The tag is annotated, and its message is read by whoever finds the tag
rather than the release page: say what shipped, the way v1.0.0's does.

**Push tags one at a time.** GitHub drops tag events past the third in a
single push, and the release simply never runs.

## Then check the version in five places

Four of them agreeing is what a broken bundle looks like: the bundle's
filename, the archive filenames, `manifest.json` inside the bundle, the
binary's own `--version`, and `checksums.txt`. The bundle missing from
that last file is the failure to look for — it ships unsigned and looks
no different.

## And verify from outside

With the commands the release page itself prints:

```
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/mmedum/google-calendar-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-calendar-mcp_*.mcpb --repo mmedum/google-calendar-mcp
```

An exit code of 0 on empty output is not evidence. Check the attestation
against a deliberately corrupted copy too — it must exit non-zero.

## What no rehearsal reaches

Three steps need an OIDC token that only a real workflow run has: the
**cosign signature**, the **provenance attestation**, and the **registry
publish**. `goreleaser check`, `actionlint`, `make check` and a full
`--snapshot` build all pass while any of the three is wrong. So read the
first run after any change to the pipeline rather than watching it go
green, and know the recovery for each, because they are not the same:

| Fails | State afterwards | Recovery |
|---|---|---|
| cosign | **no release** — signing precedes publish | fix, delete the tag, tag again |
| attestation | release published, unattested | re-run the failed job |
| registry publish | release fine, no entry | dispatch `publish-mcp.yml` with the tag |

Three more things a rehearsal is quiet about:

- **The SBOMs.** `make release-rehearse` skips them, because they need
  syft installed rather than a token. A broken `sboms:` block is green
  on a laptop and fails the tag — and it runs before publish, so the
  release is not produced at all. `goreleaser check` reads the block's
  shape, not that it resolves.
- **goreleaser refuses a dirty tree, and `--snapshot` skips that check.**
  This is why the `before` hook is `go mod download` rather than
  `go mod tidy`, and why the workflow writes its notes file outside the
  checkout.
- **The bundle's own contents.** `make mcpb` holds the manifest against
  the staged tree on every commit, and the packer's tests open an archive
  it wrote — but nobody has installed one in Claude Desktop. Until
  somebody has, that is a claim rather than a fact.

## The registry entry, and how to recover it

The MCP registry entry is **not** part of the goreleaser job. It lives in
`.github/workflows/publish-mcp.yml`, which `release.yml` calls after the
release exists, and which is **dispatchable on its own with a tag**:

```
gh workflow run publish-mcp.yml --ref main -f tag=v1.2.3
```

That matters more than it looks. The registry sends a HEAD to the
bundle's download URL before it accepts an entry, so this step can only
run last — and a step that can only run last needs a way to be run again
without cutting another release. If the registry publish is the thing
that fails, re-dispatch it; do not tag again.

It also runs with `id-token: write` and `contents: read` and nothing
else, because `mcp-publisher` is a third-party binary handed a token that
can publish under `io.github.mmedum`. The binary is verified with cosign
against the registry project's own release workflow before it is
unpacked — a version pins which artifact to fetch, not that the bytes are
the ones upstream built.

`gates server-json` builds the entry from the release's **own**
`checksums.txt`, so the hash describes the bytes that were published. A
prerelease tag skips the step: an entry cannot be taken back, so
`v1.0.0-rc1` must not leave a permanent row pointing at a bundle nobody
should install.
