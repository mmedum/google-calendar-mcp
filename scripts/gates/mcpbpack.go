package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The packer. It runs at release time, from the universal binary's post
// hook, which is the one point in the pipeline where every binary exists
// and checksums.txt has not been written yet — that is what MAKES it
// possible for the bundle to be in that file, and therefore under the
// signature. It is not what puts it there: goreleaser hashes the
// artifacts it built, so `checksum.extra_files` is what covers a file a
// hook drops into dist/, and `release.extra_files` is what uploads it.
//
// A .mcpb is a deflate zip with the manifest at the root and the
// binaries under server/. The standard library writes one, which is why
// this is Go and not the official Node CLI: `make check` does not get an
// interpreter nobody declared.

// zipTime is stamped on every entry, so the same inputs give a
// byte-identical archive. Unset writes zeroes, which display as the
// impossible 1980-00-00.
var zipTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// releaseVersion is a Semantic Versioning 2.0.0 version, by the
// expression semver.org gives: what goreleaser passes for a tag, the tag
// without its v, and for a snapshot, with a pre-release after it. The
// vendored schema takes any string as the version, so a hook passing
// {{ .Tag }} or a stray space would otherwise ship.
var releaseVersion = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// packMCPB writes the bundle for one version.
func packMCPB(dist, version, out string) error {
	if version == "" || version == placeholderVersion {
		return fmt.Errorf("pack needs the real version; %q is the committed placeholder", version)
	}
	if !releaseVersion.MatchString(version) {
		return fmt.Errorf("pack needs a version such as 1.2.3, with no v and no spaces; %q is not one", version)
	}

	// The gate's own start, read once. The placeholder version is among
	// the checks: a committed manifest claiming a version means somebody
	// stamped it in the tree, and the next build would ship whatever they
	// left behind.
	raw, _, problems, err := committedManifest(".")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("the manifest does not describe the bundle this would pack:\n  %s",
			strings.Join(problems, "\n  "))
	}
	stamped, err := stampManifest(raw, version)
	if err != nil {
		return err
	}

	sources := map[string]string{}
	for _, f := range bundleFiles {
		path, ferr := sourceFor(dist, f)
		if ferr != nil {
			return ferr
		}
		sources[f.path] = path
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	file, err := os.Create(out) //nolint:gosec // the output path is the caller's
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	zw := zip.NewWriter(file)
	if err := addEntry(zw, "manifest.json", stamped, 0o644); err != nil {
		return err
	}
	// Sorted, so two packs of the same inputs write the same bytes.
	files := slices.Clone(bundleFiles)
	slices.SortFunc(files, func(a, b staged) int { return strings.Compare(a.path, b.path) })
	for _, f := range files {
		// 0755 on every staged binary and the launcher. The packer forces
		// the execute bit on the entry point alone and copies the
		// filesystem mode for the rest, so the Windows binary arrives
		// unrunnable unless it is staged executable.
		//
		// Streamed rather than read whole: the macOS slot is a universal
		// binary, so holding one in memory to deflate it is tens of
		// megabytes for no reason.
		if err := addFile(zw, f.path, sources[f.path], f.mode()); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	// Close's error is the pack's: the last of the archive can reach the
	// disk only there, and a bundle cut short must not pass. The deferred
	// Close then does nothing.
	if err := file.Close(); err != nil {
		return err
	}

	fmt.Printf("  %s: %d files, version %s\n", out, len(sources)+1, version)
	return nil
}

// stampManifest is the committed manifest raw with version written in,
// as the bundle carries it. The version goes in through a decode and an
// encode, because a sed over JSON is how a quote ends up inside a
// string. That encode sorts the keys and escapes <, > and &, so what it
// returns is the committed document but not its bytes, and it is held
// to the vendored schema again before anything ships it.
func stampManifest(raw []byte, version string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document["version"] = version
	stamped, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := validateDocument(mcpbSchemaFile, "the stamped manifest", stamped); err != nil {
		return nil, err
	}
	return stamped, nil
}

func addEntry(zw *zip.Writer, name string, body []byte, mode os.FileMode) error {
	w, err := entryWriter(zw, name, mode)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// addFile copies one file into the archive without holding it in memory.
func addFile(zw *zip.Writer, name, path string, mode os.FileMode) error {
	w, err := entryWriter(zw, name, mode)
	if err != nil {
		return err
	}
	f, err := os.Open(path) //nolint:gosec // a path this packer resolved itself
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func entryWriter(zw *zip.Writer, name string, mode os.FileMode) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime}
	header.SetMode(mode)
	return zw.CreateHeader(header)
}

// sourceFor finds the file a staged entry comes from.
//
// A glob must match exactly one file and fails loudly on two: the layout
// under dist/ carries a build id and an amd64 variant, and a bundle
// packed from the wrong binary is not something a checksum catches —
// the checksum is of whatever was built.
func sourceFor(dist string, f staged) (string, error) {
	if f.source != "" {
		if _, err := os.Stat(f.source); err != nil {
			return "", fmt.Errorf("%s: %w", f.path, err)
		}
		return f.source, nil
	}
	pattern := f.glob
	if dist != "dist" {
		pattern = filepath.Join(dist, trimDistPrefix(f.glob))
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s: nothing matches %s", f.path, pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s: %s matches %d files (%v); a bundle packed from the wrong binary "+
			"is not something a checksum catches", f.path, pattern, len(matches), matches)
	}
}

func trimDistPrefix(glob string) string {
	if rest, ok := strings.CutPrefix(glob, "dist/"); ok {
		return rest
	}
	return glob
}
