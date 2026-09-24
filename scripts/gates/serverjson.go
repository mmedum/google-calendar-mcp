package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// The MCP registry entry (§12, "the MCP registry entry last").
//
// Last because the registry does a HEAD on the bundle's download URL
// before it accepts the entry, so the release has to exist first. That
// ordering is also why this is not goreleaser's own `mcp` block: that
// block takes a registry type, an identifier and a transport, and has
// nowhere to put a hash — and clients verify the bundle against a
// SHA-256 before installing it, which the registry requires for an MCPB
// package.
//
// So the hash comes from the release's own checksums.txt. The entry then
// describes the bytes that were published rather than a rebuild of them,
// and the file it names is the one already under the cosign signature.
//
// Everything else is derived rather than typed: the owner and repository
// from the module path, the description from the bundle manifest. A
// constant beside either would be a second copy with nothing keeping it
// honest, which is the failure §16 keeps finding one level up.

const registrySchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"

// registrySchemaFile is the vendored copy of the above. The two are held
// together by `schemas` — a document citing one version and validated
// against another is the defect `mcpb` refuses one level down.
const registrySchemaFile = "server-2025-12-11.schema.json"

// descriptionMax is the registry's cap.
const descriptionMax = 100

// sha256Line matches a checksums.txt row: the hash, then the file.
var sha256Line = regexp.MustCompile(`^([a-f0-9]{64})\s+\*?(\S+)$`)

type registryEntry struct {
	Schema      string            `json:"$schema"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	WebsiteURL  string            `json:"websiteUrl"`
	Repository  registryRepo      `json:"repository"`
	Packages    []registryPackage `json:"packages"`
}

type registryRepo struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type registryPackage struct {
	RegistryType string            `json:"registryType"`
	Identifier   string            `json:"identifier"`
	FileSHA256   string            `json:"fileSha256"`
	Version      string            `json:"version"`
	Transport    registryTransport `json:"transport"`
}

type registryTransport struct {
	Type string `json:"type"`
}

// majorVersion matches the /vN a module path carries from v2 onwards.
//
// v2 onwards, not v1: Go's rule is that the major version suffix appears
// from v2, so `/v1` in a module path is an ordinary directory and
// stripping it would name the wrong repository.
var majorVersion = regexp.MustCompile(`^v([2-9]|[1-9][0-9]+)$`)

// githubRepo splits a module path into owner and repository.
//
// The /vN a module gains at v2 is a Go requirement and not part of the
// repository name, so it is stripped rather than refused: refusing it
// would fail the release at the tag, in public, on the day this module
// went to v2 — which is the worst possible moment to discover it.
func githubRepo(module string) (owner, name string, err error) {
	parts := strings.Split(module, "/")
	if len(parts) == 4 && majorVersion.MatchString(parts[3]) {
		parts = parts[:3]
	}
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("module path %q is not github.com/OWNER/REPO", module)
	}
	return parts[1], parts[2], nil
}

// modulePath reads the module this repository is, from go.mod.
func modulePath() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("go.mod names no module")
}

// bundleRow finds the single .mcpb in a checksums file.
//
// Two bundles or none is a release that did not build the way it was
// meant to, and picking one of them would publish a hash for a file
// nobody chose — the same rule the packer's globs follow.
func bundleRow(checksums string) (name, sum string, err error) {
	data, err := os.ReadFile(checksums) //nolint:gosec // a path the release passes in
	if err != nil {
		return "", "", err
	}
	rows := 0
	for _, line := range strings.Split(string(data), "\n") {
		m := sha256Line.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		rows++
		if path.Ext(m[2]) != ".mcpb" {
			continue
		}
		if name != "" {
			return "", "", fmt.Errorf("%s lists more than one .mcpb: %s and %s", checksums, name, m[2])
		}
		name, sum = path.Base(m[2]), m[1]
	}
	switch {
	case rows == 0:
		return "", "", fmt.Errorf("%s has no checksum rows", checksums)
	case name == "":
		return "", "", fmt.Errorf("%s lists no .mcpb among %d rows; the bundle was not packed, "+
			"or was not named in checksum.extra_files", checksums, rows)
	}
	return name, sum, nil
}

// serverJSON writes the registry entry for one release.
func serverJSON(version, checksums string, stdout io.Writer) error {
	module, err := modulePath()
	if err != nil {
		return err
	}
	owner, repo, err := githubRepo(module)
	if err != nil {
		return err
	}

	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	semver := strings.TrimPrefix(tag, "v")
	if semver == "" {
		return fmt.Errorf("empty version")
	}

	m, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	if m.Description == "" {
		return fmt.Errorf("%s has no description for the registry entry to carry", manifestPath)
	}
	if len(m.Description) > descriptionMax {
		return fmt.Errorf("the bundle manifest's description is %d characters and the registry allows %d",
			len(m.Description), descriptionMax)
	}

	name, sum, err := bundleRow(checksums)
	if err != nil {
		return err
	}

	// The registry refuses a package URL that is not a release asset on
	// github.com or gitlab.com. That holds by construction here.
	entry := registryEntry{
		Schema:      registrySchema,
		Name:        fmt.Sprintf("io.github.%s/%s", owner, repo),
		Description: m.Description,
		Version:     semver,
		WebsiteURL:  fmt.Sprintf("https://github.com/%s/%s#readme", owner, repo),
		Repository: registryRepo{
			URL:    fmt.Sprintf("https://github.com/%s/%s", owner, repo),
			Source: "github",
		},
		Packages: []registryPackage{{
			RegistryType: "mcpb",
			Identifier: fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
				owner, repo, tag, name),
			FileSHA256: sum,
			Version:    semver,
			Transport:  registryTransport{Type: "stdio"},
		}},
	}
	out, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	// Against the schema the entry cites, before anybody publishes it.
	// The checks above hold the fields this repository fills in; this
	// holds the document the registry will read. A rejected publish
	// costs a dispatch against a tag that already shipped, and an entry
	// that is accepted and wrong cannot be withdrawn at all — so the
	// refusal belongs here rather than in a test alone, which is also
	// why the publish workflow runs this command rather than a
	// hand-written file.
	if err := validateDocument(registrySchemaFile, "the registry entry", out); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(out))
	return err
}
