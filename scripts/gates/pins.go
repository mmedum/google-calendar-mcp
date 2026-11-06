package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

var (
	usesRe = regexp.MustCompile(`uses:\s*([^\s#]+)(?:\s*#\s*(.*))?`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// imageDigestRe is a container image pinned by its content digest.
	imageDigestRe = regexp.MustCompile(`^docker://[^@\s]+@sha256:[0-9a-f]{64}$`)
	toolPinRe     = regexp.MustCompile(`@(v[0-9][^\s"']*|latest)`)
)

// pinGate holds three claims about the workflows:
//
//  1. every action is pinned to a full 40-character commit SHA, which
//     GitHub says is "currently the only way to use an action as an
//     immutable release", and every container image to its sha256 digest;
//  2. every action that INSTALLS a tool pins the tool as well — a
//     pinned wrapper around an unpinned dependency is not pinned, and
//     this reads as complete when it is not — and an action the tables
//     below do not classify fails until it is;
//  3. no tool version is `@latest`, because a green build that cannot be
//     reproduced tomorrow is not evidence, and each tool the Makefile
//     runs with `go run` is at an exact version, since CI runs its tools
//     through make.
//
// Claim 2 was written into the comment above long before anything
// checked it, and the file it applies to did not exist: this counted
// workflows and wanted two, which `ci.yml` and `codeql.yml` satisfied
// while naming "CI and release" in the failure message. A count is not a
// name, so the files are named now.
func pinGate() error {
	files, err := workflowPaths()
	if err != nil {
		return err
	}
	for _, want := range []string{ciWorkflowPath, releaseWorkflowPath} {
		if !slices.Contains(files, want) {
			return fmt.Errorf("%s does not exist; the workflows here are %v", want, files)
		}
	}

	var problems []string
	actions, installers := 0, 0
	workflowPins := map[int][]installerPin{}

	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // a path from the workflow glob
		if err != nil {
			return err
		}
		// Claim 2 reads the file as YAML, because the question is which
		// `with:` belongs to which `uses:` and that is structure, not
		// text. Claims 1 and 3 are about the literal lines — a SHA and
		// its trailing comment — so they stay a line scan.
		flow, err := readWorkflow(path)
		if err != nil {
			return err
		}
		found, pins := unpinnedTools(flow)
		installers += len(pins)
		for _, pin := range pins {
			workflowPins[pin.row] = append(workflowPins[pin.row], pin)
		}
		for _, problem := range found {
			problems = append(problems, path+": "+problem)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}

			if m := usesRe.FindStringSubmatch(trimmed); m != nil {
				actions++
				uses := m[1]
				action, ref := usesAction(uses)
				switch {
				case strings.HasPrefix(uses, "./"):
					// A local action is a path in this checkout, not a reference.
				case action == "":
					if !imageDigestRe.MatchString(uses) {
						problems = append(problems, fmt.Sprintf(
							"%s:%d: %s is a container image with no sha256 digest, so the image it runs can change", path, i+1, uses))
					}
				case ref == "":
					problems = append(problems, fmt.Sprintf("%s:%d: %s has no version at all", path, i+1, uses))
				case !shaRe.MatchString(ref):
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned to a tag or branch, not a 40-character SHA", path, i+1, uses))
				case strings.TrimSpace(m[2]) == "":
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned but has no trailing comment saying which version that SHA is",
						path, i+1, action))
				}
			}

			// Any tool reference pinned to @latest is unpinned.
			for _, m := range toolPinRe.FindAllStringSubmatch(trimmed, -1) {
				if m[1] == "latest" {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: something is pinned to @latest: %s", path, i+1, trimmed))
				}
			}
		}
	}

	if actions < 3 {
		return fmt.Errorf("found only %d action references across %d workflows; that is not a real CI setup",
			actions, len(files))
	}

	// A floor on claim 2 as well as claim 1. Every installer in the
	// table below is used by these workflows, so seeing fewer than all
	// of them means the reader stopped understanding the file rather
	// than that the file became clean. Rows, not steps: one action used
	// in every job would meet a count of steps alone.
	if len(workflowPins) < len(toolInstallers) {
		return fmt.Errorf("found %d of the %d tool-installing actions the table names across %d workflows; "+
			"a workflow that cannot be read reports no problems", len(workflowPins), len(toolInstallers), len(files))
	}
	makefile, err := os.ReadFile(makefilePath)
	if err != nil {
		return err
	}
	vars := makeVars(string(makefile))
	drift, compared := rehearsalDrift(workflowPins, vars)
	problems = append(problems, drift...)
	tools, toolProblems := makefileTools(string(makefile), vars)
	if tools < minMakefileTools {
		return fmt.Errorf("the Makefile runs %d tools at a version, fewer than %d: a tool runs from the PATH, "+
			"or the `go run module@version` reader has stopped finding them", tools, minMakefileTools)
	}
	problems = append(problems, toolProblems...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d workflows, %d action references, all SHA-pinned and commented; "+
		"%d tool-installing action(s) pin their tool, %d also run locally; the Makefile runs %d tools at exact versions\n",
		len(files), actions, installers, compared, tools)
	return nil
}

// minMakefileTools is the fewest tools the Makefile may run at a version.
// CI runs each tool through a make target, so nothing else holds a tool
// that runs from the PATH to a version.
const minMakefileTools = 5

var (
	// exactVersion is v1.2.3, and nothing looser.
	exactVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+[-.\w]*$`)
	// goModToolchain and goModGo are the go.mod lines setup-go installs
	// from: a toolchain directive, or else the go line.
	goModToolchain = regexp.MustCompile(`(?m)^toolchain go(\S+)`)
	goModGo        = regexp.MustCompile(`(?m)^go (\S+)`)
	// exactGoVersion is 1.2.3, and not the 1.2 that setup-go reads as the
	// newest patch.
	exactGoVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// makefileTools is how many tools the Makefile's recipes run with `go run`
// at a version, variables expanded, and each one whose version floats.
func makefileTools(makefile string, vars map[string]string) (int, []string) {
	// Two targets of one rule share its commands, so each is read once.
	byLine := map[int]string{}
	for _, rule := range parseMakefile(makefile) {
		for _, c := range rule.recipe {
			byLine[c.line] = c.text
		}
	}
	tools := map[string]bool{}
	var problems []string
	for _, line := range slices.Sorted(maps.Keys(byLine)) {
		for _, m := range goRunAt.FindAllStringSubmatch(expand(byLine[line], vars), -1) {
			tools[m[1]] = true
			if !exactVersion.MatchString(m[2]) {
				problems = append(problems, fmt.Sprintf("%s:%d runs %s@%s, which is not an exact version, "+
					"and CI runs it through make", makefilePath, line, m[1], m[2]))
			}
		}
	}
	return len(tools), problems
}

// installers are the actions that fetch a tool rather than do the work
// themselves, and the input that pins what they fetch.
//
// Pinning one of these to a SHA pins the WRAPPER. Without the input
// beside it the action fetches whatever is current that morning, and the
// step reads exactly like a pinned one. A new major version then runs
// under a config written for the old one: cosign 3 made `--bundle`
// required, and a release configured for version 2 fails.
var toolInstallers = []toolInstaller{
	{action: "sigstore/cosign-installer", input: "cosign-release"},
	{action: "anchore/sbom-action/download-syft", input: "syft-version"},
	{action: "goreleaser/goreleaser-action", input: "version", makeVar: "GORELEASER"},
	// go.mod is a pin by reference, and a better one: it cannot disagree
	// with itself the way two literals can.
	{action: "actions/setup-go", input: "go-version-file", file: true},
}

// toolInstaller is one action that fetches a tool, by its name less the
// ref, and the input that pins what it fetches.
type toolInstaller struct {
	action string
	input  string
	// file is an input that names the go.mod holding the version rather
	// than the version itself.
	file bool
	// makeVar is the Makefile variable that pins the same tool for a
	// local run, empty when nothing here runs it locally. A rehearsal on
	// a different version of goreleaser is not a rehearsal, and the two
	// numbers sit in two files that do not mention each other.
	makeVar string
}

// notInstallers are the actions that install no tool, each with the
// reason, so that adding an action is a decision rather than an omission.
var notInstallers = map[string]string{
	"actions/checkout":                "checks out the repository",
	"actions/attest-build-provenance": "calls the attestation API",
	"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
	"github/codeql-action/autobuild":  "CodeQL's bundle is GitHub's to manage",
	"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
}

// usesAction is the action a `uses:` value names, less its ref, and the
// ref. Both are empty for a local action or a container image, which name
// a path and an image rather than an action.
func usesAction(uses string) (action, ref string) {
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "docker://") {
		return "", ""
	}
	action, ref, _ = strings.Cut(uses, "@")
	return action, ref
}

// installerRow is the toolInstallers entry for an action, or -1 when it is
// not an installer.
func installerRow(action string) int {
	return slices.IndexFunc(toolInstallers, func(want toolInstaller) bool { return want.action == action })
}

// installerPin is what one tool-installing step says about its tool.
type installerPin struct {
	row     int    // which toolInstallers entry it matched
	version string // the value of that entry's input
	pinned  bool   // whether the step set it at all
}

// installerPins finds the tool-installing steps in a workflow, and each
// action neither table classifies.
//
// Which steps those are is one rule with two readers — claim 2 below,
// and the rehearsal check, which needs the version rather than its
// absence. Two walks would let the two disagree about what a
// tool-installing step is, which is how a table with one reader grows a
// second that quietly sees less.
func installerPins(flow workflow) (pins []installerPin, unclassified []string) {
	for _, step := range flow.steps() {
		action, _ := usesAction(step.Uses)
		if action == "" {
			continue
		}
		if i := installerRow(action); i >= 0 {
			value, found := step.input(toolInstallers[i].input)
			pins = append(pins, installerPin{row: i, version: value, pinned: found})
		} else if _, known := notInstallers[action]; !known {
			unclassified = append(unclassified, action)
		}
	}
	return pins, unclassified
}

// unpinnedTools reports any tool-installing action whose version input is
// missing or floating, and any action neither table classifies, along
// with every tool-installing step it saw.
//
// The steps are returned rather than a count so the caller can assert a
// floor — an action this never recognized and an action correctly pinned
// are the same silence otherwise — and so the rehearsal check reads the
// versions from the same walk rather than repeating it.
func unpinnedTools(flow workflow) ([]string, []installerPin) {
	var problems []string
	pins, unclassified := installerPins(flow)
	for _, action := range unclassified {
		problems = append(problems, fmt.Sprintf(
			"%s is not classified in scripts/gates/pins.go: add it to toolInstallers with the input "+
				"that pins its tool, or to notInstallers with the reason. A SHA pins the wrapper, not the tool", action))
	}
	for _, pin := range pins {
		want := toolInstallers[pin.row]
		switch {
		case !pin.pinned:
			problems = append(problems, fmt.Sprintf(
				"%s is pinned to a SHA and does not set %s, so the TOOL it installs is whatever is "+
					"current that morning", want.action, want.input))
		case want.file:
			if problem := goVersionFileProblem(pin.version); problem != "" {
				problems = append(problems, fmt.Sprintf("%s sets %s to %q, %s", want.action, want.input, pin.version, problem))
			}
		case !exactVersion.MatchString(pin.version):
			problems = append(problems, fmt.Sprintf(
				"%s sets %s to %q, which floats", want.action, want.input, pin.version))
		}
	}
	return problems, pins
}

// goVersionFileProblem reads the file a go-version-file names as setup-go
// does, the toolchain directive before the go line, and says why that is
// not an exact version; empty when it is
// (https://github.com/actions/setup-go/blob/b7ad1dad31e06c5925ef5d2fc7ad053ef454303e/src/installer.ts#L645,
// setup-go v7.0.0).
func goVersionFileProblem(file string) string {
	data, err := os.ReadFile(file) //nolint:gosec // a repository path a workflow names
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "which does not exist"
	case err != nil:
		return fmt.Sprintf("which cannot be read: %v", err)
	}
	m := goModToolchain.FindSubmatch(data)
	if m == nil {
		m = goModGo.FindSubmatch(data)
	}
	switch {
	case m == nil:
		return "which names no Go version"
	case !exactGoVersion.Match(m[1]):
		return fmt.Sprintf("whose Go version %s is not an exact release, x.y.z", m[1])
	}
	return ""
}

// rehearsalDrift holds the version a maintainer runs by hand against the
// version the release runs.
//
// `make release-rehearse` exists to run what the tag runs. Both numbers
// are valid on their own, neither file mentions the other, and the
// difference shows up as a release behaving unlike every rehearsal of
// it. The pair is named in the table rather than derived from the two
// strings: a module path and an action reference have only the tool in
// common, and guessing that from the names gets `anchore/sbom-action`
// wrong — it installs syft.
func rehearsalDrift(workflowPins map[int][]installerPin, vars map[string]string) ([]string, int) {
	var problems []string
	compared := 0
	for i, want := range toolInstallers {
		if want.makeVar == "" {
			continue
		}
		compared++
		local, err := makeVariable(vars, want.makeVar)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"the table pairs %s with %s, and %v", want.action, want.makeVar, err))
			continue
		}
		_, version, found := strings.Cut(local, "@")
		if !found {
			problems = append(problems, fmt.Sprintf(
				"%s is %q, which pins no version, so nothing holds %s to it", want.makeVar, local, want.action))
			continue
		}
		// "Compared nothing" and "found nothing wrong" print the same
		// clean line otherwise: a paired row whose action no workflow
		// uses any more is a comparison that silently stopped running.
		//
		// A step that is there and pins nothing is a different sentence,
		// and claim 2 above has already said it — saying "no workflow
		// uses this" as well would name the wrong file.
		if len(workflowPins[i]) == 0 {
			problems = append(problems, fmt.Sprintf(
				"the table pairs %s with %s and no workflow step uses %s, so nothing was compared",
				want.action, want.makeVar, want.action))
			continue
		}
		for _, remote := range workflowPins[i] {
			if remote.pinned && remote.version != version {
				problems = append(problems, fmt.Sprintf(
					"%s pins %s and %s runs %s: a rehearsal on a different version is not a rehearsal",
					want.makeVar, version, want.action, remote.version))
			}
		}
	}
	return problems, compared
}
