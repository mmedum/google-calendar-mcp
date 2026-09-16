package main

import (
	"fmt"
	"io"
	"os"
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

// releaseNotes writes one version's section of the changelog.
func releaseNotes(version, file string, out io.Writer) error {
	if file == "" {
		file = changelogPath
	}
	raw, err := os.ReadFile(file) //nolint:gosec // a repository path, from the Makefile or a test
	if err != nil {
		return err
	}
	body := sectionFor(string(raw), strings.TrimPrefix(version, "v"))
	if body == "" {
		return fmt.Errorf("%s has no section for %s; the release body would be empty, and a tag is "+
			"not the moment to discover the entry was never written", file, version)
	}
	_, err = fmt.Fprintln(out, promoteHeadings(body))
	return err
}

// sectionFor returns the body under `## [version]`, topped and tailed.
//
// It stops at the next heading and at the link footer. The footer is not
// part of any section, but it follows the OLDEST one with no heading in
// between — so without that second stop the oldest release's notes end
// with a block of compare links.
func sectionFor(changelog, version string) string {
	want := "## [" + version + "]"
	var body []string
	inside, fenced := false, false
	for _, line := range strings.Split(changelog, "\n") {
		// Inside a fenced block nothing is a heading. promoteHeadings
		// already knew that and this did not, so an entry showing a
		// markdown example or a link definition truncated the release
		// body at that line with every step still green.
		if inside && strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			body = append(body, line)
			continue
		}
		switch {
		case !fenced && strings.HasPrefix(line, want):
			inside = true
			continue
		case !inside:
			continue
		case fenced:
			// Taken verbatim.
		case strings.HasPrefix(line, "## "), isLinkDefinition(line):
			return trimBlank(body)
		case len(body) == 0 && strings.TrimSpace(line) == "":
			continue // the blank lines under the heading
		}
		body = append(body, line)
	}
	return trimBlank(body)
}

func trimBlank(body []string) string {
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
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
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "###") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}
