package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		// Go requires the /vN from v2 onward and it is not part of the
		// repository name. Refusing it would fail the release at the
		// tag, in public, the day this module goes to v2.
		{module: "github.com/mmedum/google-calendar-mcp/v2", owner: "mmedum", repo: "google-calendar-mcp"},
		{module: "github.com/mmedum/google-calendar-mcp/v17", owner: "mmedum", repo: "google-calendar-mcp"},
		// /v1 is not a thing a module path carries, so it is a path
		// segment like any other and the shape is wrong.
		{module: "github.com/mmedum/google-calendar-mcp/v1", wantErr: true},
		// Only one suffix comes off, and only a major version.
		{module: "github.com/mmedum/google-calendar-mcp/v2/v2", wantErr: true},
		{module: "github.com/mmedum/google-calendar-mcp/internal", wantErr: true},
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

const testModule = "github.com/mmedum/google-calendar-mcp"

func TestModuleMajor(t *testing.T) {
	for module, want := range map[string]int{
		testModule:          1,
		testModule + "/v2":  2,
		testModule + "/v12": 12,
		// Go adds the suffix from v2, so /v1 and /v0 are ordinary
		// directories rather than versions.
		testModule + "/v1": 1,
		testModule + "/v0": 1,
	} {
		if got := moduleMajor(module); got != want {
			t.Errorf("moduleMajor(%s) = %d, want %d", module, got, want)
		}
	}
}

func TestTagMajor(t *testing.T) {
	for tag, want := range map[string]int{
		"v2.0.0":     2,
		"v2.0.0-rc1": 2,
		"v0.3.0":     0,
		"v12.1.0":    12,
	} {
		if got, err := tagMajor(tag); err != nil || got != want {
			t.Errorf("tagMajor(%s) = %d (%v), want %d", tag, got, err, want)
		}
	}
	// strconv reads a sign, so v+1.0.0 would otherwise be major 1.
	for _, tag := range []string{"2.0.0", "v2", "vnext", "v", "", "v.1.0", "v+1.0.0", "v-1.0.0"} {
		if got, err := tagMajor(tag); err == nil {
			t.Errorf("tag %q was read as major %d", tag, got)
		}
	}
}

// A tag and the module path it is cut from must name the same major, or
// `go install ...@latest` serves the wrong one.
func TestTheTagMajorMustBeTheModules(t *testing.T) {
	for _, tc := range []struct {
		tag, module string
		want        string // "" passes; otherwise a fragment of the refusal
	}{
		{tag: "v2.0.0", module: testModule + "/v2"},
		{tag: "v2.0.0-rc1", module: testModule + "/v2"},
		{tag: "v0.3.0", module: testModule},
		{tag: "v1.6.0", module: testModule},
		{tag: "v1.6.0", module: testModule + "/v2", want: "a v1 tag needs no /vN suffix"},
		{tag: "v2.0.0", module: testModule, want: "a v2 tag needs /v2"},
		{tag: "v0.3.0", module: testModule + "/v2", want: "is major 2"},
		{tag: "release-2", module: testModule + "/v2", want: "not vMAJOR.MINOR.PATCH"},
	} {
		err := tagMatchesModule(tc.tag, tc.module)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s on %s was refused: %v", tc.tag, tc.module, err)
		case tc.want != "" && err == nil:
			t.Errorf("%s on %s passed", tc.tag, tc.module)
		case tc.want != "" && !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s on %s: wanted %q, got %v", tc.tag, tc.module, tc.want, err)
		}
	}
}

// The dispatch path reaches the registry without release.yml's check, so
// the entry refuses a tag whose major is not go.mod's on its own.
func TestTheEntryRefusesATagOfAnotherMajor(t *testing.T) {
	t.Chdir("../..")
	module, err := modulePath()
	if err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("v%d.0.0", moduleMajor(module)+1)
	var out bytes.Buffer
	err = serverJSON(tag, checksums(t, oneBundle), &out)
	if err == nil || !strings.Contains(err.Error(), "needs /v") {
		t.Fatalf("%s on %s gave %v", tag, module, err)
	}
	if out.Len() > 0 {
		t.Fatalf("a refused tag still wrote an entry: %s", out.String())
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

	module, err := modulePath()
	if err != nil {
		t.Fatal(err)
	}
	// The release's major is go.mod's, or serverJSON refuses it.
	version := fmt.Sprintf("%d.0.0", moduleMajor(module))

	var out bytes.Buffer
	if err := serverJSON("v"+version, checksums(t, oneBundle), &out); err != nil {
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
	if entry.Version != version || entry.Packages[0].Version != version {
		t.Errorf("version = %q / %q", entry.Version, entry.Packages[0].Version)
	}
	// And the download URL keeps it, because that is what the tag is.
	if !strings.Contains(entry.Packages[0].Identifier, "/download/v"+version+"/") {
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
