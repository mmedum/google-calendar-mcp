package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// schemaAck fails a pull request whose tool surface differs from its
// merge-base's, unless a commit it adds says so. Annotations count: a
// flipped destructiveHint changes whether a client asks the person.
// Both sides are built from commits, the merge-base and the head, never
// from the checkout, which in CI is the merge commit.
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
	changes := compareSurfaces(os.Stdout, sides[0], sides[1], "the merge-base "+short)
	log, err := gitOutput("log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return err
	}
	return checkAck(changes, strings.Split(log, "\x00"), short)
}

// addTheFooter is how to fix a pull request schema-ack fails.
const addTheFooter = "add an empty commit (git commit --allow-empty) whose message carries it, never an amend"

// checkAck is the rule, over what changed and the commit messages, so a
// test can hand it both.
func checkAck(c surfaceChanges, messages []string, base string) error {
	if c.empty() {
		fmt.Printf("  the tool surface is unchanged since %s\n", base)
		return nil
	}
	var acked, ackedBreak bool
	for _, message := range messages {
		for _, line := range footers(message) {
			if m := ackFooter.FindStringSubmatch(line); m != nil {
				acked = true
				ackedBreak = ackedBreak || m[1] != "SCHEMA-CHANGE"
			}
		}
	}
	switch {
	case len(c.breaking) > 0 && !ackedBreak:
		return fmt.Errorf("the pull request breaks the tool surface and no commit since %s says BREAKING CHANGE:; %s", base, addTheFooter)
	case !acked:
		return fmt.Errorf("the pull request changes the tool surface and no commit since %s says SCHEMA-CHANGE: or BREAKING CHANGE:; %s",
			base, addTheFooter)
	}
	fmt.Printf("  the change since %s is acknowledged\n", base)
	return nil
}

// ackFooter is a footer that acknowledges a change to the tool surface:
// SCHEMA-CHANGE: for any change, BREAKING CHANGE: for one that breaks a
// caller, or BREAKING-CHANGE:, which Conventional Commits makes its
// synonym. The text after the colon says what changed.
var ackFooter = regexp.MustCompile(`^(SCHEMA-CHANGE|BREAKING[ -]CHANGE):`)

// footerToken is a footer line: a token, a colon, a space and a value,
// as Co-Authored-By: is. continuation is a line that starts with white
// space and continues the footer above it.
var (
	footerToken  = regexp.MustCompile(`^(BREAKING CHANGE|[A-Za-z][A-Za-z0-9-]*):[ \t]+\S`)
	continuation = regexp.MustCompile(`^[ \t]+\S`)
)

// paragraphBreak is a blank line, which ends a paragraph.
var paragraphBreak = regexp.MustCompile(`\n[ \t]*\n`)

// footers is a commit message's footer block: the paragraphs at its end
// in which every line is a footer, never the subject. A line of prose
// that happens to start "BREAKING CHANGE:" is not a footer.
func footers(message string) []string {
	paras := paragraphBreak.Split(strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n")), -1)
	var out []string
	for i := len(paras) - 1; i > 0; i-- {
		lines := strings.Split(strings.Trim(paras[i], "\n"), "\n")
		for j, l := range lines {
			if !footerToken.MatchString(l) && (j == 0 || !continuation.MatchString(l)) {
				return out
			}
		}
		out = append(lines, out...)
	}
	return out
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
