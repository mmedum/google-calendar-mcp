package config_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/v5/internal/userconfig"
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

// Every flag names its variable in --help, and the flags are Define's
// settings less the removed ones, which have none.
func TestEveryFlagNamesItsEnvVar(t *testing.T) {
	registry(t)
	var want []string
	for _, v := range config.Defined() {
		if !v.Removed {
			want = append(want, v.Name)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	config.Define(fs, func(string) string { return "" })
	envName := regexp.MustCompile(`\[env (` + config.EnvPrefix + `[A-Z0-9_]+)\]$`)
	var named []string
	fs.VisitAll(func(f *flag.Flag) {
		m := envName.FindStringSubmatch(f.Usage)
		if m == nil {
			t.Errorf("flag -%s does not name its environment variable: %q", f.Name, f.Usage)
			return
		}
		named = append(named, m[1])
	})
	slices.Sort(want)
	slices.Sort(named)
	if !slices.Equal(named, want) {
		t.Errorf("the flags name %v\nDefine's settings are %v", named, want)
	}
}

// --help shows each switch's default, which is Build's for an empty
// value too.
func TestASwitchsHelpShowsItsDefault(t *testing.T) {
	var help bytes.Buffer
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&help)
	config.Define(fs, func(string) string { return "" })
	fs.PrintDefaults()
	for _, want := range []string{
		"[env GCAL_READ_ONLY] (default \"false\")\n",
		"[env GCAL_ENABLE_DESTRUCTIVE] (default \"false\")\n",
		"[env GCAL_SHARING] (default \"on\")\n",
		"[env GCAL_ENABLE_DECLINE_ALL] (default \"false\")\n",
	} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("--help does not say %q:\n%s", want, help.String())
		}
	}
	unset, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := build(t, nil, "-read-only=", "-enable-destructive=", "-sharing=", "-enable-decline-all=")
	if err != nil || !reflect.DeepEqual(empty, unset) {
		t.Errorf("empty switches: %+v, %v; want the unconfigured configuration %+v", empty, err, unset)
	}
}

// A removed setting starts when unset or off, and does nothing: for the
// read-only switch's old name, GCAL_READ_ONLY decides. On, or not a
// boolean, it is refused, beside its replacement too, saying what to do
// instead and not echoing the value. None has a flag.
func TestARemovedSettingIsRefusedUnlessOff(t *testing.T) {
	refusal := map[string]string{
		"GCAL_READONLY": "config: invalid: GCAL_READONLY was removed: " +
			"it is GCAL_READ_ONLY (-read-only) now. Unset it, and set that in its place",
		"GCAL_REQUIRE_PROMPT": "config: invalid: GCAL_REQUIRE_PROMPT was removed: " +
			"the server no longer asks the person, and the client's own approval decides each call. Unset it, " +
			"and leave GCAL_ENABLE_DESTRUCTIVE and GCAL_ENABLE_DECLINE_ALL off for a client that runs with nobody watching",
	}
	type run struct {
		name string
		env  map[string]string
		args []string
		// want is the refusal, or empty for a run that starts.
		want string
		// readOnly is ReadOnly in a run that starts: the new switch's.
		readOnly bool
	}
	var runs []run
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(string) string { return "" })
	for _, v := range registry(t) {
		if !v.Removed {
			continue
		}
		if name := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(v.Name, config.EnvPrefix)), "_", "-"); fs.Lookup(name) != nil {
			t.Errorf("-%s is a flag; %s is removed", name, v.Name)
		}
		want, ok := refusal[v.Name]
		if !ok {
			t.Errorf("%s is removed, and this test has no refusal for it", v.Name)
			continue
		}
		for _, value := range []string{"false", "0", " OFF "} {
			runs = append(runs, run{name: v.Name + "=" + value, env: map[string]string{v.Name: value}})
		}
		for _, value := range []string{"true", "1", "always"} {
			runs = append(runs, run{name: v.Name + "=" + value, env: map[string]string{v.Name: value}, want: want})
		}
	}
	runs = append(runs,
		run{name: "the old read-only name off leaves the new one on",
			env: map[string]string{"GCAL_READONLY": "false", "GCAL_READ_ONLY": "true"}, readOnly: true},
		run{name: "the old read-only name off leaves the flag on",
			env: map[string]string{"GCAL_READONLY": "0"}, args: []string{"-read-only", "true"}, readOnly: true},
		run{name: "the old read-only name on beside the new one",
			env: map[string]string{"GCAL_READONLY": "on", "GCAL_READ_ONLY": "true"}, want: refusal["GCAL_READONLY"]},
		run{name: "the old read-only name on beside the flag",
			env: map[string]string{"GCAL_READONLY": "yes"}, args: []string{"-read-only", "true"}, want: refusal["GCAL_READONLY"]},
	)
	for _, tc := range runs {
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
			if c.ReadOnly != tc.readOnly {
				t.Errorf("ReadOnly = %t, want the new switch's %t", c.ReadOnly, tc.readOnly)
			}
		})
	}
}

// Vars is what the bundle gate reads a setting's kind from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. A
// removed setting takes false and refuses true. The refresh token is
// the one credential: GCAL_CLIENT_SECRET names the client file by its
// path, which is not itself a secret.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	var switches, removed, secrets []string
	for _, v := range registry(t) {
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
		if v.Secret {
			secrets = append(secrets, v.Name)
		}
	}
	if want := []string{"GCAL_ENABLE_DECLINE_ALL", "GCAL_ENABLE_DESTRUCTIVE", "GCAL_READ_ONLY", "GCAL_SHARING"}; !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
	if want := []string{"GCAL_READONLY", "GCAL_REQUIRE_PROMPT"}; !slices.Equal(removed, want) {
		t.Errorf("Vars marks %v removed, want %v", removed, want)
	}
	if want := []string{"GCAL_REFRESH_TOKEN"}; !slices.Equal(secrets, want) {
		t.Errorf("Vars marks %v secret, want %v", secrets, want)
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

// varsFloor is how many variables the server reads today, on purpose:
// the floor moves with it, so a registry that records nothing cannot
// pass a walk over it. Define's are Vars less the two read outside it,
// so the floor holds a walk over Defined as well.
const varsFloor = 17

// registry is config.Vars, refused below the floor.
func registry(t *testing.T) []config.Var {
	t.Helper()
	vars := config.Vars()
	if len(vars) < varsFloor {
		t.Fatalf("Vars has %d variables, want at least %d", len(vars), varsFloor)
	}
	return vars
}

// A variable of spaces alone is unset, as an empty one is, and the spaces
// around a value are dropped, so a form field cleared to a space starts
// the server with the default. That holds for every variable the server
// reads: Define's through Build, and each one read outside Define through
// its reader. One read outside Define with no reader here fails, so a
// variable added there cannot miss the rule.
func TestASettingOfSpacesIsUnset(t *testing.T) {
	outside := map[string]func(t *testing.T, value string) string{
		config.EnvConfigDir: func(t *testing.T, value string) string {
			t.Setenv(config.EnvConfigDir, value)
			dir, err := userconfig.BaseDir()
			return fmt.Sprintf("%q %v", dir, err)
		},
		config.EnvRefreshToken: func(_ *testing.T, value string) string {
			s := credentials.Store{Profile: "p", Env: func(string) string { return value }}
			tok, src, err := s.Resolve()
			return fmt.Sprintf("%q %q %v", tok, src, err)
		},
	}
	defined := map[string]bool{}
	for _, v := range config.Defined() {
		defined[v.Name] = true
	}
	for _, v := range registry(t) {
		read := func(t *testing.T, value string) string {
			c, err := build(t, map[string]string{v.Name: value})
			return fmt.Sprintf("%+v %v", c, err)
		}
		if !defined[v.Name] {
			var ok bool
			if read, ok = outside[v.Name]; !ok {
				t.Errorf("%s is read outside Define, and this test has no reader for it", v.Name)
				continue
			}
			delete(outside, v.Name)
		}
		for _, pair := range [][2]string{{"  ", ""}, {" x ", "x"}} {
			if got, want := read(t, pair[0]), read(t, pair[1]); got != want {
				t.Errorf("%s=%q: %s\nwant %s, as %s=%q", v.Name, pair[0], got, want, v.Name, pair[1])
			}
		}
	}
	for name := range outside {
		t.Errorf("this test has a reader for %s, which Vars does not list as read outside Define", name)
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
	registry(t)
	reads := map[string]int{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(name string) string { reads[name]++; return "" })
	var listed []string
	for _, v := range config.Defined() {
		listed = append(listed, v.Name)
	}
	slices.Sort(listed)
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

// codeRoots are where the scans read the code from: the server's, and
// scripts/, where the live driver and the smoke test set variables for
// the server. Tests are not read; they state names as expected values.
var (
	codeRoots = []string{"../../cmd", "../../internal", "../../scripts"}
	configDir = filepath.Join("..", "..", "internal", "config")
	// liveDriver is the live driver's directory. Its own variables,
	// GCAL_LIVE_ and a name, are left out of every scan, since the
	// driver reads them itself and the server never does.
	liveDriver = filepath.Join("..", "..", "scripts", "livecal") + string(filepath.Separator)
)

// codeFile is one parsed file of the code the scans read.
type codeFile struct {
	f    *ast.File
	path string
	// inConfig is a file of internal/config, where the names are defined.
	inConfig bool
}

// driverOwn reports whether word, spelled in the file at path, is one of
// the live driver's own variables.
func (c codeFile) driverOwn(word string) bool {
	return strings.HasPrefix(c.path, liveDriver) && strings.HasPrefix(word, config.EnvPrefix+"LIVE_")
}

// codeFiles parses every Go file under codeRoots, tests aside. It fails
// when a root gives no file, so a scan cannot pass by reading too little.
func codeFiles(t *testing.T) (*token.FileSet, []codeFile) {
	t.Helper()
	fset := token.NewFileSet()
	var parsed []codeFile
	files := map[string]int{}
	for _, root := range codeRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			parsed = append(parsed, codeFile{f: f, path: path, inConfig: filepath.Dir(path) == configDir})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range codeRoots {
		if files[root] == 0 {
			t.Fatalf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	return fset, parsed
}

// Every GCAL_ variable the server's code spells is one EnvVars lists, so
// a variable read outside Define cannot escape the bundle and staleness
// gates. So is every one scripts/ spells: the live driver and the smoke
// test set them for the server, and a misspelling there tests a
// configuration nobody asked for. A name is seen as a string literal,
// or as a chain of literals and EnvPrefix joined by +; one built from
// any other constant, or through a format verb, is not seen. The scan
// must find a name outside the config package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	found := map[string]string{}
	elsewhere := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		for word, at := range namesIn(file.f) {
			if file.driverOwn(word) {
				continue
			}
			found[word] = fset.Position(at).String()
			if !file.inConfig {
				elsewhere++
			}
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

// Outside internal/config a variable's name is spelled from config's
// constants, EnvPrefix joined to the key or a constant such as
// EnvConfigDir, never typed whole in a literal: a guard's refusal, a
// tool's description, the smoke test's environment and the live
// driver's included. A struct tag is the exception, since Go takes only
// a literal there. The scan must find a name joined from EnvPrefix
// outside the config package, or it read too little.
func TestVariableNamesAreSpelledFromConfig(t *testing.T) {
	joinedElsewhere := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		if file.inConfig {
			continue
		}
		typed, tagged := literalNames(file.f)
		for _, word := range slices.Sorted(maps.Keys(typed)) {
			if !file.driverOwn(word) {
				t.Errorf("%s types %s in a literal; join config.EnvPrefix to the key, or use config's constant",
					fset.Position(typed[word]), word)
			}
		}
		for word := range namesIn(file.f) {
			if _, ok := typed[word]; !ok {
				if _, ok := tagged[word]; !ok {
					joinedElsewhere++
				}
			}
		}
	}
	if joinedElsewhere == 0 {
		t.Error("the scan found no name joined from EnvPrefix outside internal/config; it read too little")
	}
}

// literalNames is every GCAL_ name f types whole inside one string
// literal, each with where: typed outside a struct tag, and tagged in
// one.
func literalNames(f *ast.File) (typed, tagged map[string]token.Pos) {
	tags := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok && field.Tag != nil {
			tags[field.Tag] = true
		}
		return true
	})
	typed, tagged = map[string]token.Pos{}, map[string]token.Pos{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		into := typed
		if tags[lit] {
			into = tagged
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			for _, word := range words(s) {
				if variableName.MatchString(word) {
					into[word] = lit.Pos()
				}
			}
		}
		return true
	})
	return typed, tagged
}

// Every reader of a server variable goes through config, Lookup or
// Load, or a reader built on Lookup such as userconfig.BaseDir, so each
// is read trimmed and by the server's own rules. No file the scans read
// calls os.Getenv or os.LookupEnv with a GCAL_ name, whether a literal,
// a chain from EnvPrefix or one of config's constants for a name. The
// constants are read from config's source, and the scan must see a call
// of either function, or it read too little.
func TestEveryReaderOfAVariableGoesThroughConfig(t *testing.T) {
	constants := nameConstants(t)
	if !constants["EnvConfigDir"] || !constants["EnvRefreshToken"] {
		t.Fatalf("config's constants for a name were read as %v; the derivation read too little", constants)
	}
	calls := 0
	fset, files := codeFiles(t)
	for _, file := range files {
		ast.Inspect(file.f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			fun, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (fun.Sel.Name != "Getenv" && fun.Sel.Name != "LookupEnv") {
				return true
			}
			if pkg, ok := fun.X.(*ast.Ident); !ok || pkg.Name != "os" {
				return true
			}
			calls++
			if namesAVariable(call.Args[0], constants) {
				t.Errorf("%s reads a server variable with os.%s; read it through config.Lookup or config.Load",
					fset.Position(call.Pos()), fun.Sel.Name)
			}
			return true
		})
	}
	if calls == 0 {
		t.Error("the scan saw no call of os.Getenv or os.LookupEnv; it read too little")
	}
}

// namesAVariable reports whether e names a server variable: a chain
// that joins to a GCAL_ string, or one mentioning a constant config
// spells a name with.
func namesAVariable(e ast.Expr, constants map[string]bool) bool {
	if s, ok := joined(e); ok {
		return strings.HasPrefix(s, config.EnvPrefix)
	}
	named := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			named = named || constants[n.Sel.Name]
			return false
		case *ast.Ident:
			named = named || constants[n.Name]
		}
		return true
	})
	return named
}

// nameConstants is every constant internal/config declares whose value
// is a chain from EnvPrefix, EnvPrefix included, by name.
func nameConstants(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(configDir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if i < len(value.Values) {
						if s, ok := joined(value.Values[i]); ok && strings.HasPrefix(s, config.EnvPrefix) {
							found[ident.Name] = true
						}
					}
				}
			}
		}
	}
	return found
}

// variableName is a word that is a variable's name.
var variableName = regexp.MustCompile(`^` + config.EnvPrefix + `[A-Z0-9][A-Z0-9_]*$`)

// words is s split into runs of capitals, digits and underscores, the
// characters a name is made of.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !nameChar(r) })
}

// nameChar reports whether r can be part of a name.
func nameChar(r rune) bool {
	return r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// namesIn is every GCAL_ name f spells, each with where it is spelled.
// A run of literals that another operand follows ends in a prefix
// rather than a name when its last character is part of a name, as in
// EnvPrefix + "ENABLE_" + kind, so that last word is not read.
func namesIn(f *ast.File) map[string]token.Pos {
	found := map[string]token.Pos{}
	spell := func(s string, at token.Pos) {
		for _, word := range words(s) {
			if variableName.MatchString(word) {
				found[word] = at
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
			// A + chain is read in order: each run of literals and
			// EnvPrefix is one string, and any other operand ends the
			// run and is read on its own.
			run, at := "", n.Pos()
			for _, op := range operands(n) {
				if s, ok := joined(op); ok {
					if run == "" {
						at = op.Pos()
					}
					run += s
					continue
				}
				spell(strings.TrimRightFunc(run, nameChar), at)
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
	return found
}

// The scan reads a literal, EnvPrefix joined to literals by +, through
// a selector and parentheses, and a run that starts after another
// operand, which a scan reading only a chain's head would miss. A run
// that another operand follows ends in a prefix when it ends in a name's
// character, as EnvPrefix + "ENABLE_" + kind does, and the rest of it
// is still read. A name built from another constant or through a format
// verb is not seen.
func TestTheScanReadsEachChainInOrder(t *testing.T) {
	const src = `package p

var (
	a = "set GCAL_PLAIN=true"
	b = EnvPrefix + "JOINED"
	c = config.EnvPrefix + "SELECTED"
	d = f() + EnvPrefix + "LATER=" + g() + "GCAL_" + "LAST"
	e = "x" + (EnvPrefix + "PAREN")
	h = fmt.Sprintf("%sFORMAT", EnvPrefix)
	i = other + "BUILT"
	j = EnvPrefix + "ENABLE_" + kind
	k = "set " + x + EnvPrefix + "NEW"
	l = "GCAL_KEPT=1, " + EnvPrefix + "CUT_" + kind
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GCAL_JOINED", "GCAL_KEPT", "GCAL_LAST", "GCAL_LATER", "GCAL_NEW", "GCAL_PAREN",
		"GCAL_PLAIN", "GCAL_SELECTED"}
	if got := slices.Sorted(maps.Keys(namesIn(f))); !slices.Equal(got, want) {
		t.Errorf("namesIn = %v, want %v", got, want)
	}
}

// A name typed whole in a literal is found, in a struct tag apart, and
// one joined from EnvPrefix is not typed.
func TestLiteralNamesFindsANameTypedWhole(t *testing.T) {
	const src = `package p

type s struct {
	F string ` + "`json:\"f\" jsonschema:\"written under GCAL_TAGGED\"`" + `
}

var (
	a = "set GCAL_TYPED=true"
	b = EnvPrefix + "JOINED"
	c = "the GCAL_* settings"
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	typed, tagged := literalNames(f)
	if got := slices.Sorted(maps.Keys(typed)); !slices.Equal(got, []string{"GCAL_TYPED"}) {
		t.Errorf("typed = %v, want [GCAL_TYPED]", got)
	}
	if got := slices.Sorted(maps.Keys(tagged)); !slices.Equal(got, []string{"GCAL_TAGGED"}) {
		t.Errorf("tagged = %v, want [GCAL_TAGGED]", got)
	}
}

// A read is of a server variable when its argument joins to a GCAL_
// string or mentions one of config's constants for a name.
func TestNamesAVariable(t *testing.T) {
	constants := map[string]bool{"EnvConfigDir": true}
	for src, want := range map[string]bool{
		`"GCAL_ENABLE_DESTRUCTIVE"`:    true,
		`config.EnvPrefix + "PROFILE"`: true,
		`config.EnvConfigDir`:          true,
		`EnvConfigDir`:                 true,
		`"ANTHROPIC_API_KEY"`:          false,
		`envAttachment`:                false,
	} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := namesAVariable(e, constants); got != want {
			t.Errorf("namesAVariable(%s) = %t, want %t", src, got, want)
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
