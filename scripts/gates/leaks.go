package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// leakGate looks for deployer-specific data in the working tree, and
// with history=true in every blob and commit message as well.
//
// It is an ALLOW-LIST, and every rule is anchored on a shape this
// server's own generated output cannot take. That construction is
// deliberate: a leak gate cannot be patterns alone when the payload is
// ordinary words, and a pattern that is not anchored collides with
// something innocent and then gets ignored. A sibling's scan matched a
// four-character fixture value inside a timestamp and failed about one
// run in twenty-five — frequent enough to erode trust, rare enough to be
// called flaky.
//
// A calendar is the worst case for this: every event carries other
// people's email addresses.
func leakGate(history bool) error {
	rules := leakRules()
	if len(rules) < 4 {
		return fmt.Errorf("only %d leak rules; that is not the allow-list this repository needs", len(rules))
	}

	files, err := scannableFiles()
	if err != nil {
		return err
	}
	// The floor. `fd` and `rg` skip gitignored paths by default, which
	// is exactly what a sweep for build residue is looking for, so this
	// walks the tree itself — and asserts it found a real repository.
	if len(files) < 20 {
		return fmt.Errorf("only %d files scanned; that is not this repository", len(files))
	}

	var findings []string
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		findings = append(findings, scanOne(path, string(data), rules)...)
	}

	if history {
		more, err := scanHistory(rules)
		if err != nil {
			return err
		}
		findings = append(findings, more...)
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		return fmt.Errorf("possible deployer-specific data:\n  %s", strings.Join(findings, "\n  "))
	}
	scope := "working tree"
	if history {
		scope = "working tree and full history"
	}
	fmt.Printf("  %d files, %d rules, %s: clean\n", len(files), len(rules), scope)
	return nil
}

type leakRule struct {
	name string
	re   *regexp.Regexp
	// allow are the shapes this rule may match without it being a
	// finding. The list is asserted, so a third entry is an argued
	// decision rather than a quiet widening.
	allow []*regexp.Regexp
}

func leakRules() []leakRule {
	return []leakRule{
		{
			// An address needs an @ AND a dot-suffixed domain. A bare @
			// matches Go doc comments and struct tags; this shape cannot
			// be produced by a timestamp or an id.
			name: "email address",
			re:   regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
			allow: []*regexp.Regexp{
				// RFC 2606 reserves these and they can never resolve.
				regexp.MustCompile(`@([A-Za-z0-9.\-]+\.)?(example|test|invalid|localhost)(\.[A-Za-z]{2,})?$`),
				regexp.MustCompile(`@group\.calendar\.example\.test$`),
				// The project's own contact points.
				regexp.MustCompile(`@users\.noreply\.github\.com$`),
				regexp.MustCompile(`@noreply\.anthropic\.com$`),
				// The commit-attribution address, which is the other
				// shape: noreply@anthropic.com, not @noreply.anthropic.com.
				// The entry above anticipated this and spelled it wrong,
				// so `leaks-history` failed on every commit message in
				// the repository — and nobody knew, because it is the one
				// gate `make check` does not run.
				//
				// Argued rather than widened (§9.1): it is a vendor's
				// non-routable no-reply address in a Co-Authored-By
				// trailer, structurally identical to the GitHub noreply
				// above, and it says nothing about a deployer, an
				// organization or a person's calendar.
				regexp.MustCompile(`^noreply@anthropic\.com$`),
			},
		},
		{
			name: "OAuth client id",
			re:   regexp.MustCompile(`\b\d{10,}-[a-z0-9]{20,}\.apps\.googleusercontent\.com\b`),
		},
		{
			name: "Google API key",
			re:   regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),
		},
		{
			// A calendar id for a secondary calendar. Anchored on the
			// literal Google suffix, which nothing generated here emits.
			name: "Google group calendar id",
			re:   regexp.MustCompile(`\b[a-z0-9]{20,}@group\.calendar\.google\.com\b`),
		},
		{
			name: "Google Calendar event URL",
			re:   regexp.MustCompile(`https://(www\.)?google\.com/calendar/event\?eid=[A-Za-z0-9_\-]+`),
		},
		{
			// A refresh token's literal prefix.
			name: "OAuth refresh token",
			re:   regexp.MustCompile(`\b1//[0-9A-Za-z_\-]{20,}\b`),
		},
	}
}

func scanOne(path, content string, rules []leakRule) []string {
	var out []string
	for _, rule := range rules {
		for _, m := range rule.re.FindAllString(content, -1) {
			allowed := false
			for _, a := range rule.allow {
				if a.MatchString(m) {
					allowed = true
					break
				}
			}
			if !allowed {
				out = append(out, fmt.Sprintf("%s: %s %q", path, rule.name, redactFinding(m)))
			}
		}
	}
	return out
}

// redactFinding keeps the finding from becoming the leak. Printing the
// whole value into CI output publishes it a second time.
func redactFinding(s string) string {
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

// scannableFiles walks the tree itself rather than asking a
// gitignore-aware tool, because the residue worth finding is exactly
// what such a tool skips.
func scannableFiles() ([]string, error) {
	var out []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "dist", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 2<<20 {
			return nil
		}
		switch filepath.Ext(path) {
		case ".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".gz", ".mcpb", ".exe":
			return nil
		}
		// A committed binary is the finding, not something to scan: its
		// symbol table buries the line that matters.
		if isExecutable(info) && filepath.Ext(path) == "" {
			out = append(out, path)
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func isExecutable(info os.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }

func scanHistory(rules []leakRule) ([]string, error) {
	var out []string

	msgs, err := exec.Command("git", "log", "--all", "--format=%H%n%B").Output()
	if err != nil {
		return nil, fmt.Errorf("read commit messages: %w", err)
	}
	out = append(out, scanOne("git log", string(msgs), rules)...)

	blobs, err := exec.Command("git", "rev-list", "--objects", "--all").Output()
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	seen := 0
	for _, line := range strings.Split(string(blobs), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(fields) != 2 || fields[1] == "" {
			continue
		}
		content, err := exec.Command("git", "cat-file", "-p", fields[0]).Output()
		if err != nil || len(content) > 2<<20 {
			continue
		}
		seen++
		out = append(out, scanOne("blob "+fields[1], string(content), rules)...)
	}
	if seen == 0 {
		return nil, fmt.Errorf("history scan read no blobs; that is not a real repository")
	}
	return out, nil
}
