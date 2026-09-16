package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	usesRe    = regexp.MustCompile(`uses:\s*([^\s#]+)(?:\s*#\s*(.*))?`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	toolPinRe = regexp.MustCompile(`@(v[0-9][^\s"']*|latest)`)
)

// pinGate holds three claims about the workflows:
//
//  1. every action is pinned to a full 40-character commit SHA, which
//     GitHub says is "currently the only way to use an action as an
//     immutable release";
//  2. every action that INSTALLS a tool pins the tool as well — a
//     pinned wrapper around an unpinned dependency is not pinned, and
//     this reads as complete when it is not;
//  3. no tool version is `@latest`, because a green build that cannot be
//     reproduced tomorrow is not evidence.
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
	for _, want := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		if !contains(files, want) {
			return fmt.Errorf("%s does not exist; the workflows here are %v", want, files)
		}
	}

	var problems []string
	actions, installers := 0, 0

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
		found, n := unpinnedTools(flow)
		installers += n
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
				ref := m[1]
				// A local action is a path, not a pinned reference.
				if strings.HasPrefix(ref, "./") {
					continue
				}
				at := strings.LastIndex(ref, "@")
				if at < 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: %s has no version at all", path, i+1, ref))
					continue
				}
				if !shaRe.MatchString(ref[at+1:]) {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned to a tag or branch, not a 40-character SHA", path, i+1, ref))
					continue
				}
				if strings.TrimSpace(m[2]) == "" {
					problems = append(problems, fmt.Sprintf(
						"%s:%d: %s is pinned but has no trailing comment saying which version that SHA is",
						path, i+1, ref[:at]))
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
	// than that the file became clean.
	if installers < len(toolInstallers) {
		return fmt.Errorf("found %d tool-installing action(s) across %d workflows and the table names %d; "+
			"a workflow that cannot be read reports no problems", installers, len(files), len(toolInstallers))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d workflows, %d action references, all SHA-pinned and commented; "+
		"%d tool-installing action(s) pin their tool\n", len(files), actions, installers)
	return nil
}

// installers are the actions that fetch a tool rather than do the work
// themselves, and the input that pins what they fetch.
//
// Pinning one of these to a SHA pins the WRAPPER. Without the input
// beside it the action fetches whatever is current that morning, and the
// step reads exactly like a pinned one. The standard records this
// breaking a release on two servers the same afternoon: cosign 3 landed,
// `--bundle` had become required, and the config passed the arguments
// version 2 wanted.
var toolInstallers = []struct {
	action string
	input  string
}{
	{"sigstore/cosign-installer", "cosign-release"},
	{"anchore/sbom-action", "syft-version"},
	{"goreleaser/goreleaser-action", "version"},
}

// unpinnedTools reports any tool-installing action whose version input is
// missing or floating, and how many such actions it saw.
//
// The count is returned so the caller can assert a floor on it: an action
// this never recognised and an action correctly pinned are the same
// silence otherwise.
func unpinnedTools(flow workflow) ([]string, int) {
	var problems []string
	count := 0
	for _, step := range flow.steps() {
		for _, want := range toolInstallers {
			if !strings.HasPrefix(step.Uses, want.action) {
				continue
			}
			count++
			value, found := step.input(want.input)
			switch {
			case !found:
				problems = append(problems, fmt.Sprintf(
					"%s is pinned to a SHA and does not set %s, so the TOOL it installs is whatever is "+
						"current that morning", want.action, want.input))
			case !strings.HasPrefix(value, "v") || strings.Contains(value, "~") || value == "latest":
				problems = append(problems, fmt.Sprintf(
					"%s sets %s to %q, which floats", want.action, want.input, value))
			}
		}
	}
	return problems, count
}
