# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml,
# and `make parity` asserts that rather than this comment claiming it.

GO        ?= go
# .exe on Windows, where a file without one cannot be executed at all.
EXE       := $(if $(filter Windows_NT,$(OS)),.exe,)
BIN       ?= ./google-calendar-mcp$(EXE)
VERSION   ?= dev
PKG        = github.com/mmedum/google-calendar-mcp
LDFLAGS    = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
# The gates are one binary. Building it once and running it saves a link
# per gate, which is several seconds on every `make check`.
GATES     ?= ./.gates$(EXE)

# The tools, pinned to the versions CI uses and fetched the way CI
# fetches them — not whatever is on the PATH. A distribution's
# golangci-lint built with an older Go refuses this module outright, and
# says so as "can't load config", which names the wrong thing.
GOLANGCI_LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
GOVULNCHECK   ?= golang.org/x/vuln/cmd/govulncheck@v1.7.0
GOLICENSES    ?= github.com/google/go-licenses@v1.6.0
# The module path is zricethezav, not gitleaks: the project moved
# organisation and the module path did not follow it.
GITLEAKS      ?= github.com/zricethezav/gitleaks/v8@v8.30.1

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
api-fields: gates ## Every published field is modelled or written off
	@$(GATES) api-fields

.PHONY: api-diff
api-diff: gates ## Refetch the discovery document and rewrite the snapshot (network; manual)
	@$(GATES) api-diff

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
schema-diff: build gates ## Diff the tool schemas against the last tag
	@$(GATES) schema-diff $(BIN)

.PHONY: smoke
smoke: build gates ## Drive the binary over stdio
	@$(GATES) smoke $(BIN)

.PHONY: staleness
staleness: build gates ## The docs must match the code
	@$(GATES) staleness $(BIN)

.PHONY: transcript
transcript: gates ## The live driver prints only through its redactor
	@$(GATES) transcript

.PHONY: live-cover
live-cover: build gates ## Every published tool has a step in the live driver
	@$(GATES) live-cover $(BIN)

.PHONY: live
live: build ## Drive the built binary against a real account (see docs/development.md)
	$(GO) run -tags=live ./scripts/livecal -bin $(BIN)

.PHONY: check
check: fmt vet tidy lint cover vuln licenses secrets api-coverage api-fields classes leaks transcript live-cover parity pins schema-diff smoke staleness ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) $(GATES) cov.out schemas.json
