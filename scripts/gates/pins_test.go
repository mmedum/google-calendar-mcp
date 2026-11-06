package main

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Pinning an action pins the WRAPPER. The steps below fetch a tool, and a
// SHA on the `uses:` line says nothing about which version of that tool
// arrives — which is why this half reads as complete when it is not.
//
// The last two cases of the table, and the flow-style test below it, are
// not hypothetical: they are the two ways the first version of this check
// was wrong. It was a hand-rolled step splitter, it read every line of a
// step rather than the `with:` block, and it understood only one of
// YAML's two list styles. Both were found by probing it.

// asWorkflow parses a workflow fragment the way the gate does.
func asWorkflow(t *testing.T, src string) workflow {
	t.Helper()
	var w workflow
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("fixture is not valid YAML: %v", err)
	}
	return w
}

// job wraps steps in the smallest workflow that carries them.
func job(steps string) string {
	return "jobs:\n  release:\n    steps:\n" + steps
}

func TestAnActionThatInstallsAToolMustPinTheTool(t *testing.T) {
	pinned := job(`      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6
        with:
          cosign-release: v3.1.3
      - uses: anchore/sbom-action/download-syft@3ad7283483fc7af8ff2b4ea19663c2d5ca935e26
        with:
          syft-version: v1.51.1
`)
	problems, pins := unpinnedTools(asWorkflow(t, pinned))
	if len(problems) > 0 {
		t.Fatalf("a correctly pinned pair was refused:\n%s", strings.Join(problems, "\n"))
	}
	if len(pins) != 2 {
		t.Fatalf("saw %d installers in a fixture with two", len(pins))
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a SHA with no tool version beside it",
			body: job("      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6\n"),
			want: "cosign-release",
		},
		{
			name: "a tool version that floats",
			body: job(`      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94
        with:
          version: "~> v2"
`),
			want: "floats",
		},
		{
			name: "a tool version that names only its major",
			body: job(`      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94
        with:
          version: v2
`),
			want: "floats",
		},
		{
			name: "a tool version that is latest",
			body: job(`      - uses: anchore/sbom-action/download-syft@3ad7283483fc7af8ff2b4ea19663c2d5ca935e26
        with:
          syft-version: latest
`),
			want: "floats",
		},
		{
			// The first check read every line of the step, so this
			// passed. `version` is the most collidable input name there
			// is, and env is where it collides.
			name: "a version under env, which is not an input",
			body: job(`      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94
        env:
          version: v2.18.1
`),
			want: "does not set version",
		},
		{
			// A `with:` block belongs to the step above it. Reading the
			// file as a whole would call this pinned, because the input
			// the first step lacks is present under another action.
			name: "a version borrowed from the next step",
			body: job(`      - uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e
        with:
          cosign-release: v3.1.3
`),
			want: "cosign-release",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems, _ := unpinnedTools(asWorkflow(t, tc.body))
			if len(problems) == 0 {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !mentions(problems, tc.want) {
				t.Fatalf("wanted %q, got:\n%s", tc.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// An action that installs a tool and names no version is an absence the
// other claims cannot see, so every action is classified, and one nobody
// classified fails until somebody does.
func TestEveryActionIsClassified(t *testing.T) {
	t.Chdir(t.TempDir())
	put(t, ".", "go.mod", "module x\n\ngo 1.27.3\n")
	const sha = "@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
	for _, tc := range []struct {
		name, steps string
		want        []string
	}{
		{"known actions, a local one and a container", "" +
			"      - uses: actions/checkout" + sha + "\n" +
			"      - uses: actions/setup-go" + sha + "\n        with:\n          go-version-file: go.mod\n" +
			"      - uses: ./.github/actions/local\n" +
			"      - uses: docker://alpine:3.20\n" +
			"      - run: make check\n", nil},
		{"an action nobody classified", "      - uses: actions/cache" + sha + "\n", []string{
			"actions/cache is not classified in scripts/gates/pins.go: add it to toolInstallers with the input " +
				"that pins its tool, or to notInstallers with the reason. A SHA pins the wrapper, not the tool",
		}},
		{"an action named like an installer it is not", "      - uses: actions/setup-go-extra" + sha + "\n", []string{
			"actions/setup-go-extra is not classified in scripts/gates/pins.go: add it to toolInstallers with the input " +
				"that pins its tool, or to notInstallers with the reason. A SHA pins the wrapper, not the tool",
		}},
		{"Go installed from whatever the runner has", "      - uses: actions/setup-go" + sha + "\n", []string{
			"actions/setup-go is pinned to a SHA and does not set go-version-file, so the TOOL it installs is " +
				"whatever is current that morning",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems, _ := unpinnedTools(asWorkflow(t, job(tc.steps)))
			if !slices.Equal(problems, tc.want) {
				t.Errorf("problems\n got %q\nwant %q", problems, tc.want)
			}
		})
	}
}

// CI runs its tools through make targets, so the Makefile's version is
// CI's: a recipe's `go run` is at an exact version, read through the
// variables it is spelled with and across a continued line, and a
// commented-out one runs nothing. A problem names the line it is on, and a
// rule's two targets share one recipe, which is reported once.
func TestTheMakefileRunsEachToolAtAnExactVersion(t *testing.T) {
	const makefile = "GO ?= go\n" +
		"LINT ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0\n" +
		"\n" +
		"lint lint-ci:\n" +
		"\t$(GO) run $(LINT) run\n" +
		"vuln:\n" +
		"\t# $(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...\n" +
		"\tgo run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...\n" +
		"licenses:\n" +
		"\t$(GO) run \\\n" +
		"\t\tgithub.com/google/go-licenses@v1.6.0 check ./...\n"
	for _, tc := range []struct {
		name, old, new string
		tools          int
		want           []string
	}{
		{name: "as written", tools: 3},
		{"a variable at latest", "golangci-lint@v2.14.0", "golangci-lint@latest", 3, []string{
			"Makefile:5 runs github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest, which is not an exact " +
				"version, and CI runs it through make",
		}},
		{"a branch", "govulncheck@v1.8.0", "govulncheck@master", 3, []string{
			"Makefile:8 runs golang.org/x/vuln/cmd/govulncheck@master, which is not an exact version, and CI runs it through make",
		}},
		{"a continued line at latest", "go-licenses@v1.6.0", "go-licenses@latest", 3, []string{
			"Makefile:10 runs github.com/google/go-licenses@latest, which is not an exact version, and CI runs it through make",
		}},
		{"a tool from the PATH", "go run golang.org/x/vuln/cmd/govulncheck@v1.8.0", "govulncheck", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edited := makefile
			if tc.old != "" {
				edited = replaceOnce(t, makefile, tc.old, tc.new)
			}
			tools, problems := makefileTools(edited, makeVars(edited))
			if tools != tc.tools || !slices.Equal(problems, tc.want) {
				t.Errorf("makefileTools = %d, %q; want %d, %q", tools, problems, tc.tools, tc.want)
			}
		})
	}
	committed := string(repoFile(t, makefilePath))
	if tools, problems := makefileTools(committed, makeVars(committed)); tools < minMakefileTools || problems != nil {
		t.Errorf("the committed Makefile: %d tools, %q", tools, problems)
	}
}

// setup-go installs what go.mod's toolchain directive names, or else its
// go line, so that is the version held exact.
func TestGoIsPinnedByAnExactVersionInGoMod(t *testing.T) {
	const step = "      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e\n" +
		"        with:\n          go-version-file: go.mod\n"
	const sets = `actions/setup-go sets go-version-file to "go.mod", `
	for _, tc := range []struct {
		name, gomod string
		want        []string
	}{
		{"an exact go line", "module x\n\ngo 1.27.3\n", nil},
		{"an exact toolchain over a go line that floats", "module x\n\ngo 1.27\n\ntoolchain go1.27.3\n", nil},
		{"a go line that floats", "module x\n\ngo 1.27\n", []string{sets + "whose Go version 1.27 is not an exact release, x.y.z"}},
		{"a release candidate over an exact go line", "module x\n\ngo 1.27.3\n\ntoolchain go1.28rc1\n",
			[]string{sets + "whose Go version 1.28rc1 is not an exact release, x.y.z"}},
		{"no go line", "module x\n", []string{sets + "which names no Go version"}},
		{"no such file", "", []string{sets + "which does not exist"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if tc.gomod != "" {
				put(t, ".", "go.mod", tc.gomod)
			}
			if problems, _ := unpinnedTools(asWorkflow(t, job(step))); !slices.Equal(problems, tc.want) {
				t.Errorf("problems\n got %q\nwant %q", problems, tc.want)
			}
		})
	}
}

// pinsSHA is a pinned ref with the comment the pins gate wants beside it.
const pinsSHA = "@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v1.0.0\n"

// pinsMakefile runs five tools at exact versions, and goreleaser at the
// version pinsCheckout's release workflow runs.
const pinsMakefile = "GO ?= go\nGORELEASER ?= github.com/goreleaser/goreleaser/v2@v2.18.1\n\n" +
	"lint:\n\t$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run\n" +
	"vuln:\n\t$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...\n" +
	"licenses:\n\t$(GO) run github.com/google/go-licenses@v1.6.0 check ./...\n" +
	"secrets:\n\t$(GO) run github.com/zricethezav/gitleaks/v8@v8.30.1 dir .\n" +
	"release-rehearse:\n\t$(GO) run $(GORELEASER) release --snapshot\n"

// The release workflow's steps in pinsCheckout, which use every installer
// the table names.
const (
	pinsCosign = "      - uses: sigstore/cosign-installer" + pinsSHA + "        with:\n          cosign-release: v3.1.3\n"
	pinsSyft   = "      - uses: anchore/sbom-action/download-syft" + pinsSHA + "        with:\n          syft-version: v1.51.1\n"
	pinsGo     = "      - uses: actions/setup-go" + pinsSHA + "        with:\n          go-version-file: go.mod\n"
	pinsSteps  = "      - uses: actions/checkout" + pinsSHA + pinsGo + pinsCosign + pinsSyft +
		"      - uses: goreleaser/goreleaser-action" + pinsSHA + "        with:\n          version: v2.18.1\n"
)

// pinsCheckout makes the working directory a checkout the pins gate
// accepts, but for ciSteps, which follow ci.yml's checkout step from its
// line 5, and the Makefile given.
func pinsCheckout(t *testing.T, ciSteps, makefile string) {
	t.Helper()
	t.Chdir(t.TempDir())
	put(t, ".", ciWorkflowPath, "jobs:\n  test:\n    steps:\n      - uses: actions/checkout"+pinsSHA+ciSteps)
	put(t, ".", releaseWorkflowPath, "jobs:\n  release:\n    steps:\n"+pinsSteps)
	put(t, ".", makefilePath, makefile)
	put(t, ".", "go.mod", "module x\n\ngo 1.27.3\n")
}

// Every action is pinned to a 40-character SHA with the version it is in a
// comment beside it. A local action is a path in this checkout, and a
// container image is pinned by its sha256 digest.
func TestEveryActionIsPinnedToACommentedSHA(t *testing.T) {
	for _, tc := range []struct{ name, step, want string }{
		{"a local action", "      - uses: ./.github/actions/local\n", ""},
		{"a container image by digest",
			"      - uses: docker://alpine@sha256:" + strings.Repeat("0123456789abcdef", 4) + "\n", ""},
		{"a tag", "      - uses: actions/checkout@v7.0.1\n",
			".github/workflows/ci.yml:5: actions/checkout@v7.0.1 is pinned to a tag or branch, not a 40-character SHA"},
		{"no ref", "      - uses: actions/checkout\n", ".github/workflows/ci.yml:5: actions/checkout has no version at all"},
		{"a SHA with no comment", "      - uses: actions/checkout@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e\n",
			".github/workflows/ci.yml:5: actions/checkout is pinned but has no trailing comment saying which version that SHA is"},
		{"a container image by tag", "      - uses: docker://alpine:3.20\n",
			".github/workflows/ci.yml:5: docker://alpine:3.20 is a container image with no sha256 digest, so the image it runs can change"},
		{"a container image by name alone", "      - uses: docker://alpine\n",
			".github/workflows/ci.yml:5: docker://alpine is a container image with no sha256 digest, so the image it runs can change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinsCheckout(t, tc.step, pinsMakefile)
			if err := pinGate(); tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Errorf("pinGate() = %v, want %q", err, tc.want)
			}
		})
	}
}

// The floor counts the installers the table names, not the steps that use
// them: setup-go in every job would meet a count of steps by itself.
func TestThePinsGateSeesEveryInstallerTheTableNames(t *testing.T) {
	pinsCheckout(t, pinsGo+pinsGo, pinsMakefile)
	put(t, ".", releaseWorkflowPath, "jobs:\n  release:\n    steps:\n"+
		replaceOnce(t, replaceOnce(t, pinsSteps, pinsCosign, ""), pinsSyft, ""))
	const want = "found 2 of the 4 tool-installing actions the table names across 2 workflows; " +
		"a workflow that cannot be read reports no problems"
	if err := pinGate(); err == nil || err.Error() != want {
		t.Errorf("pinGate() = %v, want %q", err, want)
	}
}

// A tool moved to the PATH leaves no version to judge, so the gate holds
// the Makefile to a count of the tools it runs at one.
func TestThePinsGateCountsTheMakefilesTools(t *testing.T) {
	pinsCheckout(t, "", pinsMakefile)
	if err := pinGate(); err != nil {
		t.Fatalf("five tools at exact versions: %v", err)
	}
	pinsCheckout(t, "", replaceOnce(t, pinsMakefile, "$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0", "govulncheck"))
	const want = "the Makefile runs 4 tools at a version, fewer than 5: a tool runs from the PATH, " +
		"or the `go run module@version` reader has stopped finding them"
	if err := pinGate(); err == nil || err.Error() != want {
		t.Errorf("pinGate() = %v, want %q", err, want)
	}
}

// The other half of a gate that reads a file: one it cannot read must not
// come back clean. The first version understood block sequences only, so
// this workflow produced no steps, no installers and no problems.
func TestAWorkflowWrittenInFlowStyleIsStillRead(t *testing.T) {
	w := asWorkflow(t, "jobs: {release: {steps: [{uses: 'sigstore/cosign-installer@abc'}]}}\n")
	problems, pins := unpinnedTools(w)
	if len(pins) != 1 {
		t.Fatalf("saw %d installers in a flow-style workflow that has one", len(pins))
	}
	if !mentions(problems, "cosign-release") {
		t.Fatalf("the unpinned tool was not reported: %v", problems)
	}
}

// The committed workflows are the case that matters. They name go.mod,
// which is read from the repository's root.
func TestTheCommittedWorkflowsPinWhatTheyInstall(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, path := range []string{ciWorkflowPath, releaseWorkflowPath} {
		problems, _ := unpinnedTools(asWorkflow(t, string(repoFile(t, path))))
		if len(problems) > 0 {
			t.Errorf("%s:\n%s", path, strings.Join(problems, "\n"))
		}
	}
}

// Every installer the table names is actually used, or pinGate's floor is
// asserting a number nothing can reach.
func TestEveryInstallerInTheTableIsUsed(t *testing.T) {
	seen := map[string]bool{}
	for _, path := range []string{ciWorkflowPath, releaseWorkflowPath} {
		for _, step := range asWorkflow(t, string(repoFile(t, path))).steps() {
			action, _ := usesAction(step.Uses)
			seen[action] = true
		}
	}
	for _, want := range toolInstallers {
		if !seen[want.action] {
			t.Errorf("the table names %s and no workflow uses it, so the floor counts a step nobody runs",
				want.action)
		}
	}
}

// A workflow only a person can start is one nobody remembers to start.
func TestTheReleaseWorkflowTriggersOnATag(t *testing.T) {
	w := asWorkflow(t, string(repoFile(t, releaseWorkflowPath)))
	if !w.triggersOnTag("v") {
		t.Fatal("the release workflow does not trigger on a v* tag")
	}
	ci := asWorkflow(t, string(repoFile(t, ciWorkflowPath)))
	if ci.triggersOnTag("v") {
		t.Fatal("ci triggers on a tag, so a release would run its gates twice and this check proves nothing")
	}
}

// The rehearsal is a copy of the release, and a copy goes stale. The
// Makefile pins the goreleaser `make release-rehearse` runs, the
// workflow pins the goreleaser the tag runs, and neither file mentions
// the other: the difference would show up as a release behaving unlike
// every rehearsal of it.
//
// The pair is named in the table rather than derived from the two
// strings. Deriving it looks free and gets `anchore/sbom-action` wrong,
// which installs syft.

// pinnedIn is the workflow side of the comparison, keyed the way
// pinGate keys it.
func pinnedIn(version string) map[int][]installerPin {
	for i, want := range toolInstallers {
		if want.makeVar != "" {
			return map[int][]installerPin{i: {{row: i, version: version, pinned: true}}}
		}
	}
	return nil
}

// unpinnedIn is the same row with a step that exists and sets no
// version: claim 2's sentence, not this check's.
func unpinnedIn() map[int][]installerPin {
	for i, want := range toolInstallers {
		if want.makeVar != "" {
			return map[int][]installerPin{i: {{row: i}}}
		}
	}
	return nil
}

func TestATooledRehearsalRunsTheReleasesVersion(t *testing.T) {
	makefile := makeVars(string(repoFile(t, makefilePath)))

	local, err := makeVariable(makefile, "GORELEASER")
	if err != nil {
		t.Fatalf("the table pairs goreleaser-action with GORELEASER: %v", err)
	}
	_, version, _ := strings.Cut(local, "@")

	// The repository as it stands, read from BOTH files.
	//
	// The first version of this built the workflow side out of the
	// Makefile's own value and then asserted the two agreed, which is a
	// comparison with itself: it passes on any pin, including two that
	// have drifted apart. The expected value has to come from somewhere
	// other than the thing under test, so the workflow is parsed.
	flow, err := readWorkflow(repoPath(t, releaseWorkflowPath))
	if err != nil {
		t.Fatalf("read the release workflow: %v", err)
	}
	real := map[int][]installerPin{}
	pins, _ := installerPins(flow)
	for _, pin := range pins {
		real[pin.row] = append(real[pin.row], pin)
	}
	problems, compared := rehearsalDrift(real, makefile)
	if len(problems) != 0 || compared != 1 {
		t.Fatalf("the Makefile pins %s and release.yml disagrees: %v (%d comparison(s))",
			version, problems, compared)
	}

	// One version behind is the whole failure: both numbers are valid.
	problems, _ = rehearsalDrift(pinnedIn("v0.0.1"), makefile)
	if len(problems) != 1 {
		t.Fatalf("a drifted workflow pin must be reported: %v", problems)
	}
	for _, want := range []string{"GORELEASER", version, "v0.0.1"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("the message does not name %q: %s", want, problems[0])
		}
	}
}

// A paired row nobody runs any more compares nothing, and a check that
// compares nothing prints the same clean line as one that found nothing
// wrong.
func TestAPairedToolNoWorkflowInstallsIsReported(t *testing.T) {
	problems, compared := rehearsalDrift(map[int][]installerPin{}, makeVars(string(repoFile(t, makefilePath))))
	if compared != 1 {
		t.Fatalf("the table pairs %d tool(s) with a Makefile variable, want 1", compared)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "nothing was compared") {
		t.Fatalf("an unused pairing must be reported: %v", problems)
	}
}

// A step that installs the tool and pins nothing is claim 2's business.
// Reporting it here as "no workflow step uses this" would send the
// reader to the wrong file for a defect the gate has already named.
func TestAnUnpinnedStepIsLeftToTheClaimThatOwnsIt(t *testing.T) {
	problems, _ := rehearsalDrift(unpinnedIn(), makeVars(string(repoFile(t, makefilePath))))
	if len(problems) != 0 {
		t.Fatalf("the rehearsal check spoke for a defect claim 2 owns: %v", problems)
	}
}

// The Makefile variable named in the table has to exist, or the
// comparison reads one side of two.
func TestAMissingMakefilePinIsReported(t *testing.T) {
	problems, _ := rehearsalDrift(pinnedIn("v2.18.1"), makeVars("all:\n\ttrue\n"))
	if len(problems) != 1 || !strings.Contains(problems[0], "GORELEASER") {
		t.Fatalf("a Makefile with no pin must be reported: %v", problems)
	}
}
