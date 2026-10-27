package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// Every staged binary and the launcher arrive executable, whatever mode
// they were staged in, and the license and the README arrive readable.
//
// This packer writes 0755 into the zip deliberately rather than copying
// the source's mode, because the reference implementation forces the bit
// on the entry point and copies the mode for the rest — a binary packed
// 0644 installs and cannot run. The fixture stages its files 0600, so
// this fails the day somebody replaces that constant with the source's
// own mode, which is the regression it is here for. The modes are
// literals, not staged.mode(), so the assertion does not check the code
// against itself.
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

	want := map[string]os.FileMode{
		"manifest.json":                          0o644,
		"LICENSE":                                0o644,
		"README.md":                              0o644,
		"server/launch-linux.sh":                 0o755,
		"server/google-calendar-mcp-darwin":      0o755,
		"server/google-calendar-mcp.exe":         0o755,
		"server/google-calendar-mcp-linux-amd64": 0o755,
		"server/google-calendar-mcp-linux-arm64": 0o755,
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
		mode, ok := want[f.Name]
		if !ok {
			t.Errorf("the bundle carries %s, which is not the family's layout", f.Name)
			continue
		}
		if got := f.Mode().Perm(); got != mode {
			t.Errorf("%s is packed %v, want %v", f.Name, got, mode)
		}
	}
	if len(names) != len(want) {
		t.Errorf("the bundle carries %v; want the %d files of the family's layout", names, len(want))
	}
}

// The license and the README are the repository's own, byte for byte.
func TestTheBundleCarriesTheLicenseAndTheReadme(t *testing.T) {
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
	for _, name := range []string{"LICENSE", "README.md"} {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(z.File, func(f *zip.File) bool { return f.Name == name })
		if i < 0 {
			t.Errorf("the bundle carries no %s", name)
			continue
		}
		rc, err := z.File[i].Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the packed %s is not the repository's", name)
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

// The packer refuses what the gate refuses, because both start from
// committedManifest: a check the gate alone used to run stops the pack,
// and so does a file beside the manifest.
func TestPackRunsEveryCheckTheGateRuns(t *testing.T) {
	root := copiedRoot(t)
	t.Chdir(root)
	dist := fakeDist(t)
	out := filepath.Join(t.TempDir(), "b.mcpb")
	if err := packMCPB(dist, "1.0.0", out); err != nil {
		t.Fatalf("the copied repository does not pack: %v", err)
	}
	manifest := string(repoFile(t, manifestPath))
	for _, tc := range []struct{ name, file, old, new, want string }{
		{"no word of the login", manifestPath, "THIS BUNDLE DOES NOT LOG YOU IN", "THIS BUNDLE IS READY",
			"long_description does not say the bundle cannot log the user in"},
		{"another license", "LICENSE", "Apache License", "MIT License",
			`the manifest says the license is "Apache-2.0" and LICENSE is MIT`},
		{"a relative command", manifestPath, `"command": "${__dirname}/server/google-calendar-mcp-darwin"`,
			`"command": "server/google-calendar-mcp-darwin"`, "a command starts with ${__dirname}/"},
		{"an argument", manifestPath, `"args": []`, `"args": ["-sharing=off"]`, `mcp_config.args is ["-sharing=off"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := manifest
			if tc.file != manifestPath {
				body = string(repoFile(t, tc.file))
			}
			if !strings.Contains(body, tc.old) {
				t.Fatalf("%s does not say %q", tc.file, tc.old)
			}
			put(t, root, tc.file, strings.ReplaceAll(body, tc.old, tc.new))
			t.Cleanup(func() { put(t, root, tc.file, body) })
			err := packMCPB(dist, "1.0.0", filepath.Join(t.TempDir(), "b.mcpb"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("pack = %v, want it refused, naming %q", err, tc.want)
			}
		})
	}
}

// A version that is not Semantic Versioning's is refused, which the
// vendored schema would not do: it takes any string as the version.
// What goreleaser passes, the tag without its v, and a snapshot's
// version with a pre-release after it, pack.
func TestPackRefusesAVersionThatIsNotSemver(t *testing.T) {
	t.Chdir("../..")
	dist := fakeDist(t)
	for _, version := range []string{"v1.2.3", "1.2.3 ", " 1.2.3", "1.2", "01.2.3", "dev"} {
		err := packMCPB(dist, version, filepath.Join(t.TempDir(), "b.mcpb"))
		want := "pack needs a version such as 1.2.3, with no v and no spaces; " + strconv.Quote(version) + " is not one"
		if err == nil || err.Error() != want {
			t.Errorf("packing version %q gave %v, want %q", version, err, want)
		}
	}
	for _, version := range []string{"1.2.3", "5.0.1-SNAPSHOT-88c5e56", "1.2.3-rc.1+build.5"} {
		if err := packMCPB(dist, version, filepath.Join(t.TempDir(), "b.mcpb")); err != nil {
			t.Errorf("packing version %q: %v", version, err)
		}
	}
}

// What ships is held to the vendored schema, not only what is
// committed, since the stamp encodes the document again.
func TestTheStampedManifestIsHeldToTheSchema(t *testing.T) {
	if _, err := stampManifest(repoFile(t, manifestPath), "1.2.3"); err != nil {
		t.Fatalf("the committed manifest does not stamp: %v", err)
	}
	_, err := stampManifest(editedRaw(t, func(d map[string]any) { d["keywords"] = "calendar" }), "1.2.3")
	if want := "the stamped manifest does not satisfy mcpb-manifest-v0.3.schema.json"; err == nil ||
		!strings.Contains(err.Error(), want) {
		t.Errorf("stamping a document the schema refuses gave %v, want an error containing %q", err, want)
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
