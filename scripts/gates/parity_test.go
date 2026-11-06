package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// Only a `run:` step runs a target, a one-line step or a block; a
// comment that names one does not, and neither does a vet tag in one.
func TestRunTextReadsOnlyRunSteps(t *testing.T) {
	ci := `jobs:
  pr:
    steps:
      - name: schema change acknowledged
        run: make schema-ack
      # make smoke and go vet -tags=live run in the test job
      - run: |
          make cover
          # make lint is its own job
          go vet -tags=integration ./...
      - name: after the block
        run: make fmt`
	run := runText(ci)
	for _, want := range []string{"make schema-ack", "make cover", "go vet -tags=integration ./...", "make fmt"} {
		if !strings.Contains(run, want) {
			t.Errorf("runText missed %q:\n%s", want, run)
		}
	}
	for _, not := range []string{"make smoke", "-tags=live", "make lint"} {
		if strings.Contains(run, not) {
			t.Errorf("runText read %q from a comment:\n%s", not, run)
		}
	}
}

// parityMakefile, parityCI and parityRelease agree, and each command in
// parityRegistry runs where it says. The tagged vet passes sit under a
// prerequisite of vet, and CI runs one tool itself at the version vuln
// runs it.
const parityMakefile = `GO ?= go
GATES ?= ./.gates
GOVULNCHECK ?= golang.org/x/vuln/cmd/govulncheck@v1.8.0

check: fmt vet tidy lint cover vuln docs pins ## Everything CI runs

fmt:
	gofmt -l .
vet: vet-tagged
	$(GO) vet ./...
vet-tagged:
	$(GO) vet -tags=live ./...
	$(GO) vet -tags=evals,e2e ./...
tidy:
	$(GO) mod tidy -diff
lint:
	golangci-lint run
cover: test
	@echo cover
test:
	$(GO) test ./...
vuln:
	$(GO) run $(GOVULNCHECK) ./...
docs:
	@$(GATES) docs
pins:
	@$(GATES) pins
schema-ack:
	@$(GATES) schema-ack $(PR_BASE_REF) $(PR_HEAD)
`

const parityCI = `jobs:
  test:
    steps:
      - run: make fmt
      - run: make vet
      - run: make tidy
      - run: make lint
      - run: make cover
      - run: |
          make vuln
          make docs
      - run: make pins
      - run: make schema-ack
      - run: go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -mode=binary ./bin
`

const parityRelease = `go run ./scripts/gates release-notes "${REF_NAME}" > notes.md`

var (
	parityRegistry = map[string]command{
		"docs": {runsIn: inCheck}, "pins": {runsIn: inCheck}, "schema-ack": {runsIn: inPR},
		"release-notes": {runsIn: inRelease}, "api-diff": {runsIn: manual},
	}
	parityTags = []string{"evals", "live"}
)

func TestParityAcceptsAMatchedPair(t *testing.T) {
	problems, summary, err := parityProblems(parityMakefile, parityCI, parityRelease, parityRegistry, parityTags)
	if err != nil || problems != nil {
		t.Fatalf("a matched pair gave %q, %v", problems, err)
	}
	const want = "8 targets in `make check`, all run by CI; 4 commands run where scripts/gates says; " +
		"a vet pass for -tags=evals, -tags=live on both sides"
	if summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}

// Each tag the tree requires is vetted on both sides, each tool CI runs
// itself at a version is one `make check` runs at that version, and each
// command runs in the pipeline it declares.
func TestParityCatchesDivergence(t *testing.T) {
	const (
		localLive = "a file builds only with -tags=live and `make check` vets no such pass; a contributor cannot see it break"
		ciLive    = "a file builds only with -tags=live and ci.yml vets no such pass; it compiles only on a maintainer's machine"
		ciEvals   = "a file builds only with -tags=evals and ci.yml vets no such pass; it compiles only on a maintainer's machine"
		noDocs    = "\"docs\" runs in make check and no `make check` prerequisite runs it"
	)
	for _, tc := range []struct {
		name                  string
		makefile, ci, release [2]string // old and new text, when the row edits that file
		registry              map[string]command
		tags                  []string
		want                  []string
	}{
		{name: "a tag the tree requires and no vet pass names", tags: []string{"evals", "integration", "live"}, want: []string{
			"a file builds only with -tags=integration and `make check` vets no such pass; a contributor cannot see it break",
			"a file builds only with -tags=integration and ci.yml vets no such pass; it compiles only on a maintainer's machine",
		}},
		{
			name: "a constraint needing two tags, set by one pass",
			tags: []string{"e2e,evals", "evals", "live"},
		},
		{
			name: "a constraint needing two tags, set by two passes",
			tags: []string{"e2e,live", "evals", "live"},
			want: []string{
				"a file builds only with -tags=e2e,live and `make check` vets no such pass; a contributor cannot see it break",
				"a file builds only with -tags=e2e,live and ci.yml vets no such pass; it compiles only on a maintainer's machine",
			},
		},
		{
			name:     "a tag vetted in check's own recipe, which CI does not run",
			makefile: [2]string{"## Everything CI runs\n", "## Everything CI runs\n\t$(GO) vet -tags=soak ./...\n"},
			tags:     []string{"evals", "live", "soak"},
			want: []string{
				"a file builds only with -tags=soak and ci.yml vets no such pass; it compiles only on a maintainer's machine",
			},
		},
		{
			name:     "a tagged vet commented out of its recipe",
			makefile: [2]string{"\t$(GO) vet -tags=live", "\t# $(GO) vet -tags=live"},
			want:     []string{localLive, ciLive},
		},
		{
			name:     "a tag only CI vets, itself",
			makefile: [2]string{"\t$(GO) vet -tags=live ./...\n", ""},
			ci:       [2]string{"      - run: make tidy\n", "      - run: make tidy\n      - run: go vet -tags=live ./...\n"},
			want:     []string{localLive},
		},
		{
			name: "CI not running the target that vets",
			ci:   [2]string{"      - run: make vet\n", ""},
			want: []string{"`make check` runs \"vet\" and CI does not", ciEvals, ciLive},
		},
		{
			// The commented-out run is not one CI makes.
			name: "a tool CI runs itself and make check does not",
			ci: [2]string{"      - run: make pins\n", "      - run: make pins\n" +
				"      - run: go run github.com/zricethezav/gitleaks/v8@v8.30.1 dir .\n" +
				"      - run: |\n          # go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...\n          make pins\n"},
			want: []string{"ci.yml runs `go run github.com/zricethezav/gitleaks/v8@v8.30.1` and no `make check` " +
				"prerequisite runs it; a contributor cannot reproduce it"},
		},
		{
			name: "a tool CI runs itself at another version",
			ci:   [2]string{"govulncheck@v1.8.0", "govulncheck@v1.7.0"},
			want: []string{"ci.yml runs `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0` and no `make check` " +
				"prerequisite runs it; a contributor cannot reproduce it"},
		},
		{
			name:     "a prerequisite a second check rule adds, which CI does not run",
			makefile: [2]string{"schema-ack:\n", "check: extra\nextra:\n\t@echo extra\nschema-ack:\n"},
			want:     []string{"`make check` runs \"extra\" and CI does not"},
		},
		{
			name:     "a target CI runs that make check does not, and that runs no pull-request gate",
			makefile: [2]string{"schema-ack:\n", "extra:\n\t@echo extra\nschema-ack:\n"},
			ci:       [2]string{"      - run: make pins\n", "      - run: make pins\n      - run: make extra\n"},
			want:     []string{"CI runs `make extra` and `make check` does not"},
		},
		{
			name:     "a check gate neither side runs",
			makefile: [2]string{"\t@$(GATES) docs", "\t@echo docs"},
			want:     []string{noDocs, "\"docs\" runs in make check and ci.yml does not run it"},
		},
		{
			name:     "a check gate CI runs itself and make check does not",
			makefile: [2]string{"\t@$(GATES) docs", "\t@echo docs"},
			ci:       [2]string{"      - run: make pins\n", "      - run: make pins\n      - run: go run ./scripts/gates docs\n"},
			want:     []string{noDocs},
		},
		{
			name: "a pull-request gate CI does not run",
			ci:   [2]string{"      - run: make schema-ack\n", ""},
			want: []string{"\"schema-ack\" runs on a pull request and ci.yml does not run it"},
		},
		{
			name:     "a pull-request gate in make check",
			makefile: [2]string{"docs pins ##", "docs pins schema-ack ##"},
			want: []string{"\"schema-ack\" runs on a pull request and `make check` runs it, " +
				"where there is no merge-base to measure from"},
		},
		{
			name:    "a release step no release file runs",
			release: [2]string{"release-notes", "release-tag"},
			want: []string{"\"release-notes\" runs in the release and none of .github/workflows/release.yml, " +
				".github/workflows/publish-mcp.yml and .goreleaser.yaml runs it; a step dropped from them fails at a tag, in public"},
		},
		{
			name:     "a command that declares no pipeline",
			registry: map[string]command{"classes": {}},
			want:     []string{"\"classes\" declares no pipeline in scripts/gates/main.go"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			makefile, ci, release, tags := parityMakefile, parityCI, parityRelease, parityTags
			if tc.makefile[0] != "" {
				makefile = replaceOnce(t, makefile, tc.makefile[0], tc.makefile[1])
			}
			if tc.ci[0] != "" {
				ci = replaceOnce(t, ci, tc.ci[0], tc.ci[1])
			}
			if tc.release[0] != "" {
				release = replaceOnce(t, release, tc.release[0], tc.release[1])
			}
			registry := maps.Clone(parityRegistry)
			maps.Copy(registry, tc.registry)
			if tc.tags != nil {
				tags = tc.tags
			}
			problems, _, err := parityProblems(makefile, ci, release, registry, tags)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(problems, tc.want) {
				t.Errorf("problems\n got %q\nwant %q", problems, tc.want)
			}
		})
	}
}

// replaceOnce is s with old replaced by new, failing the test when old is
// not there, so an edit cannot quietly leave the fixture unbroken.
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("the fixture does not contain %q", old)
	}
	return strings.Replace(s, old, new, 1)
}

// A gate that read nothing would find nothing wrong.
func TestParityRefusesToRunOnTooLittle(t *testing.T) {
	for _, tc := range []struct {
		name, makefile string
		tags           []string
		want           string
	}{
		{"no check target", strings.Replace(parityMakefile, "check:", "verify:", 1), parityTags, "no `check:` target in the Makefile"},
		{"no tag in the tree", parityMakefile, nil, "no //go:build line requires a tag; the tree is not being read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parityProblems(tc.makefile, parityCI, parityRelease, parityRegistry, tc.tags); err == nil || err.Error() != tc.want {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// make reads a # in an assignment as a comment, lets a later = or :=
// replace a value, lets ?= set only a variable not yet set, and appends
// with += after a space. export and override still assign.
func TestMakeVarsReadsAssignmentsAsMakeDoes(t *testing.T) {
	got := makeVars("GO ?= go\n" +
		"GORELEASER := github.com/goreleaser/goreleaser/v2@v2.18.1 # the release's own\n" +
		"LINT = v1\nLINT := v2\n" +
		"BIN = ./bin\nBIN ?= ./other\n" +
		"FLAGS = -s\nFLAGS += -w\nFRESH += -v\n" +
		"export GOFLAGS := -mod=mod\noverride PKG = example.com/x\n" +
		"# NOTE = a comment\n" +
		"vet:\n\tTAGS=live go vet ./...\n")
	want := map[string]string{
		"GO": "go", "GORELEASER": "github.com/goreleaser/goreleaser/v2@v2.18.1", "LINT": "v2", "BIN": "./bin",
		"FLAGS": "-s -w", "FRESH": "-v", "GOFLAGS": "-mod=mod", "PKG": "example.com/x",
	}
	if !maps.Equal(got, want) {
		t.Errorf("makeVars = %q, want %q", got, want)
	}
}

// A vet pass's tags are read from its -tags flag wherever it sits among
// the arguments, in either spelling, and a shell separator ends the
// command.
func TestVetPassesReadTheTagsFlagAnywhere(t *testing.T) {
	for _, tc := range []struct {
		text string
		want [][]string
	}{
		{"go vet -tags=live ./...", [][]string{{"live"}}},
		{"go vet -v -tags=live ./...", [][]string{{"live"}}},
		{"go vet -tags live,evals ./...", [][]string{{"live", "evals"}}},
		{"go vet --tags=e2e -v ./... && go vet ./...", [][]string{{"e2e"}}},
		{"make vet", nil},
		{"go vet ./... ; echo -tags=live", nil},
	} {
		if got := vetPasses(tc.text); !slices.EqualFunc(got, tc.want, slices.Equal) {
			t.Errorf("vetPasses(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// As in make, a target's rules merge their prerequisites, and its last
// recipe is the one that runs: a later rule with none leaves it.
func TestParseMakefileMergesATargetsRules(t *testing.T) {
	rules := parseMakefile("check: fmt\ncheck: vet fmt\nlint:\n\told\nlint:\n\tnew\nlint: fmt\n")
	if got := rules["check"].prereqs; !slices.Equal(got, []string{"fmt", "vet"}) {
		t.Errorf("check's prerequisites = %q, want fmt and vet", got)
	}
	if got := rules["lint"]; !slices.Equal(got.prereqs, []string{"fmt"}) ||
		!slices.Equal(got.recipe, []makeCommand{{line: 6, text: "new"}}) {
		t.Errorf("lint = %+v, want the prerequisite fmt and the recipe on line 6", got)
	}
}

// A tag the toolchain sets is never named by -tags, and is free to be
// either value beside the tag a pass must set. A file that also builds
// with no tag at all needs no tagged pass, and one that needs two tags
// together needs a pass that sets both.
func TestTreeBuildTags(t *testing.T) {
	t.Chdir(t.TempDir())
	files := map[string]string{
		"live.go":       "//go:build live\n\npackage x\n",
		"linux.go":      "//go:build e2e && linux\n\npackage x\n",
		"unix.go":       "//go:build stress && unix\n\npackage x\n",
		"release.go":    "//go:build soak && go1.22\n\npackage x\n",
		"notwin.go":     "//go:build !windows\n\npackage x\n",
		"win.go":        "//go:build windows\n\npackage x\n",
		"bench.go":      "//go:build bench && !experimental\n\npackage x\n",
		"pair.go":       "//go:build integration && slow\n\npackage x\n",
		"either.go":     "//go:build evals || !race\n\npackage x\n",
		"feature.go":    "//go:build amd64.v2 && go1.22\n\npackage x\n",
		"ignored.go":    "//go:build ignore\n\npackage x\n",
		"plain.go":      "package x\n",
		"notes.txt":     "//go:build notes\n",
		"testdata/x.go": "//go:build fixture\n\npackage x\n",
	}
	var names []string
	for rel, body := range files {
		put(t, ".", rel, body)
		names = append(names, rel)
	}
	got, err := treeBuildTags(names)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bench", "e2e", "integration,slow", "live", "soak", "stress"}; !slices.Equal(got, want) {
		t.Errorf("treeBuildTags = %q, want %q", got, want)
	}
}

// The repository's own files agree, with every tag its tree requires.
func TestParityGate(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	files, err := checkoutFiles()
	if err != nil {
		t.Fatal(err)
	}
	if tags, err := treeBuildTags(files); err != nil || !slices.Equal(tags, []string{"evals", "live"}) {
		t.Errorf("treeBuildTags = %q, %v; want evals and live", tags, err)
	}
	if err := parityGate(); err != nil {
		t.Error(err)
	}
}
