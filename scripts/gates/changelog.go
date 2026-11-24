package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// changelogManifest is the bundle's manifest, which a person reads when
// installing it, so a change to it ships.
const changelogManifest = "packaging/mcpb/manifest.json"

// changelogBuild are the files outside the binary's packages that change
// what is released: the bundle's launcher and the release build itself.
var changelogBuild = []string{changelogManifest, launcherPath, ".goreleaser.yaml"}

// changelogNotedModules need an entry whenever their version moves: the
// MCP SDK decides what the protocol does, and a client sees that.
var changelogNotedModules = []string{"github.com/modelcontextprotocol/go-sdk"}

// changelogGate refuses a pull request that changes what users get
// without adding an entry under [Unreleased], measured from the merge-base
// so a base branch that moved on is not this pull request's change.
func changelogGate(w io.Writer, baseRef, head string) error {
	base, err := gitOutput("merge-base", baseRef, head)
	if err != nil {
		return couldNotRun{err}
	}
	changed, err := gitOutput("diff", "--name-only", "--no-renames", "-z", base, head)
	if err != nil {
		return couldNotRun{err}
	}
	files := strings.FieldsFunc(changed, func(r rune) bool { return r == 0 })
	watched, bumpOnly, err := changelogWatched(base, head, files)
	if err != nil {
		return couldNotRun{err}
	}
	switch {
	case len(watched) == 0 && bumpOnly:
		_, _ = fmt.Fprintf(w, "%d files changed, none shipped, and go.mod only in its require lines; a dependency bump needs no entry\n", len(files))
		return nil
	case len(watched) == 0:
		_, _ = fmt.Fprintf(w, "%d files changed, none of them shipped; no entry needed\n", len(files))
		return nil
	}

	var logs [2][]string
	var texts [2]string
	for i, rev := range []string{base, head} {
		if texts[i], err = gitOutput("show", rev+":CHANGELOG.md"); err != nil {
			return couldNotRun{err}
		}
		logs[i] = strings.Split(texts[i], "\n")
	}
	if cut := releaseCut(logs[0], logs[1]); cut != "" {
		_, _ = fmt.Fprintf(w, "a release cut, moving [Unreleased] under ## [%s]\n", cut)
		return nil
	}
	// More entries than at the merge-base, so an edited entry adds nothing.
	if n := changelogEntries(logs[1], "Unreleased") - changelogEntries(logs[0], "Unreleased"); n <= 0 {
		goModCounted := slices.Contains(watched, "go.mod")
		// The footer is read only while the CHANGELOG is untouched, so it
		// cannot hide a misplaced entry, never for a go.mod change the gate
		// counts, and only from the newest commit, whose footer ends the
		// squashed commit's message.
		if texts[0] == texts[1] && !goModCounted {
			message, err := gitOutput("log", "-1", "--format=%B", head)
			if err != nil {
				return couldNotRun{err}
			}
			if reason := changelogWaiver(message); reason != "" {
				_, _ = fmt.Fprintf(w, "%d shipped file(s) changed, and the newest commit says no entry is needed: %s\n", len(watched), reason)
				return nil
			}
		}
		hint := "If nothing a user sees changed, leave the CHANGELOG as it is and end the newest commit's message " +
			"with `Changelog: none, <why>`"
		if goModCounted {
			hint = "A go.mod change outside its require lines, or a bump of the MCP SDK, always needs one"
		}
		return fmt.Errorf("%d shipped file(s) changed (%s) and no entry was added under ## [Unreleased], "+
			"which the next release's notes are cut from. %s", len(watched), strings.Join(watched[:min(3, len(watched))], ", "), hint)
	}
	_, _ = fmt.Fprintf(w, "%d shipped file(s) changed, and [Unreleased] gained an entry\n", len(watched))
	return nil
}

// changelogWatched are the changed files that change what users get.
// bumpOnly says go.mod changed and was left out as a dependency bump. The
// binary's packages are read from the checkout, for the host's platform, so
// the checkout has to be head or CI's merge commit of it, and a package
// that only another platform imports is not counted.
func changelogWatched(base, head string, files []string) (watched []string, bumpOnly bool, err error) {
	var shipped *shippedSource
	for _, f := range files {
		if f != "go.mod" {
			if shipped == nil {
				if shipped, err = listShipped(); err != nil {
					return nil, false, err
				}
			}
			if shipped.has(f) {
				watched = append(watched, f)
			}
			continue
		}
		before, err := gitOutput("show", base+":go.mod")
		if err != nil {
			return nil, false, err
		}
		after, err := gitOutput("show", head+":go.mod")
		if err != nil {
			return nil, false, err
		}
		if goModBuildLines(before) != goModBuildLines(after) ||
			!slices.Equal(goModNotedRequires(before), goModNotedRequires(after)) {
			watched = append(watched, f)
		} else {
			bumpOnly = true
		}
	}
	return watched, bumpOnly, nil
}

// changelogWaiver is the reason a commit message's footer gives for
// needing no entry: `Changelog: none, <why>`, the token and the word in any
// case. The word ends at a character that cannot be part of a word, so
// `none.` counts and `none_yet` does not. A footer with no reason, or in the
// `Changelog #` reference form, waives nothing.
func changelogWaiver(message string) string {
	wordChar := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	for _, f := range footers(message) {
		if f.ref || !strings.EqualFold(f.token, "Changelog") {
			continue
		}
		text := strings.TrimSpace(f.text)
		end := strings.IndexFunc(text, func(r rune) bool { return !wordChar(r) })
		if end < 0 || !strings.EqualFold(text[:end], "none") {
			continue
		}
		rest := strings.TrimLeftFunc(text[end:], func(r rune) bool { return !wordChar(r) })
		if reason := strings.Join(strings.Fields(rest), " "); reason != "" {
			return reason
		}
	}
	return ""
}

// releaseCut is the version heading head adds when it moves base's
// [Unreleased] entries under it: base's [Unreleased] held some, head's
// holds none, and the new section holds at least as many as base's did.
func releaseCut(base, head []string) string {
	had := changelogEntries(base, "Unreleased")
	if had == 0 || changelogEntries(head, "Unreleased") > 0 {
		return ""
	}
	for _, line := range head {
		version, ok := strings.CutPrefix(line, "## [")
		if !ok {
			continue
		}
		version, _, _ = strings.Cut(version, "]")
		if version == "Unreleased" || slices.ContainsFunc(base, func(l string) bool { return strings.HasPrefix(l, "## ["+version+"]") }) {
			continue
		}
		if changelogEntries(head, version) >= had {
			return version
		}
	}
	return ""
}

// changelogEntries counts the entries under `## [label]`, outside fenced
// code: the bullets at the margin hasEntry looks for.
func changelogEntries(lines []string, label string) int {
	from, to := changelogSection(lines, label)
	var fence fenced
	n := 0
	for _, line := range lines[from:to] {
		if !fence.code(line) && changelogEntry.MatchString(line) {
			n++
		}
	}
	return n
}

// shippedSource is what the binary is built from in this module: its
// package directories and the files they embed, slash-separated from the
// module root.
type shippedSource struct{ dirs, embeds map[string]bool }

// has reports whether a file is one users get: any file but a test in one
// of the binary's package directories, an embedded file, or the bundle's
// manifest. Every file of a package directory counts, since the go command
// builds a package from more than Go (`go help filetype`).
func (s *shippedSource) has(file string) bool {
	return slices.Contains(changelogBuild, file) || s.embeds[file] ||
		!strings.HasSuffix(file, "_test.go") && s.dirs[path.Dir(file)]
}

// listShipped asks the go command what ./cmd/... is built from in this
// module. A package of another module is a dependency, which go.mod's
// require lines name.
func listShipped() (*shippedSource, error) {
	out, err := exec.Command("go", "list", "-deps", "-json=Dir,EmbedFiles,Module", "./cmd/...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps ./cmd/...: %w", err)
	}
	s := &shippedSource{dirs: map[string]bool{}, embeds: map[string]bool{}}
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p struct {
			Dir        string
			EmbedFiles []string
			Module     *struct {
				Main bool
				Dir  string
			}
		}
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		pkg := filepath.ToSlash(rel)
		s.dirs[pkg] = true
		for _, f := range p.EmbedFiles {
			s.embeds[path.Join(pkg, f)] = true
		}
	}
	// An empty listing is not reading the binary, and would excuse every
	// change.
	if len(s.dirs) == 0 {
		return nil, fmt.Errorf("go list -deps ./cmd/... named no package of this module")
	}
	return s, nil
}

// goModBuildLines is a go.mod's lines, each trimmed and the blank ones
// dropped, without its require and tool directives, a block's included
// (https://go.dev/ref/mod#go-mod-file-require). ( is a token of its own,
// so `require(` opens a block as `require (` does, and `require ()` is an
// empty block that opens nothing (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		// A comment may follow any token, so `require ( // direct` still
		// opens a block (https://go.dev/ref/mod#go-mod-file-lexical).
		code, _, _ := strings.Cut(line, "//")
		fields := strings.Fields(code)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren && fields[0] == verb+"(" || len(fields) == 2 && fields[1] == "("
		default:
			kept = append(kept, strings.Join(fields, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// goModNotedRequires are go.mod's require entries, on their own line or in
// a block, that name a module in changelogNotedModules, as module and
// version.
func goModNotedRequires(text string) []string {
	var noted []string
	for _, line := range strings.Split(text, "\n") {
		code, _, _ := strings.Cut(line, "//")
		f := strings.Fields(code)
		if len(f) > 0 && f[0] == "require" {
			f = f[1:]
		}
		if len(f) == 2 && slices.Contains(changelogNotedModules, strings.Trim(f[0], `"`)) {
			noted = append(noted, f[0]+" "+f[1])
		}
	}
	return noted
}
