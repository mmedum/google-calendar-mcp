package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// fieldResources are the resources §8b holds a verdict for. They are the
// four whose fields decide what this server can do: an event, the two
// views of a calendar, and a sharing rule.
//
// The list lives here and in nothing else. api-diff records exactly
// these from the discovery document, so a resource cannot be dropped
// from the record by editing the snapshot.
var fieldResources = []string{"Event", "Calendar", "CalendarListEntry", "AclRule"}

// apiFieldsGate is §8b: one verdict per published field.
//
// Methods are the coarse axis and api-coverage holds them. This holds
// the fine one, because this API keeps its capability in the Event
// resource: 44 properties, of which a server can implement twelve and
// still say "we support events". attachments, extendedProperties, gadget
// and the four event-type property blocks are what that sentence hides.
//
// Three directions, like the method gate:
//
//  1. a published field with no verdict fails;
//  2. a verdict for a field the API no longer publishes fails;
//  3. a verdict of `modeled` whose Go field does not exist fails, and a
//     Go field with no verdict fails — so the record cannot drift from
//     internal/gcal in either direction.
func apiFieldsGate() error {
	surface, err := loadSurface()
	if err != nil {
		return err
	}
	if len(surface.Schemas) < len(fieldResources) {
		return fmt.Errorf("the API snapshot carries %d resources, want %d (run `make api-diff`)",
			len(surface.Schemas), len(fieldResources))
	}

	published := map[string]bool{}
	for _, s := range surface.Schemas {
		if len(s.Fields) == 0 {
			return fmt.Errorf("the snapshot lists no fields for %s", s.Resource)
		}
		for _, f := range s.Fields {
			published[s.Resource+"."+f] = true
		}
	}
	// Floor: the Event resource alone publishes dozens, so a handful of
	// fields means the snapshot is not the published surface.
	if len(published) < 60 {
		return fmt.Errorf("the snapshot lists %d fields across %d resources; that is not the published surface",
			len(published), len(surface.Schemas))
	}

	verdicts, err := loadFieldVerdicts()
	if err != nil {
		return err
	}
	modeled, err := modeledFields()
	if err != nil {
		return err
	}

	var missing, stale, bad []string
	for name := range published {
		if _, ok := verdicts[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name, v := range verdicts {
		if !published[name] {
			stale = append(stale, fmt.Sprintf("%s (line %d)", name, v.line))
			continue
		}
		switch v.verdict {
		case "modeled":
			if !modeled[name] {
				bad = append(bad, fmt.Sprintf("%s (line %d): recorded as modeled, but internal/gcal has no field "+
					"with that json tag", name, v.line))
			}
		case "out":
			if strings.TrimSpace(v.reason) == "" {
				bad = append(bad, fmt.Sprintf("%s (line %d): written off with no reason", name, v.line))
			}
			if modeled[name] {
				bad = append(bad, fmt.Sprintf("%s (line %d): written off, but internal/gcal models it — "+
					"the record says this server ignores a field it reads", name, v.line))
			}
		default:
			bad = append(bad, fmt.Sprintf("%s (line %d): verdict %q is neither modeled nor out", name, v.line, v.verdict))
		}
	}
	// And the direction that catches a field added to the wire types
	// without a decision behind it.
	for name := range modeled {
		if _, ok := verdicts[name]; !ok {
			bad = append(bad, fmt.Sprintf("%s: internal/gcal models it and testdata/api-fields.tsv has no row", name))
		}
	}

	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(bad)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "published fields with no verdict in testdata/api-fields.tsv:\n  "+
			strings.Join(missing, "\n  ")+
			"\n  (Google publishes a field and nobody decided about it. Add a row saying modeled or out, with why.)")
	}
	if len(stale) > 0 {
		problems = append(problems, "verdicts for fields the API no longer publishes:\n  "+strings.Join(stale, "\n  "))
	}
	if len(bad) > 0 {
		problems = append(problems, "verdicts that do not match internal/gcal:\n  "+strings.Join(bad, "\n  "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n\n"))
	}

	in, out := 0, 0
	for _, v := range verdicts {
		if v.verdict == "modeled" {
			in++
		} else {
			out++
		}
	}
	fmt.Printf("  %d fields across %d resources: %d modeled, %d written off\n",
		len(published), len(surface.Schemas), in, out)
	return nil
}

type fieldVerdict struct {
	verdict, reason string
	line            int
}

func loadFieldVerdicts() (map[string]fieldVerdict, error) {
	rows, err := loadTSV("testdata/api-fields.tsv", 4)
	if err != nil {
		return nil, err
	}
	out := map[string]fieldVerdict{}
	for _, r := range rows {
		key := r.fields[0] + "." + r.fields[1]
		if prev, dup := out[key]; dup {
			return nil, fmt.Errorf("%s has two verdicts, on lines %d and %d", key, prev.line, r.line)
		}
		out[key] = fieldVerdict{verdict: r.fields[2], reason: r.fields[3], line: r.line}
	}
	return out, nil
}

// modeledFields reads internal/gcal and returns the json tags of the
// fields each of the four resources actually carries.
//
// It reads the source rather than a hand-kept list, which is the whole
// point: a field added to the struct and forgotten in the record is
// exactly the drift this gate exists to catch.
func modeledFields() (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("internal", "gcal", "gcal.go"), nil, 0)
	if err != nil {
		return nil, err
	}

	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		// The wire types are named after the resources they carry, so
		// the type name IS the resource name.
		resource := ts.Name.Name
		if !slices.Contains(fieldResources, resource) {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			if f.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}
			name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			out[resource+"."+name] = true
		}
		return true
	})
	if len(out) < 20 {
		return nil, fmt.Errorf("found only %d modeled fields in internal/gcal; the parser is not reading the types", len(out))
	}
	return out, nil
}
