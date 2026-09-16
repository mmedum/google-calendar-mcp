package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGithubRepoStripsTheMajorVersion(t *testing.T) {
	cases := []struct {
		module, owner, repo string
		wantErr             bool
	}{
		{module: "github.com/mmedum/google-calendar-mcp", owner: "mmedum", repo: "google-calendar-mcp"},
		// Go requires the /vN from v2 onwards and it is not part of the
		// repository name. Refusing it would fail the release at the
		// tag, in public, the day this module goes to v2.
		{module: "github.com/mmedum/google-calendar-mcp/v2", owner: "mmedum", repo: "google-calendar-mcp"},
		{module: "github.com/mmedum/google-calendar-mcp/v17", owner: "mmedum", repo: "google-calendar-mcp"},
		// /v1 is not a thing a module path carries, so it is a path
		// segment like any other and the shape is wrong.
		{module: "github.com/mmedum/google-calendar-mcp/v1", wantErr: true},
		{module: "example.invalid/mmedum/thing", wantErr: true},
		{module: "github.com/mmedum", wantErr: true},
	}
	for _, tc := range cases {
		owner, repo, err := githubRepo(tc.module)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s was accepted as %s/%s", tc.module, owner, repo)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.module, err)
			continue
		}
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("%s gave %s/%s, want %s/%s", tc.module, owner, repo, tc.owner, tc.repo)
		}
	}
}

func checksums(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneBundle = "" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  google-calendar-mcp_1.0.0.mcpb\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  google-calendar-mcp_1.0.0_linux_amd64.tar.gz\n"

// The bundle's hash is what clients verify before installing, so picking
// the wrong row, or a row that is not there, is worse than failing.
func TestTheBundleRowMustBeExactlyOne(t *testing.T) {
	name, sum, err := bundleRow(checksums(t, oneBundle))
	if err != nil {
		t.Fatalf("a well-formed checksums file was refused: %v", err)
	}
	if name != "google-calendar-mcp_1.0.0.mcpb" || !strings.HasPrefix(sum, "aaaa") {
		t.Fatalf("read %s / %s", name, sum)
	}

	cases := []struct {
		name, body, want string
	}{
		{
			name: "no bundle among the archives",
			body: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  x_linux_amd64.tar.gz\n",
			want: "lists no .mcpb",
		},
		{
			name: "two bundles, so neither was chosen",
			body: oneBundle +
				"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  other.mcpb\n",
			want: "more than one .mcpb",
		},
		{
			name: "an empty file",
			body: "\n",
			want: "no checksum rows",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := bundleRow(checksums(t, tc.body)); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

// The entry the registry receives, end to end, from the repository's own
// module path and bundle manifest.
func TestTheRegistryEntryIsBuiltFromTheRepository(t *testing.T) {
	t.Chdir("../..")

	var out bytes.Buffer
	if err := serverJSON("v1.0.0", checksums(t, oneBundle), &out); err != nil {
		t.Fatalf("serverJSON: %v", err)
	}
	var entry registryEntry
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("the entry is not valid JSON: %v", err)
	}

	if entry.Name != "io.github.mmedum/google-calendar-mcp" {
		t.Errorf("namespace = %q", entry.Name)
	}
	// The version goes in without its v, in both places.
	if entry.Version != "1.0.0" || entry.Packages[0].Version != "1.0.0" {
		t.Errorf("version = %q / %q", entry.Version, entry.Packages[0].Version)
	}
	// And the download URL keeps it, because that is what the tag is.
	if !strings.Contains(entry.Packages[0].Identifier, "/download/v1.0.0/") {
		t.Errorf("identifier = %q", entry.Packages[0].Identifier)
	}
	if entry.Packages[0].FileSHA256 == "" {
		t.Error("the entry carries no hash, which is what a client verifies before installing")
	}
	if entry.Packages[0].RegistryType != "mcpb" || entry.Packages[0].Transport.Type != "stdio" {
		t.Errorf("package = %+v", entry.Packages[0])
	}
	// The description comes from the bundle manifest, so there is one
	// owner for it rather than two that can disagree.
	if entry.Description == "" || len(entry.Description) > descriptionMax {
		t.Errorf("description is %d characters: %q", len(entry.Description), entry.Description)
	}
}

// A version with no v is the same release as one with it.
func TestTheVersionMayCarryItsVOrNot(t *testing.T) {
	t.Chdir("../..")
	path := checksums(t, oneBundle)

	var withV, without bytes.Buffer
	if err := serverJSON("v2.3.4", path, &withV); err != nil {
		t.Fatal(err)
	}
	if err := serverJSON("2.3.4", path, &without); err != nil {
		t.Fatal(err)
	}
	if withV.String() != without.String() {
		t.Fatalf("v2.3.4 and 2.3.4 produced different entries:\n%s\n%s", withV.String(), without.String())
	}
}
