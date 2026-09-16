package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A changelog with the two shapes that trip the extractor: a section
// followed by another heading, and the oldest section followed by the
// link footer with no heading between them.
const sampleChangelog = `# Changelog

## [Unreleased]

### Added

- Something not released yet.

## [1.1.0] - 2026-09-16

### Added

- The working-hours mask.

` + "```bash" + `
# not a heading
make check
` + "```" + `

## [1.0.0] - 2026-09-15

### Added

- The first release.

[Unreleased]: https://example.invalid/compare/v1.1.0...HEAD
[1.1.0]: https://example.invalid/compare/v1.0.0...v1.1.0
`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func notes(t *testing.T, version, path string) string {
	t.Helper()
	var out bytes.Buffer
	if err := releaseNotes(version, path, &out); err != nil {
		t.Fatalf("releaseNotes(%s): %v", version, err)
	}
	return out.String()
}

// A tag is not the moment to discover the entry was never written.
func TestAVersionWithNoSectionIsRefused(t *testing.T) {
	path := write(t, sampleChangelog)
	var out bytes.Buffer
	if err := releaseNotes("v9.9.9", path, &out); err == nil {
		t.Fatal("a version with no changelog section produced a release body")
	}
	if out.Len() > 0 {
		t.Fatalf("wrote a body for a version it refused: %q", out.String())
	}
}

func TestTheTagMayCarryItsVOrNot(t *testing.T) {
	path := write(t, sampleChangelog)
	if withV, without := notes(t, "v1.1.0", path), notes(t, "1.1.0", path); withV != without {
		t.Fatalf("v1.1.0 and 1.1.0 gave different notes:\n%q\n%q", withV, without)
	}
}

func TestASectionStopsAtTheNextHeading(t *testing.T) {
	body := notes(t, "v1.1.0", write(t, sampleChangelog))
	if !strings.Contains(body, "The working-hours mask") {
		t.Fatalf("the section is missing its own content:\n%s", body)
	}
	if strings.Contains(body, "The first release") {
		t.Fatalf("the section ran into the release below it:\n%s", body)
	}
	if strings.Contains(body, "Something not released yet") {
		t.Fatalf("the section picked up Unreleased:\n%s", body)
	}
}

// The footer follows the OLDEST section with no heading in between, so
// without a second stop that release's notes end in compare links.
func TestTheOldestSectionStopsAtTheLinkFooter(t *testing.T) {
	body := notes(t, "v1.0.0", write(t, sampleChangelog))
	if !strings.Contains(body, "The first release") {
		t.Fatalf("the oldest section is missing its content:\n%s", body)
	}
	if strings.Contains(body, "example.invalid") {
		t.Fatalf("the oldest section ran into the link footer:\n%s", body)
	}
}

// GitHub renders the tag as the page's h1, so an unlifted section starts
// at h3 under it — a skipped rank.
func TestHeadingsAreLiftedAndFencedCodeIsNot(t *testing.T) {
	body := notes(t, "v1.1.0", write(t, sampleChangelog))
	if !hasLine(body, "## Added") {
		t.Fatalf("### Added was not lifted to ##:\n%s", body)
	}
	if hasLine(body, "### Added") {
		t.Fatalf("### Added survived the lift:\n%s", body)
	}
	if !strings.Contains(body, "# not a heading") {
		t.Fatalf("a comment inside a fenced block was treated as a heading:\n%s", body)
	}
}

// Every released version in the committed changelog must extract to
// something, because that text IS the release page.
//
// Unreleased is exempt, deliberately: straight after a release it is
// legitimately empty, and asserting otherwise would fail on the one
// commit that cuts a version — exactly when nobody wants to be debugging
// a gate.
func TestEveryReleasedVersionHasNotes(t *testing.T) {
	path := repoPath(t, changelogPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	versions := 0
	for _, line := range strings.Split(string(raw), "\n") {
		name, ok := strings.CutPrefix(line, "## [")
		if !ok {
			continue
		}
		end := strings.Index(name, "]")
		if end < 0 {
			t.Errorf("malformed changelog heading: %q", line)
			continue
		}
		if name = name[:end]; name == "Unreleased" {
			continue
		}
		versions++
		if got := notes(t, name, path); strings.TrimSpace(got) == "" {
			t.Errorf("version %s has no notes, so its release page would be a footer and nothing else", name)
		}
	}
	if versions == 0 {
		t.Skip("nothing released yet")
	}
}

// hasLine reports whether the body carries this line exactly, so an
// assertion about a heading does not depend on what precedes it.
func hasLine(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

// A changelog entry may show markdown, and a fenced block is not
// structure. Without fence tracking the body stopped at the example.
func TestAFencedExampleDoesNotEndTheSection(t *testing.T) {
	const withExample = "# Changelog\n\n" +
		"## [2.0.0] - 2026-09-17\n\n" +
		"### Added\n\n- A release body that shows markdown:\n\n" +
		"```markdown\n## Added\n[1.0.0]: https://example.invalid/v1.0.0\n```\n\n" +
		"- And a bullet after the block.\n\n" +
		"## [1.0.0] - 2026-09-15\n\n- The first release.\n"

	body := notes(t, "v2.0.0", write(t, withExample))
	if !strings.Contains(body, "And a bullet after the block") {
		t.Fatalf("the section stopped inside the fenced example:\n%s", body)
	}
	if strings.Contains(body, "The first release") {
		t.Fatalf("the section ran past its own heading:\n%s", body)
	}
	// The example's own text survives unaltered: it is content, not
	// headings to lift.
	if !strings.Contains(body, "## Added\n[1.0.0]: https://example.invalid/v1.0.0") {
		t.Fatalf("the fenced example was rewritten:\n%s", body)
	}
}
