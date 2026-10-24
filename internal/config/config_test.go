package config_test

import (
	"bytes"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
)

func build(t *testing.T, env map[string]string, args ...string) (config.Config, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	s := config.Define(fs, func(k string) string { return env[k] })
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s.Build()
}

func TestDefaults(t *testing.T) {
	c, err := build(t, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "default" || c.LogLevel != config.LogInfo || c.LogFormat != config.LogText {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.ReadOnly || c.EnableDestructive {
		t.Fatal("read-only and destructive must both default off")
	}
	if c.EnableDeclineAll {
		t.Fatal("declining every meeting must default off; the operator turns it on")
	}
	if !c.Sharing {
		t.Fatal("sharing must default on; GCAL_SHARING=off is the opt-out")
	}
	if c.MaxEvents != config.DefaultMaxEvents || c.MaxCalendars != config.DefaultMaxCalendars {
		t.Fatalf("budget defaults wrong: %+v", c)
	}
}

func TestEnvThenFlag(t *testing.T) {
	env := map[string]string{"GCAL_PROFILE": "work", "GCAL_LOG_LEVEL": "debug"}
	c, err := build(t, env)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "work" || c.LogLevel != config.LogDebug {
		t.Fatalf("environment ignored: %+v", c)
	}
	// A flag given explicitly beats the environment.
	c, err = build(t, env, "-profile", "personal")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "personal" {
		t.Fatalf("flag did not override the environment: %q", c.Profile)
	}
}

func TestSharingTakesOnOffAndBooleans(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"on", true}, {"off", false}, {"true", true}, {"false", false},
		{"ON", true}, {"Off", false}, {"", true},
	} {
		cfg, err := build(t, map[string]string{"GCAL_SHARING": c.in})
		if err != nil {
			t.Fatalf("Build(%q): %v", c.in, err)
		}
		if cfg.Sharing != c.want {
			t.Fatalf("sharing %q = %v, want %v", c.in, cfg.Sharing, c.want)
		}
	}
	if _, err := build(t, map[string]string{"GCAL_SHARING": "maybe"}); err == nil {
		t.Fatal("sharing accepted a value that is neither on nor off")
	}
}

func TestInvalidValuesAreRejected(t *testing.T) {
	cases := []struct{ name, key, val string }{
		{"profile", "GCAL_PROFILE", "Not A Profile"},
		{"log level", "GCAL_LOG_LEVEL", "chatty"},
		{"log format", "GCAL_LOG_FORMAT", "yaml"},
		{"read-only", "GCAL_READ_ONLY", "perhaps"},
		{"the read-only switch's removed name", "GCAL_READONLY", "perhaps"},
		{"destructive", "GCAL_ENABLE_DESTRUCTIVE", "perhaps"},
		{"max events not a number", "GCAL_MAX_EVENTS", "lots"},
		{"max events too large", "GCAL_MAX_EVENTS", "999999"},
		{"max events zero", "GCAL_MAX_EVENTS", "0"},
		{"max calendars over the API ceiling", "GCAL_MAX_CALENDARS", "51"},
		{"concurrency zero", "GCAL_CONCURRENCY", "0"},
		{"timeout unparseable", "GCAL_HTTP_TIMEOUT", "soon"},
		{"timeout too long", "GCAL_HTTP_TIMEOUT", "1h"},
		{"timeout negative", "GCAL_WRITE_TIMEOUT", "-5s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := build(t, map[string]string{c.key: c.val}); err == nil {
				t.Fatalf("%s=%q was accepted", c.key, c.val)
			}
		})
	}
}

// TestMaxCalendarsCannotExceedTheAPICeiling is §2.10: freebusy.query
// takes at most 50 calendars, so a budget above that promises a fan-out
// the API will refuse.
func TestMaxCalendarsCannotExceedTheAPICeiling(t *testing.T) {
	if config.MaxMaxCalendars != 50 {
		t.Fatalf("MaxMaxCalendars = %d; the API ceiling is 50 (calendarExpansionMax)", config.MaxMaxCalendars)
	}
	if _, err := build(t, map[string]string{"GCAL_MAX_CALENDARS": "50"}); err != nil {
		t.Fatalf("50 must be allowed: %v", err)
	}
}

func TestErrorsAccumulate(t *testing.T) {
	_, err := build(t, map[string]string{
		"GCAL_PROFILE":   "Bad Profile",
		"GCAL_LOG_LEVEL": "chatty",
		"GCAL_SHARING":   "maybe",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"profile", "log level", "sharing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

func TestLogLevelSlog(t *testing.T) {
	for _, c := range []struct {
		in   config.LogLevel
		want slog.Level
	}{
		{config.LogDebug, slog.LevelDebug},
		{config.LogInfo, slog.LevelInfo},
		{config.LogWarn, slog.LevelWarn},
		{config.LogError, slog.LevelError},
		{config.LogLevel("nonsense"), slog.LevelInfo},
	} {
		if got := c.in.Slog(); got != c.want {
			t.Fatalf("%q.Slog() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNewLoggerWritesWhereItIsTold(t *testing.T) {
	for _, format := range []config.LogFormat{config.LogText, config.LogJSON} {
		var buf bytes.Buffer
		lg := config.NewLogger(config.Config{LogLevel: config.LogInfo, LogFormat: format}, &buf)
		lg.Info("hello")
		if buf.Len() == 0 {
			t.Fatalf("%s logger wrote nothing to the given writer", format)
		}
		if format == config.LogJSON && !strings.HasPrefix(buf.String(), "{") {
			t.Fatalf("json logger did not write JSON: %q", buf.String())
		}
	}
}

// TestEveryFlagNamesItsEnvVar keeps the usage strings usable as
// documentation: the staleness gate reads them to learn the settings.
func TestEveryFlagNamesItsEnvVar(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	config.Define(fs, func(string) string { return "" })
	n := 0
	fs.VisitAll(func(f *flag.Flag) {
		n++
		if !strings.Contains(f.Usage, "[env "+config.EnvPrefix) {
			t.Fatalf("flag -%s does not name its environment variable: %q", f.Name, f.Usage)
		}
	})
	if n < 12 {
		t.Fatalf("only %d flags defined; the gate needs a floor so an empty read is not mistaken for a pass", n)
	}
}

// GCAL_REQUIRE_PROMPT went with the server's question (§9a). Turned on,
// it meant writes nobody could be asked about to be refused, so a
// configuration that still turns it on is refused rather than quietly
// allowed; off or unset, it is ignored.
func TestTheRemovedRequirePromptIsRefused(t *testing.T) {
	for v, refused := range map[string]bool{
		"true": true, "1": true, "on": true, "always": true,
		"": false, "false": false, "0": false, "off": false,
	} {
		_, err := build(t, map[string]string{"GCAL_REQUIRE_PROMPT": v})
		if got := err != nil; got != refused {
			t.Errorf("GCAL_REQUIRE_PROMPT=%q: err = %v, want refused %v", v, err, refused)
			continue
		}
		if refused && (!errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "GCAL_REQUIRE_PROMPT was removed")) {
			t.Errorf("GCAL_REQUIRE_PROMPT=%q: the refusal does not say the setting was removed: %v", v, err)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(string) string { return "" })
	if err := fs.Parse([]string{"-require-prompt"}); err == nil {
		t.Error("the removed -require-prompt flag was accepted")
	}
}

// The read-only switch is GCAL_READ_ONLY alone. GCAL_READONLY, its name
// until 4.1.0, is a removed setting: off or unset it starts and does
// nothing, and the new name decides; on, or not a boolean, it is refused,
// beside the new name too, naming the new one and not echoing the value.
func TestTheReadOnlySwitchsOldNameIsRemoved(t *testing.T) {
	const refused = "config: invalid: GCAL_READONLY was removed: " +
		"it is GCAL_READ_ONLY (-read-only) now. Unset it, and set that in its place"
	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
		// want is the refusal, or empty for a run that starts.
		want string
		// on is ReadOnly in a run that starts: the new switch's.
		on bool
	}{
		{name: "neither", on: false},
		{name: "the new name on", env: map[string]string{"GCAL_READ_ONLY": "true"}, on: true},
		{name: "the flag", args: []string{"-read-only", "true"}, on: true},
		{name: "the old name off", env: map[string]string{"GCAL_READONLY": "false"}, on: false},
		{name: "the old name off in capitals and spaces", env: map[string]string{"GCAL_READONLY": " OFF "}, on: false},
		{name: "the old name 0", env: map[string]string{"GCAL_READONLY": "0"}, on: false},
		{name: "the old name empty", env: map[string]string{"GCAL_READONLY": ""}, on: false},
		{name: "the old name off leaves the new one on",
			env: map[string]string{"GCAL_READONLY": "false", "GCAL_READ_ONLY": "true"}, on: true},
		{name: "the old name on", env: map[string]string{"GCAL_READONLY": "true"}, want: refused},
		{name: "the old name 1", env: map[string]string{"GCAL_READONLY": "1"}, want: refused},
		{name: "the old name on beside the new one",
			env: map[string]string{"GCAL_READONLY": "on", "GCAL_READ_ONLY": "true"}, want: refused},
		{name: "the old name on beside the flag", env: map[string]string{"GCAL_READONLY": "yes"},
			args: []string{"-read-only", "true"}, want: refused},
		{name: "the old name not a boolean", env: map[string]string{"GCAL_READONLY": "maybe"}, want: refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := build(t, tc.env, tc.args...)
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Errorf("got %v\nwant %s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if c.ReadOnly != tc.on {
				t.Errorf("ReadOnly = %t, want the new switch's %t", c.ReadOnly, tc.on)
			}
		})
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(string) string { return "" })
	if fs.Lookup("readonly") != nil {
		t.Error("-readonly is a flag; GCAL_READONLY never had one")
	}
}

// Vars is what the bundle gate reads a setting's kind from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. A
// removed setting takes false and refuses true. An empty value
// configures what setting nothing does, which is what lets a bundle
// leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	vars := config.Vars()
	if len(vars) < 17 {
		t.Fatalf("Vars has %d variables; the server reads at least 17", len(vars))
	}
	unset, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	var switches, removed []string
	for _, v := range vars {
		accepts := func(value string) bool {
			_, err := build(t, map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Build reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if gone := accepts("false") && !accepts("true") && !accepts("maybe"); gone != v.Removed {
			t.Errorf("%s: Removed is %t, and Build refuses it on: %t", v.Name, v.Removed, gone)
		}
		if v.Switch {
			switches = append(switches, v.Name)
		}
		if v.Removed {
			removed = append(removed, v.Name)
		}
		c, err := build(t, map[string]string{v.Name: ""})
		if err != nil || !reflect.DeepEqual(c, unset) {
			t.Errorf("%s=\"\": %+v, %v; want the unconfigured configuration", v.Name, c, err)
		}
	}
	if want := []string{"GCAL_ENABLE_DECLINE_ALL", "GCAL_ENABLE_DESTRUCTIVE", "GCAL_READ_ONLY", "GCAL_SHARING"}; !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
	if want := []string{"GCAL_READONLY", "GCAL_REQUIRE_PROMPT"}; !slices.Equal(removed, want) {
		t.Errorf("Vars marks %v removed, want %v", removed, want)
	}
}

// EnvVars is every variable the server reads, the removed ones and the
// two read outside Define included.
func TestEnvVars(t *testing.T) {
	want := []string{
		"GCAL_CLIENT_SECRET", "GCAL_CONCURRENCY", "GCAL_CONFIG_DIR", "GCAL_ENABLE_DECLINE_ALL",
		"GCAL_ENABLE_DESTRUCTIVE", "GCAL_HTTP_TIMEOUT", "GCAL_LOG_FORMAT", "GCAL_LOG_LEVEL",
		"GCAL_MAX_CALENDARS", "GCAL_MAX_EVENTS", "GCAL_PROFILE", "GCAL_READONLY", "GCAL_READ_ONLY",
		"GCAL_REFRESH_TOKEN", "GCAL_REQUIRE_PROMPT", "GCAL_SHARING", "GCAL_WRITE_TIMEOUT",
	}
	if got := config.EnvVars(); !slices.Equal(got, want) {
		t.Errorf("EnvVars =\n%v\nwant\n%v", got, want)
	}
}

// definedFloor is how many variables Define reads today, on purpose: the
// floor moves with it, so a Define that reads nothing cannot pass.
const definedFloor = 15

// A setting given as spaces alone is unset, as an empty one is, so a
// form field cleared to a space starts the server with the default. A
// removed setting of spaces is off.
func TestASettingOfSpacesIsUnset(t *testing.T) {
	want, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	vars := config.Defined()
	if len(vars) < definedFloor {
		t.Fatalf("Define reads %d settings, want at least %d", len(vars), definedFloor)
	}
	for _, v := range vars {
		got, err := build(t, map[string]string{v.Name: "  "})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s=\"  \": %+v, %v; want the unconfigured configuration", v.Name, got, err)
		}
	}
}

// A profile name is held to ProfileRule, and the refusal says it. Only A
// to Z are lowered: a character Unicode folds into a to z, such as the
// Kelvin sign, is refused rather than renamed to another profile.
func TestProfileRule(t *testing.T) {
	if config.ProfileRule != "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit" {
		t.Errorf("ProfileRule = %q", config.ProfileRule)
	}
	for _, tc := range []struct{ name, want string }{
		{"Work", "work"}, {" work ", "work"}, {"a", "a"}, {"9-", "9-"}, {"a-b_c", "a-b_c"},
		{strings.Repeat("a", 64), strings.Repeat("a", 64)},
	} {
		if c, err := build(t, map[string]string{"GCAL_PROFILE": tc.name}); err != nil || c.Profile != tc.want {
			t.Errorf("GCAL_PROFILE=%q: profile %q, %v; want %q", tc.name, c.Profile, err, tc.want)
		}
	}
	for _, name := range []string{strings.Repeat("a", 65), "-a", "_a", "a.b", "a b", "caf\u00e9", "\u212aey"} {
		_, err := build(t, map[string]string{"GCAL_PROFILE": name})
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "must be "+config.ProfileRule) {
			t.Errorf("GCAL_PROFILE=%q: %v; want it refused, naming the rule", name, err)
		}
	}
}

// Load is Define, Parse and Build: a flag is read, and an unknown one
// refused.
func TestLoad(t *testing.T) {
	c, err := config.Load([]string{"-read-only", "true"}, func(string) string { return "" })
	if err != nil || !c.ReadOnly {
		t.Errorf("Load = %+v, %v; want read-only", c, err)
	}
	if _, err := config.Load([]string{"-unknown"}, func(string) string { return "" }); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// Define reads each variable once, and records each once for Vars. Two
// settings on one variable would leave it to whichever Build reads
// last.
func TestDefineReadsEachVariableOnce(t *testing.T) {
	reads := map[string]int{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(name string) string { reads[name]++; return "" })
	var listed []string
	for _, v := range config.Defined() {
		listed = append(listed, v.Name)
	}
	slices.Sort(listed)
	if len(listed) < definedFloor {
		t.Fatalf("Define records %d settings, want at least %d", len(listed), definedFloor)
	}
	if read := slices.Sorted(maps.Keys(reads)); !slices.Equal(listed, read) {
		t.Errorf("Define records %v\nDefine reads %v", listed, read)
	}
	for name, n := range reads {
		if n != 1 {
			t.Errorf("Define reads %s %d times", name, n)
		}
	}
	// A variable Define reads and something outside it reads too would
	// be read under two rules; EnvVars lists it twice, so this sees it.
	if names := config.EnvVars(); !slices.Equal(names, slices.Compact(slices.Clone(names))) {
		t.Errorf("EnvVars names a variable twice: %v", names)
	}
}

// Every GCAL_ variable the server's code spells is one EnvVars lists, so
// a variable read outside Define cannot escape the bundle and staleness
// gates. A name is seen as a string literal, or as a chain of literals
// and EnvPrefix joined by +; one built from any other constant, or
// through a format verb, is not seen. The scan must read every root and
// find a name outside the config package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	name := regexp.MustCompile(`^GCAL_[A-Z0-9][A-Z0-9_]*$`)
	found := map[string]string{}
	elsewhere := 0
	fset := token.NewFileSet()
	roots := []string{"../../cmd", "../../internal"}
	files := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			outside := filepath.Dir(path) != filepath.Join("..", "..", "internal", "config")
			spell := func(s string, at token.Pos) {
				for _, word := range strings.FieldsFunc(s, func(r rune) bool {
					return r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
				}) {
					if name.MatchString(word) {
						found[word] = fset.Position(at).String()
						if outside {
							elsewhere++
						}
					}
				}
			}
			var visit func(n ast.Node) bool
			visit = func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					if n.Op != token.ADD {
						return true
					}
					// A + chain is read in order: each run of literals
					// and EnvPrefix is one string, and any other operand
					// ends the run and is read on its own.
					run, at := "", n.Pos()
					for _, op := range operands(n) {
						if s, ok := joined(op); ok {
							if run == "" {
								at = op.Pos()
							}
							run += s
							continue
						}
						spell(run, at)
						run = ""
						ast.Inspect(op, visit)
					}
					spell(run, at)
					return false
				case *ast.BasicLit:
					if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
						spell(s, n.Pos())
					}
				}
				return true
			}
			ast.Inspect(f, visit)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range roots {
		if files[root] == 0 {
			t.Errorf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	if elsewhere == 0 {
		t.Error("the scan found no GCAL_ name outside internal/config; it read too little")
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// operands is a + chain's operands in order, through parentheses.
func operands(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(operands(e.X), operands(e.Y)...)
		}
	case *ast.ParenExpr:
		if b, ok := e.X.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return operands(b)
		}
	}
	return []ast.Expr{e}
}

// joined is a + chain of string literals and EnvPrefix as one string.
func joined(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := joined(e.X)
		if !ok {
			return "", false
		}
		y, ok := joined(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return joined(e.X)
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		return config.EnvPrefix, e.Name == "EnvPrefix"
	case *ast.SelectorExpr:
		return config.EnvPrefix, e.Sel.Name == "EnvPrefix"
	}
	return "", false
}
