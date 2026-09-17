# Development

## The one command

```
make check
```

That is what CI runs, and `make parity` asserts those two lists are the
same rather than a comment claiming it. Three of the four servers in this
family had them diverged at one point, usually with the local one ahead —
which means build-tagged files compile only on a maintainer's laptop.

## The gates, and what each one is for

| Target | Holds |
|---|---|
| `make fmt` | gofmt would change nothing |
| `make vet` | `go vet`, including the `live` build tag so tagged files keep compiling |
| `make tidy` | `go.mod` and `go.sum` are what `go mod tidy` would write |
| `make lint` | golangci-lint, including `forbidigo` for the stdout rule |
| `make cover` | race tests, and an 80% floor **per package**, not on the average |
| `make vuln` | govulncheck |
| `make licenses` | the dependency licence allow-list |
| `make secrets` | gitleaks |
| `make api-coverage` | every published API method is used or written off, with a reason |
| `make api-fields` | every published field of the four main resources is modelled or written off, with a reason |
| `make classes` | the error vocabulary is closed **from both sides** |
| `make leaks` | no deployer-specific data in the tree |
| `make pins` | actions pinned to SHAs, tools pinned to versions |
| `make live-cover` | every published tool has a step in the live driver |
| `make parity` | `make check` and `ci.yml` run the same things |
| `make schema-diff` | the tool surface against the last tag |
| `make smoke` | the binary over stdio, and a clean exit on disconnect |
| `make mcpb` | the bundle manifest describes the bundle the packer stages |
| `make release` | `.goreleaser.yaml` builds what the packer stages, and signs and uploads it |
| `make staleness` | README, `docs/` and the code agree |

Two are manual because they need the network:

- `make api-diff` refetches the Calendar discovery document and rewrites
  `testdata/api-surface.json`. On a network failure it fails loudly and
  leaves the committed file untouched. Run it at release time; what CI
  holds is the committed snapshot.
- `make leaks-history` scans every blob and commit message. Run it before
  going public.

## Every gate asserts a floor

"Found nothing" and "looked at nothing" print the same sentence, so each
gate also asserts how much it read: a coverage profile with too few
statement lines, a leak scan over too few files, an API snapshot with too
few methods. A gate nobody has watched fail is not yet a gate — break one
deliberately and watch it before you trust it.

## Green gates are not done

Anything touching the write path or an API response shape gets a live run
before it counts, and **the transcript is read**. A sibling's driver
twice reported success while its results were wrong.

```
make live                     # every step, against the logged-in account
make live && read the output  # the part that matters
```

What it does to the account, so nothing is a surprise:

- It creates a scratch calendar, or **adopts and empties** one an earlier
  run left, and creates a second for the move steps. Calendar creation is
  quota-counted and deleting one does not refund it, so `-keep` leaves
  them for the next run: `go run -tags=live ./scripts/livecal -bin
  ./google-calendar-mcp -keep`.
- The calendar and sharing steps make a third calendar through
  `create_calendar` and remove it through `delete_calendar`, which costs
  one creation per run and is the only way to drive those two tools at
  all. The driver starts the server with `GCAL_ENABLE_DESTRUCTIVE=true`
  for them; every step that could reach a destructive tool names a
  calendar the driver created, and a step whose calendar was never
  created is skipped rather than called.
- **Nothing reaches another person unless you ask for it.** Every event
  the driver writes has no guests but the account itself, and the one
  address it shares a calendar with is in `example.test`, which cannot
  resolve. `-spike-notify` is what arms the steps and spikes that mail a
  real person, and they say so before they run.
- `-show <substring>` prints the redacted body of every step whose name
  contains it. A pass/fail line cannot show a result that is confidently
  wrong, which is how the last three phases each found a defect.

## Cutting a release

`main` is released code and the tag is the maintainer's. What the tag
does is in `.goreleaser.yaml` and `.github/workflows/release.yml`: six
platform archives, `checksums.txt`, an SBOM per archive, a keyless cosign
signature over the checksums, `actions/attest-build-provenance`, and the
`.mcpb` bundle, packed in the universal binary's post hook so it reaches
`checksums.txt` and therefore the signature.

Rehearse it first. This runs everything except the three steps that need
credentials, and leaves the whole tree under `dist/`:

```
go run github.com/goreleaser/goreleaser/v2@v2.18.1 release \
  --snapshot --clean --skip=publish,sign,sbom
make release-notes VERSION=Unreleased  # what the release page would say
```

At tag time the `[Unreleased]` heading becomes `[1.2.3]` and the same
command takes that version. `gates release-notes` fails on a version with
no section, so a tag pushed before the rename stops the release before
goreleaser runs — which is the right way round, but it means the entry
has to be written first.

Then **check the version in five places**, because four of them agreeing
is what a broken bundle looks like: the bundle's filename, the archive
filenames, `manifest.json` inside the bundle, the binary's own
`--version`, and `checksums.txt`. The bundle missing from that last file
is the failure to look for — it ships unsigned and looks no different.

Three things a rehearsal cannot tell you, so watch the first real run:

- **Signing and provenance need an OIDC token**, which only a workflow
  run has. `goreleaser check` and a full local build both pass while that
  step is wrong.
- **goreleaser refuses a dirty tree, and `--snapshot` skips that check.**
  This is why the `before` hook is `go mod download` rather than
  `go mod tidy`, and why the workflow writes its notes file outside the
  checkout.
- **Push tags one at a time.** GitHub drops tag events past the third in
  a single push, and the release simply never runs.

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

## What the first run after a pipeline change is for

Three steps need an OIDC token that only a real workflow run has, so no
rehearsal reaches them: the **cosign signature**, the **provenance
attestation**, and the **registry publish**. `goreleaser check`,
`actionlint`, `make check` and a full `--snapshot` build all pass while
any of the three is wrong.

Read that run rather than watching it go green, and know the recovery for
each, because they are not the same:

| Fails | State afterwards | Recovery |
|---|---|---|
| cosign | **no release** — signing precedes publish | fix, delete the tag, tag again |
| attestation | release published, unattested | re-run the failed job |
| registry publish | release fine, no entry | dispatch `publish-mcp.yml` with the tag |

Verify from outside afterwards, with the commands the release page itself
prints:

```
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/mmedum/google-calendar-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-calendar-mcp_*.mcpb --repo mmedum/google-calendar-mcp
```

An exit code of 0 on empty output is not evidence. Check the attestation
against a deliberately corrupted copy too — it must exit non-zero.

## Adding a tool

1. Add the handler in `internal/tools`, with a `Kind` that decides its
   annotations and whether it registers at all.
2. Put the logic in `internal/service`, not the handler. The handlers are
   thin so the rules are testable in one place.
3. Add it to the README's tool table, or `make staleness` fails.
4. Add a step to `scripts/livecal`, or `make live-cover` fails. A tool
   with no live step is the one nobody remembers to drive: the fake
   answers it, every test passes, and the first real call is a user's.
5. Look at `make schema-diff` for anything breaking.

## Adding an API call

`make api-coverage` fails on a client method with no verdict. Add a row
to `testdata/api-coverage.tsv` saying `used` with the method that
implements it, or `out` with why not.

## Adding a wire field

`make api-fields` fails on a field of `Event`, `Calendar`,
`CalendarListEntry` or `AclRule` that `internal/gcal` carries with no row
in `testdata/api-fields.tsv`, and on a row saying `out` for a field the
code actually reads. Fields come from the discovery document through
`make api-diff`, so a field Google adds arrives as a gate failure naming
it rather than as silence.

## Renderer golden files

`testdata/golden/` holds what each renderer prints. A change to how a
schedule reads shows up as a diff somebody has to look at:

```
go test ./internal/render -update
```

Regenerating is one flag; reading the diff is the part that matters.
