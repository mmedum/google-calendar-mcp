package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The release body, taken from CHANGELOG.md rather than generated.
//
// goreleaser will happily build notes from commit subjects, and that
// publishes our commit hygiene to somebody deciding whether to upgrade.
// The changelog entry is the note: it was written for that reader.
//
// release.yml writes this to a file OUTSIDE the checkout and passes it
// with --release-notes. Outside, because goreleaser refuses to release
// from a dirty tree and an untracked file in the working directory is
// dirty — `release --clean` empties dist/ and has nothing to say about
// anything else. `--snapshot` does not run that check at all, so a
// rehearsal cannot find it and the first tag is the first time it fails.

const changelogPath = "CHANGELOG.md"

var (
	// semverVersion is a release's version: semver.org's grammar with its
	// prerelease identifiers and without build metadata, which a Go module
	// version cannot carry
	// (https://semver.org/spec/v2.0.0.html#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string,
	// https://go.dev/ref/mod#versions).
	semverVersion = regexp.MustCompile(`^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)` +
		`(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	// changelogEntry is an entry: a bullet at the margin. A subheading or
	// a line of prose tells a reader nothing changed.
	changelogEntry = regexp.MustCompile(`^[-*+][ \t]+\S`)
)

// releaseNotes writes one version's section of the changelog.
func releaseNotes(version, file string, out io.Writer) error {
	if file == "" {
		file = changelogPath
	}
	raw, err := os.ReadFile(file) //nolint:gosec // a repository path, from the Makefile or a test
	if err != nil {
		return err
	}
	bare := strings.TrimPrefix(version, "v")
	if !semverVersion.MatchString(bare) {
		return fmt.Errorf("%s is not a semver version, so no CHANGELOG section can be its notes", version)
	}
	body := sectionFor(string(raw), bare)
	if body == "" {
		return fmt.Errorf("%s has no section for %s; the release body would be empty, and a tag is "+
			"not the moment to discover the entry was never written", file, version)
	}
	if !hasEntry(body) {
		return fmt.Errorf("%s's section for %s holds no entry, a bullet at the margin outside fenced code; "+
			"the release body would say nothing changed", file, version)
	}
	_, err = fmt.Fprintln(out, promoteHeadings(body))
	return err
}

// sectionFor returns the body under `## [version]`, topped and tailed.
func sectionFor(changelog, version string) string {
	lines := strings.Split(changelog, "\n")
	from, to := changelogSection(lines, version)
	return trimBlank(lines[from:to])
}

// changelogSection is the range of lines under `## [version]`, empty when
// there is no such heading. The docs gate scans the [Unreleased] range it
// gives, so the scan reads what a release note will say.
//
// It stops at the next heading and at the link footer. The footer is not
// part of any section, but it follows the OLDEST one with no heading in
// between — so without that second stop the oldest release's notes end
// with a block of compare links.
func changelogSection(lines []string, version string) (from, to int) {
	want := "## [" + version + "]"
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, want) })
	if start < 0 {
		return 0, 0
	}
	var fence fenced
	for to = start + 1; to < len(lines); to++ {
		line := lines[to]
		// Inside a fenced block nothing is a heading, so an entry showing a
		// markdown example or a link definition does not end the section.
		if !fence.code(line) && (strings.HasPrefix(line, "## ") || isLinkDefinition(line)) {
			break
		}
	}
	return start + 1, to
}

// hasEntry reports whether a section holds an entry outside fenced code.
func hasEntry(body string) bool {
	var fence fenced
	for _, line := range strings.Split(body, "\n") {
		if !fence.code(line) && changelogEntry.MatchString(line) {
			return true
		}
	}
	return false
}

// trimBlank joins lines without the blank ones at either end.
func trimBlank(lines []string) string {
	blank := func(line string) bool { return strings.TrimSpace(line) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// isLinkDefinition reports whether the line is a markdown link
// definition, which the changelog's compare-link footer is made of.
func isLinkDefinition(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	end := strings.Index(line, "]")
	return end > 0 && strings.HasPrefix(line[end:], "]: ")
}

// promoteHeadings lifts every heading one level, because the file and
// the page are two different documents.
//
// In CHANGELOG.md the version is an h2 with its change kinds as h3s
// under it. On the release page that version heading is gone — GitHub
// renders the tag as the page's h1 — so an unaltered section starts at
// h3 directly under an h1, a skipped rank. Lifting one level gives h1
// then h2 with nothing missing between them.
//
// Fenced code is left alone: a `# comment` in a shell block is not a
// heading, and this changelog carries such blocks. Only h3 and deeper
// are lifted, so this can never emit a second h1.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	var fence fenced
	for i, line := range lines {
		if !fence.code(line) && strings.HasPrefix(line, "###") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}
