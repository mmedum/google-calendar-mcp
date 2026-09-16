// Package config loads and validates runtime configuration.
//
// Environment variables (GCAL_*) are the source of truth because every
// MCP client that matters passes only command, args and env to a stdio
// server. Each setting also has a flag bound to the same name; a flag
// given explicitly overrides the environment. Validation runs once at
// start so a misconfigured server fails before it announces itself.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "GCAL_"

// LogLevel is a typed enum constrained at load time.
type LogLevel string

// Allowed LogLevel values.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Slog returns the slog.Level for this level.
func (l LogLevel) Slog() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormat is a typed enum constrained at load time.
type LogFormat string

// Allowed LogFormat values.
const (
	LogText LogFormat = "text"
	LogJSON LogFormat = "json"
)

// Read budgets, counted in events rather than requests (§4.5). A tool
// call that fans out across calendars can spend many requests for few
// events, and the number that matters to a client's result limit — and
// to a model's attention — is the events.
const (
	DefaultMaxEvents = 250
	MaxMaxEvents     = 2500
)

// Fan-out limits. §11: 600 requests per user per minute is the ceiling,
// and the fan-out tools are where one tool call can spend dozens.
const (
	// DefaultMaxCalendars bounds how many calendars one call may read.
	DefaultMaxCalendars = 25
	MaxMaxCalendars     = 50
	// DefaultConcurrency bounds requests in flight during a fan-out.
	DefaultConcurrency = 4
	MaxConcurrency     = 16
	// MaxFreeBusyCalendars bounds one availability call, and is higher
	// than MaxCalendars because the cost is different: a schedule read
	// spends one request per calendar, while free/busy answers for 50 in
	// one (§2.10). Asking about a whole team is the ordinary case for
	// that tool and would otherwise be refused at 25.
	MaxFreeBusyCalendars = 100
)

// Config is the validated runtime configuration.
type Config struct {
	Profile           string
	LogLevel          LogLevel
	LogFormat         LogFormat
	ReadOnly          bool
	EnableDestructive bool
	// Sharing registers the three ACL tools. On by default; GCAL_SHARING=off
	// removes them, as GDRIVE_SHARING=off does in the sibling Drive
	// server. Sharing a calendar is the outward-facing half of this
	// server and some deployments want it gone entirely.
	Sharing          bool
	MaxEvents        int
	MaxCalendars     int
	Concurrency      int
	HTTPTimeout      time.Duration
	WriteTimeout     time.Duration
	ClientSecretPath string
}

// Settings holds the raw string values before validation. Flags and the
// environment both feed it; Build turns it into a Config.
type Settings struct {
	Profile           string
	LogLevel          string
	LogFormat         string
	ReadOnly          string
	EnableDestructive string
	Sharing           string
	MaxEvents         string
	MaxCalendars      string
	Concurrency       string
	HTTPTimeout       string
	WriteTimeout      string
	ClientSecretPath  string
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GCAL_* variable, so a flag on the command line wins over the
// environment and the environment wins over the built-in default.
//
// The staleness gate reads these def() calls to learn the setting names,
// so the documentation cannot fall behind a setting added here.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{}
	def := func(p *string, name, key, fallback, usage string) {
		v := env(EnvPrefix + key)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
	}
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	def(&s.ReadOnly, "read-only", "READONLY", "false", "register only read tools and request read-only scopes")
	def(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "false", "register delete_calendar and clear_calendar; each still needs confirm on the call")
	def(&s.Sharing, "sharing", "SHARING", "on", "register the calendar sharing tools: on, off")
	def(&s.MaxEvents, "max-events", "MAX_EVENTS", strconv.Itoa(DefaultMaxEvents), "default event budget for a read")
	def(&s.MaxCalendars, "max-calendars", "MAX_CALENDARS", strconv.Itoa(DefaultMaxCalendars), "how many calendars one call may fan out across")
	def(&s.Concurrency, "concurrency", "CONCURRENCY", strconv.Itoa(DefaultConcurrency), "requests in flight during a fan-out")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt timeout for a read")
	def(&s.WriteTimeout, "write-timeout", "WRITE_TIMEOUT", "120s", "timeout for a write")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
}

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = strings.ToLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must match %s", ErrInvalid, s.Profile, profilePattern))
	}

	c.LogLevel = LogLevel(strings.ToLower(strings.TrimSpace(s.LogLevel)))
	if !logLevels[c.LogLevel] {
		errs = append(errs, fmt.Errorf("%w: log level %q (want debug, info, warn, error)", ErrInvalid, s.LogLevel))
	}
	c.LogFormat = LogFormat(strings.ToLower(strings.TrimSpace(s.LogFormat)))
	if !logFormats[c.LogFormat] {
		errs = append(errs, fmt.Errorf("%w: log format %q (want text, json)", ErrInvalid, s.LogFormat))
	}

	var err error
	if c.ReadOnly, err = parseBool("read-only", s.ReadOnly); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDestructive, err = parseBool("enable-destructive", s.EnableDestructive); err != nil {
		errs = append(errs, err)
	}
	// Sharing takes on/off as well as true/false, because the sibling
	// spells it GDRIVE_SHARING=off and a user moving between the two
	// should not have to learn a second spelling.
	if c.Sharing, err = parseOnOff("sharing", s.Sharing); err != nil {
		errs = append(errs, err)
	}

	if c.MaxEvents, err = parseCount("max-events", s.MaxEvents, DefaultMaxEvents, MaxMaxEvents); err != nil {
		errs = append(errs, err)
	}
	if c.MaxCalendars, err = parseCount("max-calendars", s.MaxCalendars, DefaultMaxCalendars, MaxMaxCalendars); err != nil {
		errs = append(errs, err)
	}
	if c.Concurrency, err = parseCount("concurrency", s.Concurrency, DefaultConcurrency, MaxConcurrency); err != nil {
		errs = append(errs, err)
	}

	if c.HTTPTimeout, err = parseTimeout("http-timeout", s.HTTPTimeout); err != nil {
		errs = append(errs, err)
	}
	if c.WriteTimeout, err = parseTimeout("write-timeout", s.WriteTimeout); err != nil {
		errs = append(errs, err)
	}

	c.ClientSecretPath = strings.TrimSpace(s.ClientSecretPath)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return c, nil
}

func parseBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s %q (want true or false)", ErrInvalid, name, v)
}

func parseOnOff(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "1", "true", "yes":
		return true, nil
	case "off", "0", "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("%w: %s %q (want on or off)", ErrInvalid, name, v)
}

func parseCount(name, v string, fallback, maxValue int) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q is not a number", ErrInvalid, name, v)
	}
	if n < 1 || n > maxValue {
		return 0, fmt.Errorf("%w: %s %d must be between 1 and %d", ErrInvalid, name, n, maxValue)
	}
	return n, nil
}

func parseTimeout(name, v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %w", ErrInvalid, name, v, err)
	}
	if d <= 0 || d > 10*time.Minute {
		return 0, fmt.Errorf("%w: %s %s must be between 1s and 10m", ErrInvalid, name, d)
	}
	return d, nil
}

// NewLogger builds the process logger. It writes to w, which must be
// stderr on the server path: stdout carries only JSON-RPC frames.
func NewLogger(c Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel.Slog()}
	if c.LogFormat == LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
