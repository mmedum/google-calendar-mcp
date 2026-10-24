package config_test

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v4/internal/config"
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
		{"read-only under its old name", "GCAL_READONLY", "perhaps"},
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

// The read-only switch is GCAL_READ_ONLY, the family's name, and
// GCAL_READONLY, its name until 4.1.0, still works. Both set and
// disagreeing is refused rather than settled by either.
func TestTheReadOnlySwitchAnswersToItsOldName(t *testing.T) {
	const (
		newName = "GCAL_READ_ONLY"
		oldName = "GCAL_READONLY"
	)
	renamed := []config.Rename{{Old: oldName, New: newName}}
	for _, tc := range []struct {
		name       string
		env        map[string]string
		args       []string
		on         bool
		deprecated []config.Rename
	}{
		{name: "neither", on: false},
		{name: "the new name on", env: map[string]string{newName: "true"}, on: true},
		{name: "the new name off", env: map[string]string{newName: "false"}, on: false},
		{name: "the old name alone on", env: map[string]string{oldName: "true"}, on: true, deprecated: renamed},
		{name: "the old name alone off", env: map[string]string{oldName: "false"}, on: false, deprecated: renamed},
		{name: "both on", env: map[string]string{newName: "true", oldName: "1"}, on: true, deprecated: renamed},
		{name: "both off", env: map[string]string{newName: "off", oldName: "false"}, on: false, deprecated: renamed},
		{name: "the flag", args: []string{"-read-only", "true"}, on: true},
		{name: "the flag agreeing with the old name", env: map[string]string{oldName: "true"},
			args: []string{"-read-only", "true"}, on: true, deprecated: renamed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := build(t, tc.env, tc.args...)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if c.ReadOnly != tc.on {
				t.Errorf("ReadOnly = %t, want %t", c.ReadOnly, tc.on)
			}
			if !slices.Equal(c.Deprecated, tc.deprecated) {
				t.Errorf("Deprecated = %v, want %v", c.Deprecated, tc.deprecated)
			}
		})
	}

	for _, tc := range []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{name: "the new on, the old off", env: map[string]string{newName: "true", oldName: "false"},
			want: newName + " is true and " + oldName + ", its old name, is false: unset " + oldName},
		{name: "the new off, the old on", env: map[string]string{newName: "false", oldName: "true"},
			want: newName + " is false and " + oldName + ", its old name, is true"},
		{name: "the flag against the old name", env: map[string]string{oldName: "true"},
			args: []string{"-read-only", "false"}, want: newName + " is false and " + oldName},
		{name: "the old name not a boolean", env: map[string]string{oldName: "maybe"},
			want: oldName + ` "maybe"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(t, tc.env, tc.args...)
			if err == nil || !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an invalid setting naming %q", err, tc.want)
			}
		})
	}
}

// Vars is what the bundle gate reads a setting's default and kind from,
// so each claim it makes is held to what Build does. A switch takes true
// and false and refuses anything else, and nothing else does that. Its
// default and an empty value each configure what setting nothing does,
// which is what lets a bundle leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	vars := config.Vars()
	if len(vars) != 15 {
		t.Fatalf("Vars has %d settings; Define reads 15", len(vars))
	}
	names := make([]string, len(vars))
	for i, v := range vars {
		names[i] = v.Name
	}
	slices.Sort(names)
	if want := config.EnvVars(); !slices.Equal(names, want) {
		t.Errorf("Vars names\n%v\nwant every variable Define reads\n%v", names, want)
	}
	unset, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	var switches []string
	for _, v := range vars {
		accepts := func(value string) bool {
			_, err := build(t, map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Build reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if v.Switch {
			switches = append(switches, v.Name)
			if v.Default != "true" && v.Default != "false" {
				t.Errorf("%s is a switch defaulting to %q, which a bundle's checkbox cannot be", v.Name, v.Default)
			}
		}
		for _, value := range []string{v.Default, ""} {
			c, err := build(t, map[string]string{v.Name: value})
			if err != nil {
				t.Errorf("%s=%q is refused: %v", v.Name, value, err)
				continue
			}
			c.Deprecated = nil
			if !reflect.DeepEqual(c, unset) {
				t.Errorf("%s=%q configures something:\n got %+v\nwant %+v", v.Name, value, c, unset)
			}
		}
		if v.ReplacedBy != "" && !slices.ContainsFunc(vars, func(w config.Var) bool { return w.Name == v.ReplacedBy }) {
			t.Errorf("%s is replaced by %s, which is not a setting", v.Name, v.ReplacedBy)
		}
	}
	want := []string{"GCAL_READ_ONLY", "GCAL_ENABLE_DESTRUCTIVE", "GCAL_SHARING", "GCAL_ENABLE_DECLINE_ALL", "GCAL_READONLY"}
	if !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
	for name, def := range map[string]string{"GCAL_SHARING": "true", "GCAL_READ_ONLY": "false"} {
		i := slices.IndexFunc(vars, func(v config.Var) bool { return v.Name == name })
		if i < 0 || vars[i].Default != def {
			t.Errorf("%s does not default to %s in Vars", name, def)
		}
	}
	if i := slices.IndexFunc(vars, func(v config.Var) bool { return v.Removed }); i < 0 || vars[i].Name != "GCAL_REQUIRE_PROMPT" {
		t.Errorf("Vars does not mark GCAL_REQUIRE_PROMPT removed")
	}
}

// EnvVars is recorded from the lookups Define makes, so it holds the old
// and removed names that no flag binds as well as every one that does.
func TestEnvVarsIsEveryLookup(t *testing.T) {
	got := config.EnvVars()
	for _, name := range []string{"GCAL_READ_ONLY", "GCAL_READONLY", "GCAL_REQUIRE_PROMPT", "GCAL_CLIENT_SECRET"} {
		if !slices.Contains(got, name) {
			t.Errorf("EnvVars lacks %s, which Define reads: %v", name, got)
		}
	}
	if len(got) != 15 || !slices.IsSorted(got) {
		t.Errorf("EnvVars is %v; want the 15 variables Define reads, sorted", got)
	}
}
