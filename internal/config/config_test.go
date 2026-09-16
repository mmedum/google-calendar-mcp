package config_test

import (
	"bytes"
	"flag"
	"log/slog"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/config"
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
		{"read-only", "GCAL_READONLY", "perhaps"},
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
