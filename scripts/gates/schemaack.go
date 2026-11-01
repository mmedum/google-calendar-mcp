package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// schemaAck fails a pull request whose tool surface differs from its
// merge-base's, unless the commits it adds name every item that changed
// in a footer. Annotations count: a flipped destructiveHint changes
// whether a client asks the person. Both sides are built from commits,
// the merge-base and the head, never from the checkout, which in CI is
// the merge commit.
func schemaAck(baseRef, head string) error {
	base, err := gitOutput("merge-base", baseRef, head)
	if err != nil {
		return err
	}
	var sides [2]surface
	for i, rev := range []string{base, head} {
		data, err := dumpAt(rev)
		if err != nil {
			return err
		}
		if sides[i], err = readSurface(data); err != nil {
			return fmt.Errorf("%s: %w", rev, err)
		}
	}
	short := base[:min(12, len(base))]
	c := compareSurfaces(os.Stdout, sides[0], sides[1], "the merge-base "+short)
	if len(c.breaking) > 0 {
		released, err := releasedBreaks(baseRef, sides[1])
		if err != nil {
			return err
		}
		c = releaseBreaksOnly(os.Stdout, c, released)
	}
	log, err := gitOutput("log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return err
	}
	return checkAck(c, strings.Split(log, "\x00"), short)
}

// releasedBreaks is what head breaks of the newest release's surface: the
// baseline the base branch records, not the head's, which a branch cut
// before the last release would leave behind. Before the first release
// there is no baseline, and nothing is a break.
func releasedBreaks(baseRef string, head surface) ([]change, error) {
	listed, err := gitOutput("ls-tree", "--name-only", baseRef, "--", baselineFile)
	if err != nil || listed == "" {
		return nil, err
	}
	data, err := gitOutput("show", baseRef+":"+baselineFile)
	if err != nil {
		return nil, err
	}
	release, err := readSurface([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", baselineFile, baseRef, err)
	}
	return compareSurfaces(io.Discard, release, head, baselineFile).breaking, nil
}

// releaseBreaksOnly keeps as breaks only the changes the head also makes
// against the released surface. Any other break narrows something no
// release shipped, so it breaks no caller and is a plain change, which
// it says on w, since the report above it called it a break.
func releaseBreaksOnly(w io.Writer, c surfaceChanges, released []change) surfaceChanges {
	var breaking []change
	for _, b := range c.breaking {
		if slices.Contains(released, b) {
			breaking = append(breaking, b)
		} else {
			_, _ = fmt.Fprintf(w, "    %s breaks nothing a release shipped, so it is a change\n", b.text)
			c.changed = append(c.changed, b)
		}
	}
	c.breaking = breaking
	return c
}

// checkAck is the rule, over the changes and the commit messages, so a
// test can hand it both. Every added or changed item must be named in an
// acknowledging footer, and every break in a BREAKING CHANGE: one.
func checkAck(c surfaceChanges, messages []string, base string) error {
	if c.empty() {
		fmt.Printf("  the tool surface is unchanged since %s\n", base)
		return nil
	}
	named, breaks := acknowledged(messages)
	var problems []string
	if items := unnamed(c.breaking, breaks); len(items) > 0 {
		problems = append(problems, "no BREAKING CHANGE: footer names "+strings.Join(items, ", "))
	}
	if items := unnamed(slices.Concat(c.added, c.changed), named); len(items) > 0 {
		problems = append(problems, "no footer names "+strings.Join(items, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("the tool surface changed since %s, and %s. Add an empty commit (git commit --allow-empty) "+
			"whose message ends with a footer naming them, such as `SCHEMA-CHANGE: create_event: destructiveHint true`; "+
			"never amend", base, strings.Join(problems, ", and "))
	}
	fmt.Printf("  the change since %s is acknowledged\n", base)
	return nil
}

// unnamed is each item of cs that names does not hold, sorted, once.
func unnamed(cs []change, names map[string]bool) []string {
	var out []string
	for _, c := range cs {
		if !names[c.item] {
			out = append(out, c.item)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// footerToken is a footer line: a token, a colon, a space and a value,
// as Co-Authored-By: is. continuation is a line that starts with white
// space and continues the footer above it.
var (
	footerToken  = regexp.MustCompile(`^(BREAKING CHANGE|[A-Za-z][A-Za-z0-9-]*):[ \t]+(\S.*)$`)
	continuation = regexp.MustCompile(`^[ \t]+\S`)
)

// paragraphBreak is a blank line, which ends a paragraph.
var paragraphBreak = regexp.MustCompile(`\n[ \t]*\n`)

// footers is a commit message's footer block, one string per footer
// with its continuation lines joined on: the paragraphs at its end in
// which every line is a footer, never the subject. A line of prose that
// happens to start "BREAKING CHANGE:" is not a footer.
func footers(message string) []string {
	paras := paragraphBreak.Split(strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n")), -1)
	var out []string
	for i := len(paras) - 1; i > 0; i-- {
		var block []string
		for j, l := range strings.Split(strings.Trim(paras[i], "\n"), "\n") {
			switch {
			case footerToken.MatchString(l):
				block = append(block, l)
			case j > 0 && continuation.MatchString(l):
				block[len(block)-1] += " " + strings.TrimSpace(l)
			default:
				return out
			}
		}
		out = append(block, out...)
	}
	return out
}

// ackTokens are the footers that acknowledge a change to the tool
// surface, each saying whether it acknowledges a break of a release.
// BREAKING-CHANGE: is the synonym Conventional Commits defines.
var ackTokens = map[string]bool{"SCHEMA-CHANGE": false, "BREAKING CHANGE": true, "BREAKING-CHANGE": true}

// nameSeparator splits a footer's text into the names it may carry. A
// colon and a full stop are trimmed from a name rather than split on,
// since a URI holds both; a name in backticks is taken whole, so a URI
// with a comma in it can be named.
var nameSeparator = regexp.MustCompile("[\\s,;()'\"]+")

// acknowledged reads every message's footers for the items they name:
// named holds each one a SCHEMA-CHANGE: or BREAKING CHANGE: footer
// names, breaks each one a BREAKING CHANGE: footer names. A name is a
// whole token, so create_event does not name create_events.
func acknowledged(messages []string) (named, breaks map[string]bool) {
	named, breaks = map[string]bool{}, map[string]bool{}
	for _, message := range messages {
		for _, f := range footers(message) {
			m := footerToken.FindStringSubmatch(f)
			isBreak, ok := ackTokens[m[1]]
			if !ok {
				continue
			}
			text := m[2]
			var names []string
			for _, span := range codeSpanRe.FindAllStringSubmatch(text, -1) {
				names = append(names, span[1])
			}
			names = append(names, nameSeparator.Split(codeSpanRe.ReplaceAllString(text, " "), -1)...)
			for _, n := range names {
				if n = strings.TrimRight(n, ":."); n != "" {
					named[n] = true
					breaks[n] = breaks[n] || isBreak
				}
			}
		}
	}
	return named, breaks
}

// dumpAt builds the server as it was at a revision, trimmed and without
// cgo as `make build` does, and dumps its surface. The worktree is removed on
// every path out, a failed build included, and a removal that fails says
// so rather than leave a registration that breaks the next run.
func dumpAt(rev string) (data []byte, err error) {
	tmp, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return nil, err
	}
	src := filepath.Join(tmp, "src")
	if _, err := gitOutput("worktree", "add", "--quiet", "--detach", src, rev); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	defer func() {
		_, rmErr := gitOutput("worktree", "remove", "--force", src)
		_ = os.RemoveAll(tmp)
		_, _ = gitOutput("worktree", "prune")
		if rmErr != nil && err == nil {
			err = rmErr
		}
	}()
	bin := filepath.Join(tmp, "google-calendar-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "./cmd/google-calendar-mcp")
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %w: %s", rev, err, out)
	}
	return dumpBytes(bin)
}

// gitOutput runs git and returns its trimmed output, or an error that
// carries git's own message.
func gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
