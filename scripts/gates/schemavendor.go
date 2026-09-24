package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// The published schemas this repository's own documents cite, kept here
// so they can be checked rather than merely named.
//
// `mcpb` and `server-json` already hold each document's $schema to being
// present, pinned and agreeing with the version beside it. That is a
// claim ABOUT the reference; none of it opens the schema. A document can
// cite the right file and not satisfy it, and for the registry entry
// that is the expensive direction: an entry cannot be withdrawn, so
// "fails in somebody else's validator" is not a state to recover from.
//
// Vendored rather than fetched, because a gate that needs the network
// fails on a train, and a check that only runs when a CDN answers is not
// a check. Embedded rather than read from disk, so the gate does not
// depend on the directory it was started in.
//
// What vendoring does NOT buy, said here because it reads as though it
// does: a frozen copy cannot notice upstream publishing a NEWER schema.
// The hash below proves these bytes are the ones somebody reviewed, not
// that upstream still serves them. Only a re-fetch shows that, which is
// why `make schema-refetch` exists and why docs/release.md runs it at
// release time rather than this file claiming to cover it.
//
//go:embed schemas/*.json
var schemaFS embed.FS

type vendoredSchema struct {
	// source is where the bytes came from, so a re-fetch has one place
	// to read the URL from rather than a person remembering it.
	source string
	// sha256 is the digest of what was reviewed. A vendored copy is
	// worth its provenance: without this, "make the document pass" and
	// "edit the schema" are the same amount of work.
	sha256 string
}

var vendoredSchemas = map[string]vendoredSchema{
	"server-2025-12-11.schema.json": {
		source: registrySchema,
		sha256: "3fba09590c99f61735d234822279f4223fab9e300c0a81e81c91ab62a4114de0",
	},
	"mcpb-manifest-v0.3.schema.json": {
		source: "https://raw.githubusercontent.com/anthropics/mcpb/v2.1.2/schemas/mcpb-manifest-v0.3.schema.json",
		sha256: "3a0ac9d845711a1b9b17dfa5a52f8b60628239d6a86a9db417206a9efc78592d",
	},
}

// loadSchema returns the vendored schema, having first checked that the
// bytes are the ones recorded.
func loadSchema(name string) (*jsonschema.Resolved, error) {
	want, known := vendoredSchemas[name]
	if !known {
		return nil, fmt.Errorf("no vendored schema named %q; the names are the files under scripts/gates/schemas", name)
	}
	raw, err := schemaFS.ReadFile("schemas/" + name)
	if err != nil {
		return nil, fmt.Errorf("read vendored %s: %w", name, err)
	}
	if got := hex.EncodeToString(sha256Of(raw)); got != want.sha256 {
		return nil, fmt.Errorf(
			"vendored %s hashes to %s and the recorded digest is %s; if it was refreshed on purpose, "+
				"update the digest in the same commit, and re-read what changed rather than only the hash",
			name, got, want.sha256)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("vendored %s is not a valid schema document: %w", name, err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("resolve vendored %s: %w", name, err)
	}
	return resolved, nil
}

func sha256Of(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// validateDocument holds one of this repository's own documents to the
// schema it cites. `what` names the document in the failure, because
// "does not validate" without it sends the reader to the wrong file.
func validateDocument(schemaName, what string, raw []byte) error {
	resolved, err := loadSchema(schemaName)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", what, err)
	}
	if err := resolved.Validate(doc); err != nil {
		return fmt.Errorf("%s does not satisfy %s, which it cites: %w", what, schemaName, err)
	}
	return nil
}

// schemaRefetch is the half a vendored copy cannot do for itself.
//
// The digest proves these bytes are the ones somebody reviewed. It says
// nothing about whether upstream still serves them, or whether a newer
// format has been published beside them — a frozen copy is frozen in
// both directions. So this fetches each source and reports a difference
// rather than papering over one.
//
// It does not write. A refresh is a decision: somebody reads what
// changed, updates the digest in the same commit, and re-checks the
// documents against the new file. Rewriting the copy here would make
// that decision silently, on a run somebody did to ask a question.
func schemaRefetch(stdout io.Writer) error {
	names := make([]string, 0, len(vendoredSchemas))
	for name := range vendoredSchemas {
		names = append(names, name)
	}
	sort.Strings(names)

	var drifted []string
	for _, name := range names {
		want := vendoredSchemas[name]
		req, err := http.NewRequest(http.MethodGet, want.source, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", want.source, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", want.source, err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch %s: HTTP %d", want.source, resp.StatusCode)
		}
		upstream := hex.EncodeToString(sha256Of(body))
		if upstream == want.sha256 {
			_, _ = fmt.Fprintf(stdout, "  %s: unchanged upstream\n", name)
			continue
		}
		drifted = append(drifted, fmt.Sprintf(
			"%s has changed upstream: the vendored copy is %s and %s now serves %s",
			name, want.sha256[:12], want.source, upstream[:12]))
	}
	if len(drifted) > 0 {
		return fmt.Errorf("%s\n\nRead what changed, refresh the file, and update the digest in the same "+
			"commit — then re-run the gates, which hold this repository's documents against the new file",
			strings.Join(drifted, "\n"))
	}
	_, _ = fmt.Fprintf(stdout, "  %d vendored schema(s) still match what their sources serve\n", len(names))
	return nil
}
