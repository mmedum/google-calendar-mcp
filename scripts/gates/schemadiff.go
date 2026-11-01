package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// schemaDiff compares the binary's tool surface with the baseline, the
// surface of the CHANGELOG's newest release, and fails on a change that
// breaks a caller, at any depth: a tool or resource removed; a field
// removed; an input that takes fewer types or loses a listed value; an
// output that may return another type or may be missing where it was
// required; or an input newly required. Anything else that changed is
// reported for a person to look at, and an output that may carry a value
// it did not list is named.
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
	if len(cur.items) < 5 {
		return fmt.Errorf("the binary published %d tools and resources; that is not the surface", len(cur.items))
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
		fmt.Printf("  no baseline yet; %d tools and resources in the current surface\n", len(cur.items))
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", baselineFile, err)
	}
	base, err := readSurface(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", baselineFile, err)
	}

	changes := compareSurfaces(base, cur)
	changes.write(os.Stdout, baselineFile)
	breaking := changes.breaking
	var problems []string
	if want != "" && base.version != want {
		problems = append(problems, fmt.Sprintf("the baseline is the %q surface, but %s's newest release is %s; "+
			"record it in that release's commit with `make schema-baseline VERSION=%s`",
			base.version, changelogPath, want, want))
	}
	if undeclared := undeclaredBreaks(breaking, changelog); len(undeclared) > 0 {
		problems = append(problems, fmt.Sprintf("the tool surface breaks a caller of %s: %d change(s) above that "+
			"[Unreleased] does not declare. A release that makes one names its tool, in backticks, in a "+
			"**Breaking:** entry under [Unreleased], and is a new major version (`make schema-baseline` refuses "+
			"it otherwise): %s", base.version, len(undeclared), strings.Join(undeclared, "; ")))
	} else if len(breaking) > 0 {
		fmt.Printf("  %d breaking change(s), each declared under **Breaking:** in [Unreleased]\n", len(breaking))
	}
	if len(problems) == 0 && want != "" && sectionFor(changelog, "Unreleased") == "" && !changes.empty() {
		problems = append(problems, fmt.Sprintf("nothing is under [Unreleased], so this build is %s, "+
			"and its surface differs from the baseline. In %s's release commit, run "+
			"`make schema-baseline VERSION=%s`; otherwise, say what changed under [Unreleased]", want, want, want))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// undeclaredBreaks is each break whose item no **Breaking:** entry
// under [Unreleased] names in backticks.
func undeclaredBreaks(breaking []change, changelog string) []string {
	named := map[string]bool{}
	for _, line := range strings.Split(sectionFor(changelog, "Unreleased"), "\n") {
		if !strings.Contains(line, "**Breaking:**") {
			continue
		}
		for _, span := range codeSpans(line) {
			named[span] = true
		}
	}
	var out []string
	for _, b := range breaking {
		if !named[b.item] {
			out = append(out, b.text)
		}
	}
	return out
}

// codeSpanRe is one code span in a CHANGELOG line or a footer.
var codeSpanRe = regexp.MustCompile("`([^`]+)`")

// codeSpans is the text of each code span in s.
func codeSpans(s string) []string {
	var out []string
	for _, m := range codeSpanRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// surface is one schema dump, read for the diff.
type surface struct {
	version string
	fields  map[string]toolFields
	// items is every item of every list in the dump, keyed by itemKey,
	// and rest is every other key; neither holds the build stamp. A
	// change anywhere in either is a change to what a client sees.
	items map[string]map[string]any
	rest  map[string]any
}

// stampKeys are the dump's build stamp. They change with every build and
// are not part of the surface; what an SDK bump changes on the wire shows
// up inside the tools.
var stampKeys = []string{"server", "version", "sdk_version"}

func readSurface(data []byte) (surface, error) {
	var head struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return surface{}, err
	}
	items, rest, err := dumpItems(data)
	if err != nil {
		return surface{}, err
	}
	fields, err := fieldsOf(data)
	if err != nil {
		return surface{}, err
	}
	return surface{version: head.Version, fields: fields, items: items, rest: rest}, nil
}

// A change is one difference between two dumps: the item it is to, which
// is what a footer or a **Breaking:** entry names, and how the report
// shows it. The item is a tool's name, a resource's URI, a list item's
// name, or a top-level key such as instructions. field is, on the break
// of a tool still there, the field it breaks, such as "input calendar".
type change struct {
	item, text, field string
}

// surfaceChanges is what differs between two surfaces. breaking is what
// breaks a caller; changed is every other difference, an annotation or a
// _meta mark included, named by the part that moved. notes are outputs
// that may carry a new listed value, which a caller may want to know.
type surfaceChanges struct {
	added, changed, breaking []change
	notes                    []string
}

func (c surfaceChanges) empty() bool {
	return len(c.added) == 0 && len(c.changed) == 0 && len(c.breaking) == 0
}

// compareSurfaces is what changed from base to cur. An item gone breaks
// a caller, and so does a tool still there that broke at any depth
// (brokenFields). A tool that broke lists its other changes, such as a
// flipped hint, but not its schemas again, which the break already names.
func compareSurfaces(base, cur surface) surfaceChanges {
	var c surfaceChanges
	var removed []change
	fields := brokenFields(base.fields, cur.fields)
	broken := map[string]bool{}
	for _, b := range fields {
		broken[b.item] = true
	}
	for _, key := range sortedUnion(base.items, cur.items) {
		was, inBase := base.items[key]
		now, inCur := cur.items[key]
		name := itemName(key)
		switch {
		case !inCur:
			removed = append(removed, change{item: name, text: key + " removed"})
		case !inBase:
			c.added = append(c.added, change{item: name, text: key})
		default:
			parts := changedParts(was, now)
			if broken[key] {
				parts = slices.DeleteFunc(parts, func(p string) bool {
					return strings.HasPrefix(p, "input_schema") || strings.HasPrefix(p, "output_schema")
				})
			}
			if len(parts) > 0 {
				c.changed = append(c.changed, change{item: name, text: key + " (" + strings.Join(parts, ", ") + ")"})
			}
		}
	}
	for _, part := range changedParts(base.rest, cur.rest) {
		top, _, _ := strings.Cut(part, ".")
		c.changed = append(c.changed, change{item: top, text: part})
	}
	c.breaking = slices.Concat(removed, fields)
	c.notes = valueNotes(base.fields, cur.fields)
	return c
}

// write prints the changes to w, under a header naming what they are
// against.
func (c surfaceChanges) write(w io.Writer, against string) {
	_, _ = fmt.Fprintf(w, "  against %s: %d added, %d changed, %d breaking\n", against, len(c.added), len(c.changed), len(c.breaking))
	for _, n := range c.added {
		_, _ = fmt.Fprintf(w, "    + %s\n", n.text)
	}
	for _, n := range c.changed {
		_, _ = fmt.Fprintf(w, "    ~ %s\n", n.text)
	}
	for _, n := range c.notes {
		_, _ = fmt.Fprintf(w, "    ~ %s\n", n)
	}
	for _, b := range c.breaking {
		_, _ = fmt.Fprintf(w, "    ! %s  BREAKING\n", b.text)
	}
}

// changedParts names what differs between two dumped objects, one level
// deep, so a flipped hint reads as annotations.destructiveHint.
func changedParts(o, n map[string]any) []string {
	var parts []string
	for _, k := range sortedUnion(o, n) {
		if reflect.DeepEqual(o[k], n[k]) {
			continue
		}
		om, oMap := o[k].(map[string]any)
		nm, nMap := n[k].(map[string]any)
		if !oMap || !nMap {
			parts = append(parts, k)
			continue
		}
		for _, sub := range sortedUnion(om, nm) {
			if !reflect.DeepEqual(om[sub], nm[sub]) {
				parts = append(parts, k+"."+sub)
			}
		}
	}
	return parts
}

// sortedUnion is every key of a and b, sorted.
func sortedUnion[V any](a, b map[string]V) []string {
	keys := sortedKeys(a)
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// toolFields is what a caller relies on in each tool, at any depth: the
// fields it may send, and the ones it reads back.
//
// A path names a field the way a caller reaches it: `start.date`, and
// `events[].start` for a field of each element of a list.
type toolFields struct {
	inputs, outputs fieldSet
}

// fieldSet is one side of a tool: every field by its path, and which of
// them are required.
type fieldSet struct {
	fields   map[string]field
	required map[string]bool
}

// field is one field's type as the schema spells it, "" for any type,
// and the values it is limited to, nil when it is not.
type field struct {
	typ  string
	enum []string
}

func newFieldSet() fieldSet {
	return fieldSet{fields: map[string]field{}, required: map[string]bool{}}
}

// schemaNode is the part of a JSON Schema the diff walks. The dump
// carries no $ref, anyOf or oneOf, so properties and items reach every
// field.
type schemaNode struct {
	Type       json.RawMessage        `json:"type"`
	Enum       []json.RawMessage      `json:"enum"`
	Properties map[string]*schemaNode `json:"properties"`
	Items      *schemaNode            `json:"items"`
	Required   []string               `json:"required"`
}

// UnmarshalJSON takes a boolean schema as well. true allows any value,
// which a node with no type already means; false allows none.
func (n *schemaNode) UnmarshalJSON(b []byte) error {
	switch string(bytes.TrimSpace(b)) {
	case "true":
		*n = schemaNode{}
		return nil
	case "false":
		*n = schemaNode{Type: json.RawMessage(`[]`)}
		return nil
	}
	type plain schemaNode
	return json.Unmarshal(b, (*plain)(n))
}

// walk records every field under n, and which are required.
func (n *schemaNode) walk(prefix string, into fieldSet) {
	if n == nil {
		return
	}
	for _, r := range n.Required {
		into.required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		into.fields[path] = child.field()
		child.walk(path, into)
	}
	if n.Items != nil {
		into.fields[prefix+"[]"] = n.Items.field()
		n.Items.walk(prefix+"[]", into)
	}
}

// field is what the schema says of the field n describes.
func (n *schemaNode) field() field {
	f := field{typ: compactJSON(n.Type)}
	for _, v := range n.Enum {
		f.enum = append(f.enum, compactJSON(v))
	}
	return f
}

// compactJSON is a schema value as the schema spells it, so `"string"`
// and `["null","string"]` differ.
func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
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
		f := toolFields{inputs: newFieldSet(), outputs: newFieldSet()}
		t.InputSchema.walk("", f.inputs)
		t.OutputSchema.walk("", f.outputs)
		out[t.Name] = f
	}
	return out, nil
}

// brokenFields lists, as changes to each tool, what a tool kept by name
// lost: an input or output field removed, an input that takes fewer
// types or values, an output that may return more types or may now be
// missing, or an input newly required. A field inside one that was removed is not listed again. A
// removed tool is the caller's to report.
func brokenFields(prev, cur map[string]toolFields) []change {
	var out []change
	broke := func(name, side, field, format string, args ...any) {
		out = append(out, change{name, name + ": " + fmt.Sprintf(format, args...), side + " " + field})
	}
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			input    bool
			was, now fieldSet
		}{{"input", true, was.inputs, now.inputs}, {"output", false, was.outputs, now.outputs}} {
			for _, f := range sortedKeys(side.was.fields) {
				w := side.was.fields[f]
				n, still := side.now.fields[f]
				if !still {
					if _, parentKept := side.now.fields[parentPath(f)]; parentPath(f) == "" || parentKept {
						broke(name, side.what, f, "%s field %s removed", side.what, f)
					}
					continue
				}
				if typeBreaks(w.typ, n.typ, side.input) {
					broke(name, side.what, f, "%s field %s changed type from %s to %s", side.what, f, typeWord(w.typ), typeWord(n.typ))
				}
				// A caller sent this value, and it is refused now.
				if side.input && n.enum != nil {
					for _, v := range w.enum {
						if !slices.Contains(n.enum, v) {
							broke(name, "input", f, "input field %s no longer takes %s", f, v)
						}
					}
				}
				// A caller read this field as always there.
				if !side.input && side.was.required[f] && !side.now.required[f] {
					broke(name, "output", f, "output field %s no longer required", f)
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range sortedKeys(now.inputs.required) {
			parent := parentPath(f)
			_, parentWas := was.inputs.fields[parent]
			if !was.inputs.required[f] && (parent == "" || parentWas) {
				broke(name, "input", f, "input field %s newly required", f)
			}
		}
	}
	return out
}

// valueNotes lists what a person should look at in the values a kept
// field lists, where a caller may or may not be broken: an output that
// may carry a value it did not, which a caller may not handle, and an
// input newly limited to a list, which breaks a caller only if the
// server took other values before.
func valueNotes(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, f := range sortedKeys(was.outputs.fields) {
			w := was.outputs.fields[f]
			n, still := now.outputs.fields[f]
			switch {
			case !still || w.enum == nil:
			case n.enum == nil:
				out = append(out, fmt.Sprintf("%s: output field %s may now be any value", name, f))
			default:
				for _, v := range n.enum {
					if !slices.Contains(w.enum, v) {
						out = append(out, fmt.Sprintf("%s: output field %s may now be %s", name, f, v))
					}
				}
			}
		}
		for _, f := range sortedKeys(was.inputs.fields) {
			if n, still := now.inputs.fields[f]; still && was.inputs.fields[f].enum == nil && n.enum != nil {
				out = append(out, fmt.Sprintf("%s: input field %s now takes only %s", name, f, strings.Join(n.enum, ", ")))
			}
		}
	}
	return out
}

// typeBreaks reports whether a field's type change breaks a caller. An
// input may take more types than it did, and an output may return fewer;
// the other way round, a caller that sent or read the old type is
// broken. So `"string"` to `["null","string"]` breaks an output, where a
// caller read the field as always there, and not an input.
func typeBreaks(was, now string, input bool) bool {
	if was == now {
		return false
	}
	wide, narrow := now, was
	if !input {
		wide, narrow = was, now
	}
	return !typesCover(wide, narrow)
}

// typesCover reports whether every type narrow allows, wide allows too.
// No type at all is any type, and an integer is a number.
func typesCover(wide, narrow string) bool {
	if wide == "" {
		return true
	}
	if narrow == "" {
		return false
	}
	allowed := map[string]bool{}
	for _, t := range typeList(wide) {
		allowed[t] = true
	}
	if allowed["number"] {
		allowed["integer"] = true
	}
	for _, t := range typeList(narrow) {
		if !allowed[t] {
			return false
		}
	}
	return true
}

// typeList is a schema's type as a list, whether it was written as one
// name or several.
func typeList(typ string) []string {
	var one string
	if json.Unmarshal([]byte(typ), &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal([]byte(typ), &many)
	return many
}

// typeWord is a type for a message: as the schema spells it, or any.
func typeWord(typ string) string {
	if typ == "" {
		return "any"
	}
	return typ
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

func dumpFrom(bin string) (map[string]map[string]any, error) {
	out, err := dumpBytes(bin)
	if err != nil {
		return nil, err
	}
	return parseDump(out)
}

func parseDump(data []byte) (map[string]map[string]any, error) {
	items, _, err := dumpItems(data)
	return items, err
}

// dumpItems reads every item of every list in a dump, keyed by itemKey,
// and every other key but the build stamp. Resources are part of the
// surface: a resource whose URI changed breaks a client exactly as a
// renamed tool does. A list with an item that has no identity is
// compared as one value, since a position is no identity.
func dumpItems(data []byte) (map[string]map[string]any, map[string]any, error) {
	var whole map[string]any
	if err := json.Unmarshal(data, &whole); err != nil {
		return nil, nil, err
	}
	for _, k := range stampKeys {
		delete(whole, k)
	}
	items := map[string]map[string]any{}
	rest := map[string]any{}
	for key, v := range whole {
		list, isList := v.([]any)
		keyed := map[string]map[string]any{}
		for _, item := range list {
			obj, _ := item.(map[string]any)
			id := itemKey(key, obj)
			if id == "" {
				keyed = nil
				break
			}
			if _, twice := keyed[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			keyed[id] = obj
		}
		if !isList || keyed == nil {
			rest[key] = v
			continue
		}
		for id, obj := range keyed {
			if _, twice := items[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			items[id] = obj
		}
	}
	return items, rest, nil
}

// itemKey is how an item of a dumped list is named in the diff: a tool
// by its name, a resource by the URI or template a client asks for, and
// an item of any other list by the list and its name. Empty when the
// item has none.
func itemKey(list string, item map[string]any) string {
	id := ""
	for _, k := range []string{"uri", "uri_template", "name"} {
		if v, ok := item[k].(string); ok && v != "" {
			id = v
			break
		}
	}
	switch {
	case id == "":
		return ""
	case list == "tools":
		return id
	case list == "resources":
		return resourceKey(id)
	}
	return list + " " + id
}

// itemName is what a footer or a **Breaking:** entry names an item by,
// from its key in the surface: a tool's name, a resource's URI without
// its prefix, an item of another list without its list's name.
func itemName(key string) string {
	if uri, ok := strings.CutPrefix(key, resourcePrefix); ok {
		return uri
	}
	if _, name, ok := strings.Cut(key, " "); ok {
		return name
	}
	return key
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
		c := compareSurfaces(base, cur)
		c.write(os.Stdout, baselineFile)
		if breaking := c.breaking; len(breaking) > 0 && !newMajor(base.version, want) {
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
	fmt.Printf("  %s is now the %s surface: %d tools and resources\n", baselineFile, want, len(cur.items))
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
