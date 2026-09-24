package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The vendored schemas are only worth their provenance: without the
// recorded digest, "make the document pass" and "edit the schema" are
// the same amount of work, and the second one is silent.
func TestAVendoredSchemaIsHeldToItsRecordedDigest(t *testing.T) {
	for name := range vendoredSchemas {
		if _, err := loadSchema(name); err != nil {
			t.Errorf("the committed %s does not match its recorded digest: %v", name, err)
		}
	}

	// And the check bites: a schema whose digest is not the recorded
	// one is refused rather than used.
	saved := vendoredSchemas["mcpb-manifest-v0.3.schema.json"]
	t.Cleanup(func() { vendoredSchemas["mcpb-manifest-v0.3.schema.json"] = saved })
	wrong := saved
	wrong.sha256 = strings.Repeat("0", 64)
	vendoredSchemas["mcpb-manifest-v0.3.schema.json"] = wrong

	_, err := loadSchema("mcpb-manifest-v0.3.schema.json")
	if err == nil {
		t.Fatal("a schema that does not match its recorded digest was used anyway")
	}
	if !strings.Contains(err.Error(), "recorded digest") {
		t.Errorf("the message does not say what is wrong: %v", err)
	}
}

// Every vendored file is recorded, and every record has a file. A
// schema in the directory that nothing names is a schema nothing
// checks, and a record naming a file that is not there fails only when
// something reaches for it.
func TestTheVendoredFilesAndTheRecordsAgree(t *testing.T) {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		t.Fatal(err)
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		onDisk[e.Name()] = true
		if _, recorded := vendoredSchemas[e.Name()]; !recorded {
			t.Errorf("schemas/%s is vendored and has no recorded digest, so nothing holds it", e.Name())
		}
	}
	for name, v := range vendoredSchemas {
		if !onDisk[name] {
			t.Errorf("a digest is recorded for %s, which is not in schemas/", name)
		}
		if v.source == "" {
			t.Errorf("%s records no source, so a re-fetch has nowhere to read from", name)
		}
	}
	if len(entries) < 2 {
		t.Fatalf("found %d vendored schema(s); the registry entry and the bundle manifest are both held", len(entries))
	}
}

// The committed manifest and a built registry entry satisfy the schemas
// they cite — the claim the $schema checks make and cannot test.
func TestThisRepositorysDocumentsSatisfyTheirSchemas(t *testing.T) {
	t.Chdir("../..")

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDocument(mcpbSchemaFile, "the committed manifest", raw); err != nil {
		t.Errorf("%v", err)
	}

	entry := exampleEntry()
	out, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDocument(registrySchemaFile, "a registry entry", out); err != nil {
		t.Errorf("%v", err)
	}
}

// And the validation bites, which is the half a passing document cannot
// show. Each case breaks one thing the registry cares about.
//
// The cases are what the schema ACTUALLY constrains, which is less than
// it sounds like: `version` is a string of at most 255 characters with
// no pattern, and `registryType` is a bare string with no enum, so
// "not a version" and "tarball" are both valid documents. Two cases
// started out as those, passed, and were replaced — the schema is the
// floor, not the whole check, and `server-json`'s own rules are what
// hold the parts it leaves open.
//
// The mutations are applied to the DOCUMENT rather than to the struct,
// because the interesting failure is a missing field and a Go struct
// without omitempty cannot express one: `Version = ""` still emits the
// key, which this schema accepts.
func TestARegistryEntryThatBreaksTheSchemaIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(doc map[string]any)
		want   string
	}{
		{
			name:   "a name outside the namespace shape",
			breaks: func(doc map[string]any) { doc["name"] = "not a server name" },
			want:   "does not satisfy",
		},
		{
			// The cap this repository already enforces itself. Both
			// numbers are held against the schema now rather than
			// against each other.
			name:   "a description past the registry's cap",
			breaks: func(doc map[string]any) { doc["description"] = strings.Repeat("x", descriptionMax+1) },
			want:   "does not satisfy",
		},
		{
			name:   "no version at all",
			breaks: func(doc map[string]any) { delete(doc, "version") },
			want:   "does not satisfy",
		},
		{
			name: "a file hash that is not a sha256",
			breaks: func(doc map[string]any) {
				pkgs := doc["packages"].([]any)
				pkgs[0].(map[string]any)["fileSha256"] = "nope"
			},
			want: "does not satisfy",
		},
		{
			name: "a package with no transport",
			breaks: func(doc map[string]any) {
				pkgs := doc["packages"].([]any)
				delete(pkgs[0].(map[string]any), "transport")
			},
			want: "does not satisfy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := exampleEntryDocument(t)
			tc.breaks(doc)
			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			err = validateDocument(registrySchemaFile, "a registry entry", out)
			if err == nil {
				t.Fatalf("%s was accepted, and an entry cannot be withdrawn", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("wanted %q in: %v", tc.want, err)
			}
		})
	}
}

// exampleEntryDocument is a valid entry as the JSON the registry would
// read, so a test can take a field away.
func exampleEntryDocument(t *testing.T) map[string]any {
	t.Helper()
	out, err := json.Marshal(exampleEntry())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func exampleEntry() registryEntry {
	return registryEntry{
		Schema:      registrySchema,
		Name:        "io.github.example/example-mcp",
		Description: "An example server, for a document these tests can hold without a release.",
		Version:     "1.2.3",
		WebsiteURL:  "https://github.com/example/example-mcp#readme",
		Repository:  registryRepo{URL: "https://github.com/example/example-mcp", Source: "github"},
		Packages: []registryPackage{{
			RegistryType: "mcpb",
			Identifier:   "https://github.com/example/example-mcp/releases/download/v1.2.3/example-mcp_1.2.3.mcpb",
			FileSHA256:   strings.Repeat("a", 64),
			Version:      "1.2.3",
			Transport:    registryTransport{Type: "stdio"},
		}},
	}
}
