package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// schemaDiff compares the binary's tool surface against the last tag's,
// and fails on a change that breaks a caller: a tool or resource removed,
// an input or output field removed, or an input field newly required.
// Anything else that changed is reported for a person to look at.
//
// With no previous tag it prints the surface and passes: a first release
// has nothing to diff against, and failing here would block the commit
// that creates the baseline.
func schemaDiff(bin string) error {
	current, err := dumpFrom(bin)
	if err != nil {
		return err
	}
	if len(current) < 5 {
		return fmt.Errorf("the binary published %d tools; that is not the surface", len(current))
	}

	// A tag is the best baseline, and a committed snapshot is the one
	// that exists during a phased build. Without the fallback this gate
	// reported "no previous tag" on every run from the first commit to
	// the first release — which is exactly the stretch where the tool
	// surface changes most, so it was inert when it was most needed.
	previous, against, prevFields, err := baseline(current)
	if err != nil {
		return err
	}
	if previous == nil {
		fmt.Printf("  no baseline yet; %d tools in the current surface. "+
			"`make schema-baseline` records it\n", len(current))
		return nil
	}

	var removed, changed []string
	for name, prev := range previous {
		now, still := current[name]
		if !still {
			removed = append(removed, name)
			continue
		}
		if prev != now {
			changed = append(changed, name)
		}
	}
	var added []string
	for name := range current {
		if _, had := previous[name]; !had {
			added = append(added, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	sort.Strings(added)

	fmt.Printf("  against %s: %d added, %d changed, %d removed\n", against, len(added), len(changed), len(removed))
	for _, n := range added {
		fmt.Printf("    + %s\n", n)
	}
	for _, n := range changed {
		what := "input schema or description changed"
		if strings.HasPrefix(n, resourcePrefix) {
			what = "description or type changed"
		}
		fmt.Printf("    ~ %s (%s)\n", n, what)
	}
	for _, n := range removed {
		fmt.Printf("    - %s  BREAKING\n", n)
	}
	curFields, err := currentFields(bin)
	if err != nil {
		return err
	}
	breaking := append(removed, brokenFields(prevFields, curFields)...)
	for _, b := range breaking[len(removed):] {
		fmt.Printf("    ! %s  BREAKING\n", b)
	}
	// Failed, not reported: a released surface is a contract, and a
	// removal that is right goes out as a major version, whose release
	// commit records the new baseline on purpose.
	if len(breaking) > 0 {
		return fmt.Errorf("the tool surface breaks a caller since %s: %d change(s) above", against, len(breaking))
	}
	return nil
}

// toolFields is what a caller relies on in each tool: the fields it may
// send, the ones it must, and the ones it reads back.
type toolFields struct {
	inputs, required, outputs map[string]bool
}

// fieldsOf reads every tool's fields out of a dump. A dump that carries
// no output schema, as one from before they were dumped, compares only
// its inputs.
func fieldsOf(data []byte) (map[string]toolFields, error) {
	type schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	var dump struct {
		Tools []struct {
			Name         string `json:"name"`
			InputSchema  schema `json:"input_schema"`
			OutputSchema schema `json:"output_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, err
	}
	out := map[string]toolFields{}
	for _, t := range dump.Tools {
		f := toolFields{inputs: map[string]bool{}, required: map[string]bool{}, outputs: map[string]bool{}}
		for p := range t.InputSchema.Properties {
			f.inputs[p] = true
		}
		for _, r := range t.InputSchema.Required {
			f.required[r] = true
		}
		for p := range t.OutputSchema.Properties {
			f.outputs[p] = true
		}
		out[t.Name] = f
	}
	return out, nil
}

// brokenFields lists what a tool kept by name lost: an input or output
// field removed, or an input newly required. A removed tool is the
// caller's to report.
func brokenFields(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, f := range sortedKeys(was.inputs) {
			if !now.inputs[f] {
				out = append(out, fmt.Sprintf("%s: input field %s removed", name, f))
			}
		}
		for _, f := range sortedKeys(now.required) {
			if !was.required[f] {
				out = append(out, fmt.Sprintf("%s: input field %s newly required", name, f))
			}
		}
		for _, f := range sortedKeys(was.outputs) {
			if !now.outputs[f] {
				out = append(out, fmt.Sprintf("%s: output field %s removed", name, f))
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func currentFields(bin string) (map[string]toolFields, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	return fieldsOf(out)
}

func dumpFrom(bin string) (map[string]string, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	return parseDump(out)
}

func parseDump(data []byte) (map[string]string, error) {
	var dump struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		Resources []struct {
			Name        string `json:"name"`
			URI         string `json:"uri"`
			URITemplate string `json:"uri_template"`
			MIMEType    string `json:"mime_type"`
			Description string `json:"description"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range dump.Tools {
		out[t.Name] = t.Description + "\x00" + string(t.InputSchema)
	}
	// Resources share the map, keyed by the URI a client would ask for.
	// They are part of the surface: a resource whose URI changed breaks
	// a client exactly as a renamed tool does, and a baseline that held
	// only tools would not have said so.
	for _, r := range dump.Resources {
		uri := r.URI
		if uri == "" {
			uri = r.URITemplate
		}
		out[resourceKey(uri)] = r.Name + "\x00" + r.MIMEType + "\x00" + r.Description
	}
	return out, nil
}

func lastTag() (string, error) {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// resourcePrefix distinguishes a resource from a tool in the one map
// both gates join on: the schema diff builds these keys and live-cover
// matches them against the driver's steps. It is a constant because a
// prefix edited in one of those two files does not make the gate go
// quiet — live-cover then reports every resource as undriven AND as a
// step for something the binary does not publish, which reads as a
// broken driver rather than a broken gate.
const resourcePrefix = "resource "

// resourceKey is how a resource's URI or template appears in the
// surface map.
func resourceKey(uri string) string { return resourcePrefix + uri }

// dumpFromTag builds the binary as it was at tag, into a temp directory,
// and returns its dump.
func dumpFromTag(tag string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "schema-diff")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	worktree := dir + "/src"
	if out, err := exec.Command("git", "worktree", "add", "--detach", worktree, tag).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("worktree at %s: %w: %s", tag, err, out)
	}
	defer func() { _ = exec.Command("git", "worktree", "remove", "--force", worktree).Run() }()

	bin := dir + "/old-binary"
	build := exec.Command("go", "build", "-o", bin, "./cmd/google-calendar-mcp")
	build.Dir = worktree
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %w: %s", tag, err, out)
	}
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	return out, nil
}

// baselineFile is the recorded tool surface, used when no tag exists.
const baselineFile = "testdata/schema-baseline.json"

// baseline returns the surface to diff against and what to call it.
//
// The tag wins when there is one: it is the surface that actually
// shipped. Otherwise the committed snapshot stands in, and refreshing it
// is the deliberate act of saying the change has been looked at — which
// is the same thing tagging says, at a smaller scale.
func baseline(current map[string]string) (map[string]string, string, map[string]toolFields, error) {
	data, against := []byte(nil), baselineFile
	if tag, err := lastTag(); err == nil && tag != "" {
		dump, derr := dumpFromTag(tag)
		if derr != nil {
			fmt.Printf("  could not read the surface at %s (%v); falling back to %s\n",
				tag, derr, baselineFile)
		} else {
			data, against = dump, tag
		}
	}
	if data == nil {
		var err error
		data, err = os.ReadFile(baselineFile)
		if os.IsNotExist(err) {
			return nil, "", nil, nil
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("read %s: %w", baselineFile, err)
		}
	}
	previous, err := parseDump(data)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse %s: %w", against, err)
	}
	fields, err := fieldsOf(data)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse %s: %w", against, err)
	}
	return previous, against, fields, nil
}

// writeBaseline records the binary's current surface as the baseline.
func writeBaseline(bin string) error {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	if _, err := parseDump(out); err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	if err := os.WriteFile(baselineFile, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("  recorded %s\n", baselineFile)
	return nil
}
