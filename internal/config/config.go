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
	// Deprecated is each old setting name this run was given, for the
	// caller to warn about once it has a logger.
	Deprecated []Rename
}

// Rename is a setting that took a new name and still answers to the
// old one.
type Rename struct {
	Old, New string
}

// renamedReadOnly is the read-only switch's name until 4.1.0, renamed to
// match the sibling servers and the bundle key read_only.
var renamedReadOnly = Rename{Old: EnvPrefix + "READONLY", New: EnvPrefix + "READ_ONLY"}

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
	// ReadOnlyOld is GCAL_READONLY, ReadOnly's old name, still read.
	ReadOnlyOld string
	// RequirePrompt is the removed GCAL_REQUIRE_PROMPT, read only to
	// refuse it turned on.
	RequirePrompt    string
	MaxEvents        string
	MaxCalendars     string
	Concurrency      string
	HTTPTimeout      string
	WriteTimeout     string
	ClientSecretPath string
	// vars is every setting Define reads, in order.
	vars []Var
}

// Var is one setting Define reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Default is what the server uses when neither the variable nor its
	// flag is set. A switch's is "true" or "false", which is also how a
	// bundle's checkbox reaches the environment.
	Default string
	// Switch marks a setting that is on or off.
	Switch bool
	// ReplacedBy names the variable that took this one's place. The old
	// name still works, and nothing should offer it.
	ReplacedBy string
	// Removed marks a setting a release took out, read only to refuse it.
	Removed bool
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GCAL_* variable, so a flag on the command line wins over the
// environment and the environment wins over the built-in default.
//
// The staleness gate reads these def() and sw() calls to learn the
// setting names, so the documentation cannot fall behind a setting added
// here.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{}
	bind := func(p *string, name, fallback, usage string, v Var) {
		value := env(v.Name)
		if value == "" {
			value = fallback
		}
		fs.StringVar(p, name, value, usage+" [env "+v.Name+"]")
		s.vars = append(s.vars, v)
	}
	def := func(p *string, name, key, fallback, usage string) {
		bind(p, name, fallback, usage, Var{Name: EnvPrefix + key, Default: fallback})
	}
	// A switch is registered empty rather than with its default, so
	// Build can tell one nobody set from one set to its default. Empty
	// is the default.
	sw := func(p *string, name, key string, on bool, usage string) {
		bind(p, name, "", usage, Var{Name: EnvPrefix + key, Default: strconv.FormatBool(on), Switch: true})
	}
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	sw(&s.ReadOnly, "read-only", "READ_ONLY", false, "register only read tools and request read-only scopes (default off)")
	sw(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", false, "register delete_calendar, clear_calendar, publish_calendar and widen_calendar_access; each still needs confirm on the call (default off)")
	sw(&s.Sharing, "sharing", "SHARING", true, "register the calendar sharing tools, the wider ones with -enable-destructive: on, off (default on)")
	sw(&s.EnableDeclineAll, "enable-decline-all", "ENABLE_DECLINE_ALL", false, "register create_event_declining_all, an out-of-office or focus-time event that declines every meeting it overlaps (default off)")
	def(&s.MaxEvents, "max-events", "MAX_EVENTS", strconv.Itoa(DefaultMaxEvents), "default event budget for a read")
	def(&s.MaxCalendars, "max-calendars", "MAX_CALENDARS", strconv.Itoa(DefaultMaxCalendars), "how many calendars one call may fan out across")
	def(&s.Concurrency, "concurrency", "CONCURRENCY", strconv.Itoa(DefaultConcurrency), "requests in flight during a fan-out")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt timeout for a read")
	def(&s.WriteTimeout, "write-timeout", "WRITE_TIMEOUT", "120s", "timeout for a write")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	renamed(&s.ReadOnlyOld, env, "READONLY")
	s.vars = append(s.vars, Var{Name: renamedReadOnly.Old, Default: "false", Switch: true, ReplacedBy: renamedReadOnly.New})
	removed(&s.RequirePrompt, env, "REQUIRE_PROMPT")
	s.vars = append(s.vars, Var{Name: EnvPrefix + "REQUIRE_PROMPT", Removed: true})
	return s
}

// renamed reads a setting's old name, with no flag: the flag kept its
// name, and only the variable moved. The staleness gate reads these
// calls, so the documentation may name an old name to say it still
// works.
func renamed(p *string, env func(string) string, key string) {
	*p = env(EnvPrefix + key)
}

// removed reads a setting a release took away, with no flag: it is read
// only so Build can refuse it turned on. The staleness gate reads these
// calls, so the documentation may name a removed setting to say it is
// gone.
func removed(p *string, env func(string) string, key string) {
	*p = env(EnvPrefix + key)
}

// Vars is every setting Define reads, in the order it reads them. The
// bundle gate reads each switch's default here.
func Vars() []Var {
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return Define(fs, func(string) string { return "" }).vars
}

// EnvVars is every variable Define looks up, sorted. It is recorded from
// the lookups rather than listed, so it cannot fall behind.
func EnvVars() []string {
	var seen []string
	fs := flag.NewFlagSet("envvars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	Define(fs, func(name string) string {
		seen = append(seen, name)
		return ""
	})
	slices.Sort(seen)
	return slices.Compact(seen)
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
	if c.ReadOnly, err = s.readOnly(); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(s.ReadOnlyOld) != "" {
		c.Deprecated = append(c.Deprecated, renamedReadOnly)
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
	// GCAL_REQUIRE_PROMPT refused the writes that asked the person when
	// the client could not ask. The question is gone (§9a), and ignored,
	// the setting would quietly allow what its owner set it to stop, so
	// anything but off is refused, a value it never took included.
	if on, perr := parseBool("GCAL_REQUIRE_PROMPT", s.RequirePrompt); on || perr != nil {
		errs = append(errs, fmt.Errorf("%w: GCAL_REQUIRE_PROMPT was removed: the server no longer asks the "+
			"person, and the client's own approval decides each call. Unset it, and leave "+
			"GCAL_ENABLE_DESTRUCTIVE and GCAL_ENABLE_DECLINE_ALL off for a client that runs with nobody watching",
			ErrInvalid))
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

// readOnly reads the read-only switch under its name and under its old
// one. Either alone decides. Both set have to agree, because a run that
// took one over the other would turn on or leave off what its owner set
// the other to say.
func (s *Settings) readOnly() (bool, error) {
	on, err := parseBool("read-only", s.ReadOnly)
	if err != nil || strings.TrimSpace(s.ReadOnlyOld) == "" {
		return on, err
	}
	old, err := parseBool(renamedReadOnly.Old, s.ReadOnlyOld)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(s.ReadOnly) != "" && old != on {
		return false, fmt.Errorf("%w: %s is %t and %s, its old name, is %t: unset %s",
			ErrInvalid, renamedReadOnly.New, on, renamedReadOnly.Old, old, renamedReadOnly.Old)
	}
	return old, nil
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
