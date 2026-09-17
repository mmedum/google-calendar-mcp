package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Pinning an action pins the WRAPPER. The steps below fetch a tool, and a
// SHA on the `uses:` line says nothing about which version of that tool
// arrives — which is why this half reads as complete when it is not, and
// why the standard records it breaking a release on two servers the same
// afternoon.
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

// The committed workflows are the case that matters.
func TestTheCommittedWorkflowsPinWhatTheyInstall(t *testing.T) {
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
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
	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		for _, step := range asWorkflow(t, string(repoFile(t, path))).steps() {
			for _, want := range toolInstallers {
				if strings.HasPrefix(step.Uses, want.action) {
					seen[want.action] = true
				}
			}
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
	w := asWorkflow(t, string(repoFile(t, ".github/workflows/release.yml")))
	if !w.triggersOnTag("v") {
		t.Fatal("the release workflow does not trigger on a v* tag")
	}
	ci := asWorkflow(t, string(repoFile(t, ".github/workflows/ci.yml")))
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
	makefile := repoPath(t, makefilePath)

	// The repository as it stands: the Makefile's pin and the
	// workflow's agree.
	local, err := makeVariable(makefile, "GORELEASER")
	if err != nil {
		t.Fatalf("the table pairs goreleaser-action with GORELEASER: %v", err)
	}
	_, version, _ := strings.Cut(local, "@")
	problems, compared := rehearsalDrift(pinnedIn(version), makefile)
	if len(problems) != 0 || compared != 1 {
		t.Fatalf("agreeing pins reported %v after %d comparison(s)", problems, compared)
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
	problems, compared := rehearsalDrift(map[int][]installerPin{}, repoPath(t, makefilePath))
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
	problems, _ := rehearsalDrift(unpinnedIn(), repoPath(t, makefilePath))
	if len(problems) != 0 {
		t.Fatalf("the rehearsal check spoke for a defect claim 2 owns: %v", problems)
	}
}

// The Makefile variable named in the table has to exist, or the
// comparison reads one side of two.
func TestAMissingMakefilePinIsReported(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "Makefile")
	if err := os.WriteFile(empty, []byte("all:\n\ttrue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	problems, _ := rehearsalDrift(pinnedIn("v2.18.1"), empty)
	if len(problems) != 1 || !strings.Contains(problems[0], "GORELEASER") {
		t.Fatalf("a Makefile with no pin must be reported: %v", problems)
	}
}
