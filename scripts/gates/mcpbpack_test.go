package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests that read a packed bundle BACK, which nothing in this family had.
// The manifest checks hold the document; these hold the archive, and the
// two fail in different ways.

// fakeDist builds a dist tree shaped like the one goreleaser leaves, so
// the packer's globs resolve without a real build.
func fakeDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	for dir, name := range map[string]string{
		"google-calendar-mcp-universal_darwin_all": "google-calendar-mcp",
		"google-calendar-mcp_linux_amd64_v1":       "google-calendar-mcp",
		"google-calendar-mcp_linux_arm64_v8.0":     "google-calendar-mcp",
		"google-calendar-mcp_windows_amd64_v1":     "google-calendar-mcp.exe",
	} {
		full := filepath.Join(dist, dir)
		if err := os.MkdirAll(full, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, name), []byte("binary for "+dir), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dist
}

// The claim §12 makes out loud: rebuilding a tag reproduces it. The
// binaries get that from -trimpath and mod_timestamp; the bundle gets it
// only if the packer stamps its own entries, and nothing else in any of
// these repositories would notice if it stopped.
func TestTwoPacksOfTheSameInputsAreByteIdentical(t *testing.T) {
	t.Chdir("../..")
	dist := fakeDist(t)
	out := t.TempDir()

	first := filepath.Join(out, "one.mcpb")
	second := filepath.Join(out, "two.mcpb")
	if err := packMCPB(dist, "1.2.3", first); err != nil {
		t.Fatalf("first pack: %v", err)
	}
	if err := packMCPB(dist, "1.2.3", second); err != nil {
		t.Fatalf("second pack: %v", err)
	}

	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("two packs of the same inputs differ: %d vs %d bytes. A zip entry with an unset "+
			"modification time takes the clock, and the release stops being reproducible",
			len(a), len(b))
	}

	// And the property directly, because the comparison above does NOT
	// discriminate on its own: zip stores DOS timestamps at two-second
	// granularity, so two packs a millisecond apart are byte-identical
	// even with time.Now(). Watched: swapping zipTime for time.Now()
	// left the comparison green. Asserting the stamp is what catches it.
	z, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()
	// The expected instant is a LITERAL, not zipTime. Asserting
	// f.Modified equals zipTime puts the same variable on both sides: it
	// catches the stamp being replaced at the call site, which is how
	// this was first found, and passes vacuously if zipTime itself
	// becomes time.Now(). Writing the date out is what makes the
	// assertion independent of the thing it is checking.
	want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range z.File {
		if got := f.Modified.UTC(); !got.Equal(want) {
			t.Errorf("%s is stamped %s, not the fixed %s; the archive takes the clock and a rebuild "+
				"of the same tag stops reproducing", f.Name, got, want)
		}
	}
}

// The version reaches the manifest through a decode and an encode. A
// substitution over text is how a quote ends up inside a string, and the
// committed manifest must keep its placeholder.
func TestThePackedManifestCarriesTheRealVersion(t *testing.T) {
	t.Chdir("../..")
	out := filepath.Join(t.TempDir(), "b.mcpb")
	if err := packMCPB(fakeDist(t), "9.8.7", out); err != nil {
		t.Fatalf("pack: %v", err)
	}

	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()

	var found bool
	for _, f := range z.File {
		if f.Name != "manifest.json" {
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			t.Fatalf("the packed manifest is not valid JSON: %v", err)
		}
		_ = rc.Close()
		if m["version"] != "9.8.7" {
			t.Fatalf("packed manifest says version %v", m["version"])
		}
		// Everything else survived the rewrite.
		if m["name"] == nil || m["server"] == nil {
			t.Fatal("the rewrite dropped fields; it must decode and encode, not replace a line")
		}
		// Including the declaration, which is the half `make mcpb`
		// reads from the committed file while this is what ships. Same
		// check, over the manifest that is actually in the bundle.
		schema, _ := m["$schema"].(string)
		declared, _ := m["manifest_version"].(string)
		support, _ := m["support"].(string)
		if problems := manifestShapeProblems(schema, declared, support); len(problems) > 0 {
			t.Fatalf("the packed manifest's own declaration is wrong:\n%s", strings.Join(problems, "\n"))
		}
	}
	if !found {
		t.Fatal("no manifest.json at the bundle root")
	}

	// And the committed one still carries the placeholder.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var committed map[string]any
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatal(err)
	}
	if committed["version"] != placeholderVersion {
		t.Fatalf("the committed manifest says %v; a version stamped in the tree ships whatever "+
			"somebody left behind", committed["version"])
	}
}

// Every staged file arrives executable, whatever mode it was staged in.
//
// This packer writes 0755 into the zip deliberately rather than copying
// the source's mode, because the reference implementation forces the bit
// on the entry point and copies the mode for the rest — a binary packed
// 0644 installs and cannot run. The fixture stages its files 0600, so
// this fails the day somebody replaces that constant with the source's
// own mode, which is the regression it is here for.
func TestEveryStagedBinaryIsExecutable(t *testing.T) {
	t.Chdir("../..")
	out := filepath.Join(t.TempDir(), "b.mcpb")
	if err := packMCPB(fakeDist(t), "1.0.0", out); err != nil {
		t.Fatalf("pack: %v", err)
	}
	z, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = z.Close() }()

	for _, f := range z.File {
		if f.Name == "manifest.json" {
			continue
		}
		if mode := f.Mode(); mode&0o111 == 0 {
			t.Errorf("%s is packed %v, which installs and cannot run", f.Name, mode)
		}
	}
}

// A dist tree missing a binary is refused rather than packed short. A
// bundle with four of five files installs and then fails on one platform.
func TestAnIncompleteDistIsRefused(t *testing.T) {
	t.Chdir("../..")
	dist := fakeDist(t)
	if err := os.RemoveAll(filepath.Join(dist, "google-calendar-mcp_windows_amd64_v1")); err != nil {
		t.Fatal(err)
	}
	err := packMCPB(dist, "1.0.0", filepath.Join(t.TempDir(), "b.mcpb"))
	if err == nil {
		t.Fatal("a dist tree with no Windows binary was packed anyway")
	}
}

// The placeholder is refused as a version, so a release cannot ship a
// bundle claiming to be the committed development copy.
func TestPackRefusesThePlaceholder(t *testing.T) {
	t.Chdir("../..")
	if err := packMCPB(fakeDist(t), placeholderVersion, filepath.Join(t.TempDir(), "b.mcpb")); err == nil {
		t.Fatal("the placeholder was accepted as a release version")
	}
}
