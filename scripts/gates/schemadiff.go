package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// schemaDiff compares the binary's tool surface with the baseline, the
// surface of the CHANGELOG's newest release, and fails on a change that
// breaks a caller: a tool or resource removed, an input or output field
// removed or retyped at any depth, or an input newly required. Anything
// else that changed is reported for a person to look at.
//
// It also fails when the baseline is not the newest release's: an older
// one protects an older surface, so whatever shipped since could be
// dropped and nothing would say. With nothing under [Unreleased] the
// build is that release, so its surface must be the baseline's exactly,
// which proves a release commit recorded the baseline rather than
// relabeling it.
//
// The baseline is a committed file, never a tag: a shallow checkout has
// no tags, so a tag-based diff compares against whatever it falls back
// to, and CI is a shallow checkout.
func schemaDiff(bin string) error {
	out, err := dumpBytes(bin)
	if err != nil {
		return err
	}
	cur, err := readSurface(out)
	if err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	if len(cur.entries) < 5 {
		return fmt.Errorf("the binary published %d tools and resources; that is not the surface", len(cur.entries))
	}
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	changelog := string(raw)
	want := baselineVersion(changelog)

	data, err := os.ReadFile(baselineFile)
	if os.IsNotExist(err) {
		if want != "" {
			return fmt.Errorf("%s names %s as released, but %s is missing; "+
				"record it in that release's commit with `make schema-baseline VERSION=%s`",
				changelogPath, want, baselineFile, want)
		}
		// The first release is exactly this state, and it is not a
		// failure: there is nothing yet to be compatible with.
		fmt.Printf("  no baseline yet; %d tools and resources in the current surface\n", len(cur.entries))
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", baselineFile, err)
	}
	base, err := readSurface(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", baselineFile, err)
	}

	breaking := compareSurfaces(base, cur)
	var problems []string
	if want != "" && base.version != want {
		problems = append(problems, fmt.Sprintf("the baseline is the %q surface, but %s's newest release is %s; "+
			"record it in that release's commit with `make schema-baseline VERSION=%s`",
			base.version, changelogPath, want, want))
	}
	if len(breaking) > 0 {
		problems = append(problems, fmt.Sprintf("the tool surface breaks a caller of %s: %d change(s) above",
			base.version, len(breaking)))
	}
	if len(problems) == 0 && want != "" && sectionFor(changelog, "Unreleased") == "" && !sameSurface(data, out) {
		problems = append(problems, fmt.Sprintf("nothing is under [Unreleased], so this build is %s, "+
			"and its surface differs from the baseline. In %s's release commit, run "+
			"`make schema-baseline VERSION=%s`; otherwise, say what changed under [Unreleased]", want, want, want))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// surface is one schema dump, read for the diff.
type surface struct {
	version string
	// entries is every tool and resource, keyed by name or by URI, with
	// what a person should look at when it changes.
	entries map[string]string
	fields  map[string]toolFields
}

func readSurface(data []byte) (surface, error) {
	var head struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return surface{}, err
	}
	entries, err := parseDump(data)
	if err != nil {
		return surface{}, err
	}
	fields, err := fieldsOf(data)
	if err != nil {
		return surface{}, err
	}
	return surface{version: head.Version, entries: entries, fields: fields}, nil
}

// compareSurfaces prints what changed from base to cur and returns what
// breaks a caller.
func compareSurfaces(base, cur surface) []string {
	var removed, changed, added []string
	for name, prev := range base.entries {
		now, still := cur.entries[name]
		switch {
		case !still:
			removed = append(removed, name)
		case prev != now:
			changed = append(changed, name)
		}
	}
	for name := range cur.entries {
		if _, had := base.entries[name]; !had {
			added = append(added, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	sort.Strings(added)

	fmt.Printf("  against %s: %d added, %d changed, %d removed\n", baselineFile, len(added), len(changed), len(removed))
	for _, n := range added {
		fmt.Printf("    + %s\n", n)
	}
	for _, n := range changed {
		what := "description or schema changed"
		if strings.HasPrefix(n, resourcePrefix) {
			what = "description or type changed"
		}
		fmt.Printf("    ~ %s (%s)\n", n, what)
	}
	for _, n := range removed {
		fmt.Printf("    - %s  BREAKING\n", n)
	}
	fields := brokenFields(base.fields, cur.fields)
	for _, b := range fields {
		fmt.Printf("    ! %s  BREAKING\n", b)
	}
	return append(removed, fields...)
}

// sameSurface reports whether two dumps publish the same tools and
// resources, field for field. The version and SDK stamps are not part
// of it, and neither is key order.
func sameSurface(a, b []byte) bool {
	type published struct {
		Tools     any `json:"tools"`
		Resources any `json:"resources"`
	}
	var x, y published
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// toolFields is what a caller relies on in each tool, at any depth: the
// fields it may send and their types, the ones it must send, and the
// ones it reads back and their types.
//
// A path names a field the way a caller reaches it: `start.date`, and
// `events[].start` for a field of each element of a list.
type toolFields struct {
	inputs, outputs map[string]string
	required        map[string]bool
}

// schemaNode is the part of a JSON Schema the diff walks. The dump
// carries no $ref, anyOf or oneOf, so properties and items reach every
// field.
type schemaNode struct {
	Type       json.RawMessage        `json:"type"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
	Required   []string               `json:"required"`
}

// walk records the type of every field under n, and which are required.
func (n *schemaNode) walk(prefix string, types map[string]string, required map[string]bool) {
	if n == nil {
		return
	}
	for _, r := range n.Required {
		required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		types[path] = child.typeName()
		child.walk(path, types, required)
	}
	if n.Items != nil {
		types[prefix+"[]"] = n.Items.typeName()
		n.Items.walk(prefix+"[]", types, required)
	}
}

// typeName is the type as the schema spells it, so `"string"` and
// `["null","string"]` differ: a field that may now be null breaks a
// caller that read it as always there.
func (n *schemaNode) typeName() string {
	var buf bytes.Buffer
	if json.Compact(&buf, n.Type) != nil {
		return string(n.Type)
	}
	return buf.String()
}

func fieldPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// parentPath is the field a path sits in, or "" at the top.
func parentPath(path string) string {
	if strings.HasSuffix(path, "[]") {
		return strings.TrimSuffix(path, "[]")
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

// fieldsOf reads every tool's fields out of a dump.
func fieldsOf(data []byte) (map[string]toolFields, error) {
	var dump struct {
		Tools []struct {
			Name         string      `json:"name"`
			InputSchema  *schemaNode `json:"input_schema"`
			OutputSchema *schemaNode `json:"output_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, err
	}
	out := map[string]toolFields{}
	for _, t := range dump.Tools {
		f := toolFields{inputs: map[string]string{}, outputs: map[string]string{}, required: map[string]bool{}}
		t.InputSchema.walk("", f.inputs, f.required)
		t.OutputSchema.walk("", f.outputs, map[string]bool{})
		out[t.Name] = f
	}
	return out, nil
}

// brokenFields lists what a tool kept by name lost: an input or output
// field removed or retyped, or an input newly required. A field inside
// one that was removed is not listed again. A removed tool is the
// caller's to report.
func brokenFields(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			was, now map[string]string
		}{{"input", was.inputs, now.inputs}, {"output", was.outputs, now.outputs}} {
			for _, f := range sortedKeys(side.was) {
				t, still := side.now[f]
				_, parentKept := side.now[parentPath(f)]
				switch {
				case !still && (parentPath(f) == "" || parentKept):
					out = append(out, fmt.Sprintf("%s: %s field %s removed", name, side.what, f))
				case still && t != side.was[f]:
					out = append(out, fmt.Sprintf("%s: %s field %s changed type from %s to %s", name, side.what, f, side.was[f], t))
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range sortedKeys(now.required) {
			parent := parentPath(f)
			_, parentWas := was.inputs[parent]
			if !was.required[f] && (parent == "" || parentWas) {
				out = append(out, fmt.Sprintf("%s: input field %s newly required", name, f))
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

// dumpBytes runs the binary's schema dump with nowhere to find a
// setting, as the smoke test runs it, so a GCAL_ variable in the
// maintainer's shell cannot change the surface a gate compares or
// records.
func dumpBytes(bin string) ([]byte, error) {
	cmd := exec.Command(bin, "--dump-schemas")
	smokeEnv(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run %s --dump-schemas: %w", bin, err)
	}
	return out, nil
}

func dumpFrom(bin string) (map[string]string, error) {
	out, err := dumpBytes(bin)
	if err != nil {
		return nil, err
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

// baselineFile is the surface of the newest release, recorded in that
// release's commit.
const baselineFile = "testdata/schema-baseline.json"

// baselineVersion is the release whose surface the baseline must hold:
// the CHANGELOG's newest heading, or empty before the first release.
//
// Between releases that heading is the last tag. In a release commit it
// is the release being cut, and the baseline is refreshed in that same
// commit. Refreshing after the tag instead would fail every branch from
// the moment the tag is pushed until a second change lands.
func baselineVersion(changelog string) string {
	for _, line := range strings.Split(changelog, "\n") {
		rest, ok := strings.CutPrefix(line, "## [")
		if !ok {
			continue
		}
		v, _, ok := strings.Cut(rest, "]")
		if ok && v != "Unreleased" {
			return "v" + v
		}
	}
	return ""
}

// writeBaseline records the surface of the release being cut as the
// baseline. The release commit runs it, so the baseline lands with the
// CHANGELOG heading that names it.
//
// It compares the build with the current baseline first, and refuses a
// change that breaks a caller unless the release is a new major version,
// which is what a break has to ship as. Overwriting first would leave
// the diff comparing the release with itself.
func writeBaseline(bin string) error {
	out, err := dumpBytes(bin)
	if err != nil {
		return err
	}
	cur, err := readSurface(out)
	if err != nil {
		return fmt.Errorf("the binary did not produce a readable surface: %w", err)
	}
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	want := baselineVersion(string(raw))
	if want == "" {
		return fmt.Errorf("%s names no release yet, so there is no surface to record", changelogPath)
	}
	if cur.version != want {
		return fmt.Errorf("%s is stamped %q, but the release being cut is %s; build it with `make build VERSION=%s`",
			bin, cur.version, want, want)
	}
	data, err := os.ReadFile(baselineFile)
	switch {
	case err == nil:
		base, err := readSurface(data)
		if err != nil {
			return fmt.Errorf("parse %s: %w", baselineFile, err)
		}
		if breaking := compareSurfaces(base, cur); len(breaking) > 0 && !newMajor(base.version, want) {
			msg := fmt.Sprintf("%s breaks a caller of %s in %d way(s) above, and is not a new major version; %s is unchanged",
				want, base.version, len(breaking), baselineFile)
			if base.version == want {
				msg += fmt.Sprintf(". It already holds %s from an earlier run; restore the last release's "+
					"baseline from its tag, then run this again", want)
			}
			return errors.New(msg)
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("read %s: %w", baselineFile, err)
	}
	if err := writeThrough(baselineFile, out); err != nil {
		return fmt.Errorf("write %s: %w", baselineFile, err)
	}
	fmt.Printf("  %s is now the %s surface: %d tools and resources\n", baselineFile, want, len(cur.entries))
	return nil
}

// newMajor reports whether release to is a later major version than
// release from. An unreadable version is not.
func newMajor(from, to string) bool {
	major := func(v string) int {
		head, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), ".")
		n, err := strconv.Atoi(head)
		if err != nil {
			return -1
		}
		return n
	}
	f, t := major(from), major(to)
	return f >= 0 && t > f
}

// writeThrough writes path through a temporary file beside it and a
// rename, so a failed write leaves whatever was there whole rather than
// truncated or half-written.
func writeThrough(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // a committed file, read by everyone
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
