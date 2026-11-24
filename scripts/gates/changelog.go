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
)

// changelogManifest is the bundle's manifest, which a person reads when
// installing it, so a change to it ships.
const changelogManifest = "packaging/mcpb/manifest.json"

// changelogNotedModules need an entry whenever their version moves: the
// MCP SDK decides what the protocol does, and a client sees that.
var changelogNotedModules = []string{"github.com/modelcontextprotocol/go-sdk"}

// changelogGate refuses a pull request that changes what users get
// without adding an entry under [Unreleased]. What users get is a file of
// a package the binary imports, a file one embeds, the bundle's manifest,
// and go.mod outside its require lines; a bump of a module in
// changelogNotedModules counts too. It measures from the merge-base, so a
// base branch that moved on is not this pull request's change.
// [Unreleased] has to hold more entries than at the merge-base, so an
// edited entry adds nothing. A release cut passes, and so does a pull
// request whose commit says why nothing a user sees changed.
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
		_, _ = fmt.Fprintf(w, "%d files changed, go.mod only in the modules it requires; a dependency bump needs no entry\n", len(files))
		return nil
	case len(watched) == 0:
		_, _ = fmt.Fprintf(w, "%d files changed, none of them shipped; no entry needed\n", len(files))
		return nil
	}

	var logs [2][]string
	for i, rev := range []string{base, head} {
		text, err := gitOutput("show", rev+":CHANGELOG.md")
		if err != nil {
			return couldNotRun{err}
		}
		logs[i] = strings.Split(text, "\n")
	}
	if cut := releaseCut(logs[0], logs[1]); cut != "" {
		_, _ = fmt.Fprintf(w, "a release cut, moving [Unreleased] under ## [%s]\n", cut)
		return nil
	}
	if n := changelogEntries(logs[1], "Unreleased") - changelogEntries(logs[0], "Unreleased"); n <= 0 {
		log, err := gitOutput("log", "--format=%B%x00", base+".."+head)
		if err != nil {
			return couldNotRun{err}
		}
		if reason := changelogWaiver(strings.Split(log, "\x00")); reason != "" {
			_, _ = fmt.Fprintf(w, "%d shipped file(s) changed, and a commit says no entry is needed: %s\n", len(watched), reason)
			return nil
		}
		return fmt.Errorf("%d shipped file(s) changed (%s) and no entry was added under ## [Unreleased], "+
			"which the next release's notes are cut from. If nothing a user sees changed, end a commit's message with "+
			"`Changelog: none, <why>`", len(watched), strings.Join(watched[:min(3, len(watched))], ", "))
	}
	_, _ = fmt.Fprintf(w, "%d shipped file(s) changed, and [Unreleased] gained an entry\n", len(watched))
	return nil
}

// changelogWatched are the changed files that change what users get.
// bumpOnly says go.mod changed and was left out as a dependency bump. The
// binary's packages are read from the checkout, so the checkout has to be
// head or CI's merge commit of it.
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

// changelogWaiver is the reason a commit footer gives for needing no
// entry: `Changelog: none, <why>`, the token in any case. A footer with
// no reason after "none" waives nothing.
func changelogWaiver(messages []string) string {
	for _, message := range messages {
		for _, f := range footers(message) {
			if !strings.EqualFold(f.token, "Changelog") {
				continue
			}
			rest, ok := strings.CutPrefix(strings.TrimSpace(f.text), "none")
			if reason := strings.TrimSpace(strings.TrimLeft(rest, ",;:-— ")); ok && reason != "" {
				return reason
			}
		}
	}
	return ""
}

// releaseCut is the version heading head adds when it moves base's
// [Unreleased] entries under it: the new section holds at least as many
// entries as base's [Unreleased], and at least one.
func releaseCut(base, head []string) string {
	for _, line := range head {
		version, ok := strings.CutPrefix(line, "## [")
		if !ok {
			continue
		}
		version, _, _ = strings.Cut(version, "]")
		if version == "Unreleased" || slices.ContainsFunc(base, func(l string) bool { return strings.HasPrefix(l, "## ["+version+"]") }) {
			continue
		}
		if n := changelogEntries(head, version); n > 0 && n >= changelogEntries(base, "Unreleased") {
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
	return file == changelogManifest || s.embeds[file] ||
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
// so `require(` opens a block as `require (` does
// (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren || len(fields) > 1 && strings.HasPrefix(fields[1], "(")
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
