package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// classGate holds the error vocabulary closed FROM BOTH SIDES.
//
// One direction is the obvious one: every class the code emits must be
// declared. The other is the one that catches drift — every declared
// class must be emitted somewhere, or the documentation promises a
// vocabulary the server cannot produce.
//
// A sibling documented ten classes while its code emitted fifteen, four
// of them named nowhere. That is what checking one direction buys you.
func classGate() error {
	declared, err := declaredClasses()
	if err != nil {
		return err
	}
	emitted, files, err := emittedClasses()
	if err != nil {
		return err
	}

	// Floors, so "found nothing" cannot pass for "looked at nothing".
	if len(declared) < 6 {
		return fmt.Errorf("only %d classes declared; the standard's baseline alone is six", len(declared))
	}
	if files < 5 {
		return fmt.Errorf("scanned only %d files for class uses; that is not the whole module", files)
	}

	planned, err := plannedClasses()
	if err != nil {
		return err
	}

	var undeclared, unemitted, stalePlan []string
	for c := range emitted {
		if !declared[c] {
			undeclared = append(undeclared, c)
		}
		if _, isPlanned := planned[c]; isPlanned {
			// It is emitted now, so the promise has been kept and the
			// entry must go — otherwise Planned only ever grows.
			stalePlan = append(stalePlan, c)
		}
	}
	for c := range declared {
		if emitted[c] {
			continue
		}
		if _, isPlanned := planned[c]; isPlanned {
			continue
		}
		unemitted = append(unemitted, c)
	}
	sort.Strings(stalePlan)
	sort.Strings(undeclared)
	sort.Strings(unemitted)

	var problems []string
	if len(undeclared) > 0 {
		problems = append(problems, "emitted but not in gapi.Classes: "+strings.Join(undeclared, ", "))
	}
	if len(unemitted) > 0 {
		problems = append(problems, "declared in gapi.Classes but never emitted: "+strings.Join(unemitted, ", ")+
			"\n  (a class nothing produces is a promise the server cannot keep; emit it, remove it, "+
			"or name the phase that will emit it in gapi.Planned)")
	}
	if len(stalePlan) > 0 {
		problems = append(problems, "gapi.Planned still lists classes the code now emits: "+strings.Join(stalePlan, ", ")+
			"\n  (remove the entry; otherwise Planned only ever grows and stops meaning anything)")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %d classes in %d files: %d emitted, %d planned for a later phase\n",
		len(declared), files, len(emitted), len(planned))
	return nil
}

// plannedClasses reads gapi.Planned: classes declared now and emitted by
// a phase that has not been built yet.
//
// It is a map to a REASON rather than a set, so an entry has to say
// which phase owns it. A bare allow-list here would be indistinguishable
// from switching the second direction off.
func plannedClasses() (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("internal", "gapi", "errors.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	constOf, err := classConstNames()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "Planned" {
			return true
		}
		for _, v := range vs.Values {
			cl, ok := v.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				id, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				val, found := constOf[id.Name]
				if !found {
					continue
				}
				if lit, ok := kv.Value.(*ast.BasicLit); ok {
					out[val] = strings.Trim(lit.Value, `"`)
				}
			}
		}
		return false
	})
	return out, nil
}

// declaredClasses reads the Classes slice literal in the source, rather
// than importing the package and reading the variable.
//
// Deriving it from the code is the point: typing the list out here would
// make this gate a second copy of the thing it is checking.
func declaredClasses() (map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("internal", "gapi", "errors.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	constOf := map[string]string{}
	out := map[string]bool{}

	// First the const block: ClassInvalid = "invalid", ...
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				constOf[name.Name] = strings.Trim(lit.Value, `"`)
			}
		}
		return true
	})

	// Then the Classes slice, resolved through those constants.
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "Classes" {
			return true
		}
		for _, v := range vs.Values {
			cl, ok := v.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, el := range cl.Elts {
				if id, ok := el.(*ast.Ident); ok {
					if val, found := constOf[id.Name]; found {
						out[val] = true
					}
				}
			}
		}
		return false
	})
	return out, nil
}

// emittedClasses finds every Class constant actually referenced outside
// its own declaration.
func emittedClasses() (map[string]bool, int, error) {
	constOf, err := classConstNames()
	if err != nil {
		return nil, 0, err
	}
	out := map[string]bool{}
	files := 0

	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "dist", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file that will not parse is not this gate's problem:
			// `go vet` and the compiler both run before it. Skipping
			// keeps the gate from failing twice for one cause. The floor
			// on `files` below is what stops a parse failure quietly
			// shrinking the scan.
			return nil //nolint:nilerr // the build catches unparseable files
		}
		files++
		// Only FUNCTION BODIES count as emitting a class. A name that
		// appears solely in the const block or in the Classes slice is
		// being declared, not produced, and counting those would make
		// the second direction vacuous.
		//
		// The first version of this gate excluded errors.go wholesale
		// for that reason — and errors.go is where classify() actually
		// emits most of the vocabulary, so it reported six classes as
		// dead that the server emits on every 403. Excluding a file is
		// the wrong axis; excluding declarations is the right one.
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch e := n.(type) {
				case *ast.SelectorExpr:
					if val, ok := constOf[e.Sel.Name]; ok {
						out[val] = true
					}
				case *ast.Ident:
					if val, ok := constOf[e.Name]; ok {
						out[val] = true
					}
				}
				return true
			})
		}
		return nil
	})
	return out, files, err
}

func classConstNames() (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("internal", "gapi", "errors.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) || !strings.HasPrefix(name.Name, "Class") {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out[name.Name] = strings.Trim(lit.Value, `"`)
			}
		}
		return true
	})
	return out, nil
}
