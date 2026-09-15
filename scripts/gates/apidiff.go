package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// discoveryURL is the Calendar API's published description.
const discoveryURL = "https://www.googleapis.com/discovery/v1/apis/calendar/v3/rest"

// apiDiff refetches the discovery document and rewrites the snapshot.
//
// Manual, because it needs the network, and a gate that fails when
// Google is slow is one people learn to rerun until it passes. What CI
// holds is the committed snapshot, which api-coverage reads offline.
//
// One property this must have, and it is the reason for the temp file:
// on a network failure it fails loudly and leaves the committed file
// UNTOUCHED. A refresh that half-writes is worse than one nobody runs,
// because the next check then holds the record against a snapshot that
// is neither the old truth nor the new one.
func apiDiff(out io.Writer) error {
	before, _ := loadSurface()

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, discoveryURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch the discovery document: %w (the committed snapshot is unchanged)", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discovery document returned %d (the committed snapshot is unchanged)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read the discovery document: %w (the committed snapshot is unchanged)", err)
	}

	var doc struct {
		Version   string                  `json:"version"`
		Revision  string                  `json:"revision"`
		Resources map[string]resourceNode `json:"resources"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse the discovery document: %w (the committed snapshot is unchanged)", err)
	}

	methods := walkResources(doc.Resources, "")
	if len(methods) < 20 {
		return fmt.Errorf("the discovery document yielded only %d methods; refusing to overwrite the snapshot", len(methods))
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })

	next := apiSurface{
		Fetched: time.Now().UTC().Format("2006-01-02"),
		Methods: methods,
	}
	next.APIs = append(next.APIs, struct {
		API      string `json:"api"`
		Version  string `json:"version"`
		Revision string `json:"revision"`
		URL      string `json:"url"`
	}{API: "calendar", Version: doc.Version, Revision: doc.Revision, URL: discoveryURL})

	buf, err := json.MarshalIndent(struct {
		Fetched string `json:"fetched"`
		Note    string `json:"note"`
		APIs    any    `json:"apis"`
		Methods any    `json:"methods"`
	}{
		Fetched: next.Fetched,
		Note:    "Written by `gates api-diff`; nobody edits this. One verdict per method lives in testdata/api-coverage.tsv.",
		APIs:    next.APIs, Methods: next.Methods,
	}, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file and rename, so a failure above this line
	// cannot leave a half-written snapshot.
	tmp := "testdata/api-surface.json.tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil { //nolint:gosec // a committed snapshot, not a secret
		return err
	}
	if err := os.Rename(tmp, "testdata/api-surface.json"); err != nil {
		return err
	}

	if before != nil {
		reportChanges(out, before, &next)
	}
	_, _ = fmt.Fprintf(out, "  snapshot rewritten: %d methods, revision %s\n", len(methods), doc.Revision)
	return nil
}

type resourceNode struct {
	Methods map[string]struct {
		HTTPMethod string `json:"httpMethod"`
		Path       string `json:"path"`
	} `json:"methods"`
	Resources map[string]resourceNode `json:"resources"`
}

type methodRow = struct {
	Name string `json:"name"`
	Verb string `json:"verb"`
	Path string `json:"path"`
}

func walkResources(res map[string]resourceNode, prefix string) []methodRow {
	var out []methodRow
	for name, node := range res {
		full := prefix + name
		for m, mv := range node.Methods {
			out = append(out, methodRow{Name: full + "." + m, Verb: mv.HTTPMethod, Path: mv.Path})
		}
		out = append(out, walkResources(node.Resources, full+".")...)
	}
	return out
}

func reportChanges(out io.Writer, before, after *apiSurface) {
	had := map[string]methodRow{}
	for _, m := range before.Methods {
		had[m.Name] = m
	}
	has := map[string]methodRow{}
	for _, m := range after.Methods {
		has[m.Name] = m
	}
	for name, m := range has {
		old, existed := had[name]
		switch {
		case !existed:
			_, _ = fmt.Fprintf(out, "  NEW     %s (%s %s) — needs a verdict in api-coverage.tsv\n", name, m.Verb, m.Path)
		case old.Path != m.Path || old.Verb != m.Verb:
			_, _ = fmt.Fprintf(out, "  CHANGED %s: %s %s -> %s %s\n", name, old.Verb, old.Path, m.Verb, m.Path)
		}
	}
	for name := range had {
		if _, still := has[name]; !still {
			_, _ = fmt.Fprintf(out, "  GONE    %s — remove its verdict\n", name)
		}
	}
}
