// Command google-calendar-mcp is an MCP server for Google Calendar.
//
// With no subcommand it serves MCP over stdio. Stdout carries JSON-RPC
// frames and nothing else — this file is the one place that names the
// process's streams, and it passes them down as io.Writer so nothing
// below can print to the wrong one.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/server"
	"github.com/mmedum/google-calendar-mcp/internal/service"
	"github.com/mmedum/google-calendar-mcp/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "google-calendar-mcp: %v\n", err)
		os.Exit(1)
	}
}

// run is main with its dependencies passed in, so tests drive it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env func(string) string) error {
	// A subcommand is the first argument when it is not a flag.
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		sub := args[0]
		rest := args[1:]
		switch sub {
		case "login":
			return cmdLogin(rest, stdout, stderr, env)
		case "logout":
			return cmdLogout(rest, stdout, env)
		case "status":
			return cmdStatus(rest, stdout, env)
		case "doctor":
			return cmdDoctor(rest, stdout, env)
		case "serve":
			// Explicit form of the default.
		default:
			return fmt.Errorf("unknown command %q (want login, logout, status, doctor or serve)", sub)
		}
		args = rest
	}

	fs := flag.NewFlagSet("google-calendar-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	settings := config.Define(fs, env)
	showVersion := fs.Bool("version", false, "print the version and exit")
	dumpSchemas := fs.Bool("dump-schemas", false, "print the tool schemas as JSON and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.Info())
		return err
	}

	cfg, err := settings.Build()
	if err != nil {
		return err
	}
	logger := config.NewLogger(cfg, stderr)

	if *dumpSchemas {
		// The dump is the one time stdout carries something that is not
		// a JSON-RPC frame, and it is also the one time no session
		// exists to corrupt.
		return server.DumpSchemas(context.Background(), stdout, server.Deps{
			Config: cfg, Version: version.String(),
		})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A missing or broken credential is NOT a reason to refuse to
	// start. The client launches this server and asks for tools/list
	// before anybody has logged in; exiting here makes the server
	// undiscoverable and looks like a crash in every host's log.
	svc, err := buildService(ctx, cfg, env, func(msg string) { logger.Warn(msg) })
	if err != nil {
		logger.Warn("starting without credentials; tools will report [auth] until login", "reason", err)
		svc = service.Unauthenticated(cfg, err)
	}

	srv := server.New(server.Deps{
		Service: svc, Config: cfg, Logger: logger, Version: version.String(),
	})

	logger.Info("serving", "version", version.String(), "profile", cfg.Profile,
		"read_only", cfg.ReadOnly, "sharing", cfg.Sharing, "destructive", cfg.EnableDestructive)

	// The transport is built from the streams passed in, not from
	// os.Stdin and os.Stdout directly. This function already took them
	// as parameters and then ignored them, which made the serve path —
	// the whole point of the binary — the one path no test could reach.
	transport := &mcp.IOTransport{
		Reader: readCloser(stdin),
		Writer: writeCloser(stdout),
	}
	if err := srv.Run(ctx, transport); err != nil {
		if isDisconnect(err) || errors.Is(err, context.Canceled) {
			// An ordinary disconnect, not a crash.
			logger.Info("client disconnected")
			return nil
		}
		return err
	}
	return nil
}

// readCloser and writeCloser adapt the process's streams to the
// transport, which wants closers. Closing stdin or stdout from inside
// the session is not something this server does — the host owns those —
// so the Close is a no-op where one has to be invented.
func readCloser(r io.Reader) io.ReadCloser {
	if rc, ok := r.(io.ReadCloser); ok {
		return rc
	}
	return io.NopCloser(r)
}

func writeCloser(w io.Writer) io.WriteCloser {
	if wc, ok := w.(io.WriteCloser); ok {
		return wc
	}
	return nopWriteCloser{w}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// isDisconnect reports whether err is the end of a stdio session.
//
// The trap the standard's §11 names: errors.Is(err, io.EOF) does NOT
// catch this. The SDK reports a closed connection as a JSON-RPC error
// with code -32004 (or -32003) and the EOF only as message text, so an
// unmatched error makes the process exit non-zero on an ordinary
// disconnect — which every host logs as a crash. Match the code, not
// the text.
func isDisconnect(err error) bool {
	var je *jsonrpc.Error
	if errors.As(err, &je) {
		return je.Code == -32004 || je.Code == -32003
	}
	return errors.Is(err, io.EOF)
}
