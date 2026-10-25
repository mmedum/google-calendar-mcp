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
	"slices"
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
	// Sharing registers the ACL tools. On by default; GCAL_SHARING=off
	// removes them, as GDRIVE_SHARING=off does in the sibling Drive
	// server. Sharing a calendar is the outward-facing half of this
	// server and some deployments want it gone entirely. The two that
	// widen access past the organization also need EnableDestructive.
	Sharing bool
	// EnableDeclineAll registers create_event_declining_all: an
	// out-of-office or focus-time event that declines every meeting it
	// overlaps, the accepted ones too. Its own flag rather than the
	// destructive one, because a flag people set to get out-of-office
	// would otherwise arm clear_calendar too (§9).
	EnableDeclineAll bool
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
	EnableDeclineAll  string
	MaxEvents         string
	MaxCalendars      string
	Concurrency       string
	HTTPTimeout       string
	WriteTimeout      string
	ClientSecretPath  string
	// removed are the settings a release took out, in removedSettings'
	// order, read only to refuse one still set.
	removed []string
	// vars is every setting Define reads, in order.
	vars []Var
}

// Var is one variable the server reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Switch marks a setting that is on or off.
	Switch bool
	// Removed marks a setting a release took out, read only to refuse it.
	Removed bool
	// Secret marks a credential. A bundle field that sets one is
	// sensitive, so a host keeps the value out of sight.
	Secret bool
}

// The variables read outside Define. internal/userconfig reads the
// profile directory before any flag is parsed, and internal/credentials
// the refresh token; each takes its name from here, so config imports
// neither, and the bundle gate imports no keyring.
const (
	EnvConfigDir    = EnvPrefix + "CONFIG_DIR"
	EnvRefreshToken = EnvPrefix + "REFRESH_TOKEN"
)

// envOnly is every variable read outside Define, for Vars.
var envOnly = []Var{{Name: EnvConfigDir}, {Name: EnvRefreshToken, Secret: true}}

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// Lookup is the variable name as env gives it, without the spaces around
// it. Spaces alone are "", unset as an empty variable is, so a form field
// cleared to a space means the default rather than a value the server
// refuses. Define reads every setting through it, and so does each
// reader of a variable outside Define.
func Lookup(env func(string) string, name string) string {
	return strings.TrimSpace(env(name))
}

// removedSettings are the settings a release took out, each with why
// and what to do instead. Each was off by default, and unset or off is
// still accepted. On, or a value that is not a boolean, is refused at
// start: ignored, it would quietly leave the server otherwise than its
// owner set it. None has a flag. A setting renamed later goes the same
// way, rather than answering to two names.
var removedSettings = []struct{ key, why string }{
	// The server's question went in 4.0.0 (§9a), and its flag with it.
	{key: "REQUIRE_PROMPT",
		why: "the server no longer asks the person, and the client's own approval decides each call. Unset it, " +
			"and leave " + EnvPrefix + "ENABLE_DESTRUCTIVE and " + EnvPrefix + "ENABLE_DECLINE_ALL off for a client " +
			"that runs with nobody watching"},
	// The read-only switch's name until 4.1.0 renamed it to match the
	// sibling servers and the bundle key read_only. It never had a flag.
	{key: "READONLY",
		why: "it is " + EnvPrefix + "READ_ONLY (-read-only) now. Unset it, and set that in its place"},
}

// removedSet refuses each removed setting that is set to anything but
// off.
func (s *Settings) removedSet() []error {
	var errs []error
	for i, r := range removedSettings {
		if on, err := parseBool(r.key, s.removed[i]); err == nil && !on {
			continue
		}
		errs = append(errs, fmt.Errorf("%w: %s was removed: %s", ErrInvalid, EnvPrefix+r.key, r.why))
	}
	return errs
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GCAL_* variable, so a flag on the command line wins over the
// environment and the environment wins over the built-in default.
//
// Each variable it reads is recorded for Vars, which the staleness and
// bundle gates read, so neither can fall behind a setting added here.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{removed: make([]string, len(removedSettings))}
	bind := func(p *string, name, fallback, usage string, v Var) {
		value := Lookup(env, v.Name)
		if value == "" {
			value = fallback
		}
		fs.StringVar(p, name, value, usage+" [env "+v.Name+"]")
		s.vars = append(s.vars, v)
	}
	def := func(p *string, name, key, fallback, usage string) {
		bind(p, name, fallback, usage, Var{Name: EnvPrefix + key})
	}
	defSwitch := func(p *string, name, key, fallback, usage string) {
		bind(p, name, fallback, usage, Var{Name: EnvPrefix + key, Switch: true})
	}
	def(&s.Profile, "profile", "PROFILE", DefaultProfile, "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	defSwitch(&s.ReadOnly, "read-only", "READ_ONLY", "false", "register only read tools and request read-only scopes")
	defSwitch(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "false", "register delete_calendar, clear_calendar, publish_calendar and widen_calendar_access; each still needs confirm on the call")
	defSwitch(&s.Sharing, "sharing", "SHARING", "on", "register the calendar sharing tools, the wider ones with -enable-destructive: on, off")
	defSwitch(&s.EnableDeclineAll, "enable-decline-all", "ENABLE_DECLINE_ALL", "false", "register create_event_declining_all, an out-of-office or focus-time event that declines every meeting it overlaps")
	def(&s.MaxEvents, "max-events", "MAX_EVENTS", strconv.Itoa(DefaultMaxEvents), "default event budget for a read")
	def(&s.MaxCalendars, "max-calendars", "MAX_CALENDARS", strconv.Itoa(DefaultMaxCalendars), "how many calendars one call may fan out across")
	def(&s.Concurrency, "concurrency", "CONCURRENCY", strconv.Itoa(DefaultConcurrency), "requests in flight during a fan-out")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt timeout for a read")
	def(&s.WriteTimeout, "write-timeout", "WRITE_TIMEOUT", "120s", "timeout for a write")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	for i, r := range removedSettings {
		s.removed[i] = Lookup(env, EnvPrefix+r.key)
		s.vars = append(s.vars, Var{Name: EnvPrefix + r.key, Removed: true})
	}
	return s
}

// Vars is every variable the server reads, sorted by name: Define's,
// the removed ones included, and the ones read outside it. Define's are
// recorded rather than listed, so they cannot fall behind. The bundle
// and staleness gates derive what they hold from these.
func Vars() []Var {
	vars := append(defined(), envOnly...)
	slices.SortFunc(vars, func(a, b Var) int { return strings.Compare(a.Name, b.Name) })
	return vars
}

// defined is every variable Define reads, in the order it reads them.
func defined() []Var {
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return Define(fs, func(string) string { return "" }).vars
}

// EnvVars is the names of Vars, sorted. A name listed twice is a
// variable read under two rules, which a test refuses, so it is not
// compacted.
func EnvVars() []string {
	vars := Vars()
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	return names
}

// Load is Define, Parse and Build for a caller with no flags of its own.
func Load(args []string, env func(string) string) (Config, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Define(fs, env)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return s.Build()
}

// ProfileRule is profilePattern in words, which Build's refusal and the
// bundle's profile field both give.
const ProfileRule = "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit"

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// asciiLower lowers A to Z alone, so a character Unicode folds into a
// to z, such as the Kelvin sign, is refused rather than renamed.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = asciiLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must be %s", ErrInvalid, s.Profile, ProfileRule))
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
	if c.EnableDeclineAll, err = parseBool("enable-decline-all", s.EnableDeclineAll); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, s.removedSet()...)

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
