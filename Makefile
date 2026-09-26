# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml,
# and `make parity` asserts that rather than this comment claiming it.

GO        ?= go
# .exe on Windows, where a file without one cannot be executed at all.
EXE       := $(if $(filter Windows_NT,$(OS)),.exe,)
BIN       ?= ./google-calendar-mcp$(EXE)
VERSION   ?= dev
PKG        = github.com/mmedum/google-calendar-mcp/v2
LDFLAGS    = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
# Where the release's binaries are, what version the bundle claims, and
# where it lands. Only `mcpb-pack` reads them.
DIST      ?= dist
MCPB_OUT  ?= dist/google-calendar-mcp_$(VERSION).mcpb
# The gates are one binary. Building it once and running it saves a link
# per gate, which is several seconds on every `make check`.
GATES     ?= ./.gates$(EXE)

# The tools, pinned to the versions CI uses and fetched the way CI
# fetches them — not whatever is on the PATH. A distribution's
# golangci-lint built with an older Go refuses this module outright, and
# says so as "can't load config", which names the wrong thing.
GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.8.0
GOLICENSES    ?= github.com/google/go-licenses@v1.6.0
# The module path is zricethezav, not gitleaks: the project moved
# organization and the module path did not follow it.
GITLEAKS      ?= github.com/zricethezav/gitleaks/v8@v8.30.1
# The release runs goreleaser through goreleaser-action, which pins its
# own copy. `pins` holds this version against that one: a rehearsal on a
# different goreleaser is not a rehearsal.
GORELEASER    ?= github.com/goreleaser/goreleaser/v2@v2.18.1

.PHONY: all
all: check

.PHONY: build
build: ## Build the binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/google-calendar-mcp

.PHONY: install
install: ## go install the binary
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/google-calendar-mcp

.PHONY: fmt
fmt: ## Fail if gofmt would change anything
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet, including the tagged tests so they keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=live ./...
	$(GO) vet -tags=evals ./...

.PHONY: lint
lint:
	$(GO) run $(GOLANGCI_LINT) run

.PHONY: test
test: ## Unit tests with the race detector and coverage
	$(GO) test -race -coverpkg=./internal/...,./cmd/... -coverprofile=cov.out -covermode=atomic ./...

.PHONY: gates
gates: ## Build the repository's own checks
	@$(GO) build -o $(GATES) ./scripts/gates

.PHONY: cover
cover: test gates ## Enforce the coverage floor per package
	@$(GATES) coverage cov.out $(COVER_MIN)

.PHONY: tidy
tidy: ## go.mod and go.sum are what `go mod tidy` would write
	$(GO) mod tidy -diff

.PHONY: vuln
vuln:
	$(GO) run $(GOVULNCHECK) ./...

.PHONY: licenses
licenses:
	$(GO) run $(GOLICENSES) check ./... --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC

.PHONY: secrets
secrets: ## Credentials, as CI scans for them
	$(GO) run $(GITLEAKS) dir . --config .gitleaks.toml --no-banner

.PHONY: classes
classes: gates ## The error vocabulary, held closed from both sides
	@$(GATES) classes

.PHONY: api-coverage
api-coverage: gates ## Every published API method is used or written off
	@$(GATES) api-coverage

.PHONY: api-fields
api-fields: gates ## Every published field is modeled or written off
	@$(GATES) api-fields

.PHONY: api-diff
api-diff: gates ## Refetch the discovery document and rewrite the snapshot (network; manual)
	@$(GATES) api-diff

# The half a vendored schema cannot do for itself: a digest says these
# bytes are the ones somebody reviewed, not that upstream still serves
# them. Manual, and read at release time — docs/release.md runs it.
.PHONY: schema-refetch
schema-refetch: gates ## Check the vendored schemas against what their sources serve (network; manual)
	@$(GATES) schema-refetch

.PHONY: leaks
leaks: gates ## Identifiers and data in the working tree
	@$(GATES) leaks

.PHONY: leaks-history
leaks-history: gates ## Every blob and message in the history; run before going public
	@$(GATES) leaks history

.PHONY: parity
parity: gates ## `make check` and ci.yml run the same things
	@$(GATES) parity

.PHONY: pins
pins: gates ## Actions pinned by SHA, tool versions exact
	@$(GATES) pins

.PHONY: schemas
schemas: build ## Dump the tool schemas
	$(BIN) --dump-schemas > schemas.json

.PHONY: schema-diff
schema-diff: build gates ## Diff the tool schemas against the last tag, else the recorded baseline
	@$(GATES) schema-diff $(BIN)

.PHONY: schema-baseline
schema-baseline: build gates ## Record the current tool surface as the baseline (deliberate; manual)
	@$(GATES) schema-baseline $(BIN)

.PHONY: smoke
smoke: build gates ## Drive the binary over stdio
	@$(GATES) smoke $(BIN)

.PHONY: staleness
staleness: build gates ## The docs must match the code
	@$(GATES) staleness $(BIN)

.PHONY: transcript
transcript: gates ## The live driver prints only through its redactor
	@$(GATES) transcript

.PHONY: mcpb
mcpb: gates ## The bundle manifest describes the bundle the packer builds
	@$(GATES) mcpb

.PHONY: release
release: gates ## The release config builds what the packer stages, and signs and uploads it
	@$(GATES) release

# The other half of the bundle, and the reason there are two subcommands:
# the gate above needs only the staged NAMES, which are static, so it
# runs on every commit. This one needs the binaries, so it runs at
# release time, from the universal binary's post hook in .goreleaser.yaml
# — which calls `go run ./scripts/gates` rather than this target, so a
# rehearsal needs nothing but the Go toolchain. `make release` holds the
# path it packs to against MCPB_OUT above. Deliberately NOT in `check`,
# and therefore not in CI, which is why `parity` does not see it.
.PHONY: mcpb-pack
mcpb-pack: gates ## Pack the .mcpb from a built dist tree (release; manual)
	@$(GATES) mcpb-pack $(DIST) $(VERSION) $(MCPB_OUT)

# The release, as far as a laptop can take it. Four things it does NOT
# do: the three that need an OIDC token only a workflow run has, and the
# SBOMs, which need syft installed rather than credentials — a broken
# `sboms:` block is green here and fails the tag. `--snapshot` also skips
# goreleaser's dirty-tree check. docs/release.md says what that costs.
.PHONY: release-rehearse
release-rehearse: ## Build the whole release locally, unsigned (manual)
	$(GO) run $(GORELEASER) release --snapshot --clean --skip=publish,sign,sbom

# The release body, from the CHANGELOG section for the tag. release.yml
# runs the gate directly and writes it outside the checkout; this target
# is for reading what a tag would publish before pushing it.
.PHONY: release-notes
release-notes: gates ## Print the CHANGELOG section a tag would publish (manual)
	@$(GATES) release-notes $(VERSION)

.PHONY: live-cover
live-cover: build gates ## Every published tool has a step in the live driver
	@$(GATES) live-cover $(BIN)

.PHONY: live
live: build ## Drive the built binary against a real account (see docs/development.md)
	$(GO) run -tags=live ./scripts/livecal -bin $(BIN)

# Not in `check`, deliberately: it spends money and it is not
# deterministic. Like `live`, it is run by hand and its transcript is
# read — a failure here is usually a tool description.
.PHONY: evals
evals: ## Score a model against the tool surface (needs ANTHROPIC_API_KEY; manual)
	$(GO) run -tags=evals ./scripts/evals $(EVAL_ARGS)

# The half of the evals that IS deterministic, hermetic and free: it
# builds each task's calendar, offers the tool list, and asserts every
# task fails on a calendar nobody touched. A scorer that passes there is
# scoring nothing, and without this it would be found by spending money.
.PHONY: evals-check
evals-check: ## The eval scorers discriminate, with no model and no key
	@$(GO) run -tags=evals ./scripts/evals -self-check

.PHONY: check
check: fmt vet tidy lint cover vuln licenses secrets api-coverage api-fields classes evals-check leaks mcpb release transcript live-cover parity pins schema-diff smoke staleness ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) $(GATES) cov.out schemas.json
