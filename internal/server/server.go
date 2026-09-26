// Package server wires the MCP SDK to the tools and offers a schema dump
// through an in-memory client session.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/config"
	"github.com/mmedum/google-calendar-mcp/v2/internal/service"
	"github.com/mmedum/google-calendar-mcp/v2/internal/tools"
)

// Name is the MCP server name.
const Name = "google-calendar-mcp"

// SDKVersion is recorded in schema dumps, so a diff caused by an SDK
// upgrade can be told apart from a change to the tool surface.
const SDKVersion = "v1.7.0"

const instructions = "Google Calendar tools. This server answers *when*: calendars, the events on them, and who " +
	"is free. What a meeting produces — a recording, a notes document, an attachment — belongs to the Drive and " +
	"Docs servers. " +
	"Start with list_calendars: every other tool takes a calendar, the ids come from there, and it reports each " +
	"calendar's time zone, which is what times are read against. " +
	"Time is the thing to be careful about here. An all-day event is a DATE and has no time of day; it is never " +
	"midnight, and reading it as an instant puts it on the wrong day for anyone west of UTC. A timed event always " +
	"carries an IANA zone, not just an offset, because an offset cannot expand a repeating event across a " +
	"daylight-saving change. Every read states the absolute window it used, the zone it used, and where that zone " +
	"came from — check those rather than assuming, and resolve relative dates like \"next Tuesday\" yourself " +
	"before calling. " +
	"list_events with expand=true gives each occurrence of a repeating event; with no_expand it gives the series " +
	"once with its rule. Ask for what you mean: they answer different questions. " +
	"search_events is Google's undocumented free-text match with no field syntax, so an empty result means the " +
	"search found nothing, not that nothing exists — fall back to list_events when you need certainty. " +
	"Never answer \"are they free\" from a list of events: events you cannot see the details of are still busy, " +
	"and an event marked free is not. That is what check_availability is for, and it reports a calendar it could " +
	"not read as unknown rather than as free."

// Deps are what the server needs.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
	Version string
}

// New builds the MCP server with every tool the configuration allows.
func New(d Deps) *mcp.Server {
	opts := &mcp.ServerOptions{Instructions: instructions}
	// The SDK's own logger writes session chatter, never a JSON-RPC
	// frame. At info it would put two lines per session into every
	// client's log file for nothing, so it is attached only at debug.
	if d.Logger != nil && d.Logger.Enabled(context.Background(), slog.LevelDebug) {
		opts.Logger = d.Logger
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: d.Version}, opts)
	if d.Logger != nil {
		s.AddReceivingMiddleware(logCalls(d.Logger))
	}
	tools.Register(s, tools.Deps{Service: d.Service, Config: d.Config, Logger: d.Logger})
	return s
}

// logCalls records that a call happened and how it went, and nothing
// about what it carried.
//
// The fields are the method, the tool name, the outcome and the
// duration. Arguments and results are the person's calendar — titles,
// guest lists, locations, search terms — and a log somebody is asked to
// paste into a bug report has to be safe to paste by construction
// rather than by their vigilance (§9). The tool name is the one part of
// a request that comes from this server's own schema.
func logCalls(lg *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			start := time.Now()
			res, err := next(ctx, method, req)
			attrs := []any{"method", method, "ms", time.Since(start).Milliseconds()}
			if ctr, ok := req.(*mcp.CallToolRequest); ok && ctr.Params != nil {
				attrs = append(attrs, "tool", ctr.Params.Name)
			}
			if err != nil {
				// The error text is this server's own message, built
				// from a class and a fixed sentence. It is not the
				// caller's payload.
				lg.Debug("mcp call failed", append(attrs, "outcome", "error")...)
			} else {
				lg.Debug("mcp call", append(attrs, "outcome", "ok")...)
			}
			return res, err
		}
	}
}

// SchemaDump is the stable description of the tool surface, for the
// schema diff.
type SchemaDump struct {
	Server     string       `json:"server"`
	SDKVersion string       `json:"sdk_version"`
	Tools      []SchemaTool `json:"tools"`
	// Resources are part of the surface a client sees, so they are part
	// of what a diff has to notice. A resource removed is as breaking as
	// a tool removed, and nothing else would have reported it.
	Resources []SchemaResource `json:"resources,omitempty"`
}

// SchemaResource is one resource or resource template in a dump.
type SchemaResource struct {
	Name string `json:"name"`
	// URI is set on a fixed resource, URITemplate on a templated one.
	// Exactly one of them, which is what tells the two apart in a diff.
	URI         string `json:"uri,omitempty"`
	URITemplate string `json:"uri_template,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Description string `json:"description,omitempty"`
}

// SchemaTool is one tool in a dump.
type SchemaTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// DumpSchemas lists every tool under the full surface.
//
// It connects a real in-memory client rather than reading a registry,
// so the dump is what a client would actually be told — including
// anything the SDK adds or renames on the way out.
func DumpSchemas(ctx context.Context, w io.Writer, d Deps) error {
	d.Config = tools.FullSurface(d.Config)
	d.Logger = slog.New(slog.DiscardHandler)
	srv := New(d)

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return fmt.Errorf("server: connect: %w", err)
	}
	defer func() { _ = ss.Wait() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "schema-dump", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return fmt.Errorf("server: client connect: %w", err)
	}
	defer func() { _ = cs.Close() }()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("server: list tools: %w", err)
	}

	dump := SchemaDump{Server: Name, SDKVersion: SDKVersion}
	for _, t := range res.Tools {
		st := SchemaTool{Name: t.Name, Description: t.Description}
		if t.InputSchema != nil {
			if b, err := json.Marshal(t.InputSchema); err == nil {
				st.InputSchema = b
			}
		}
		if t.Annotations != nil {
			if b, err := json.Marshal(t.Annotations); err == nil {
				st.Annotations = b
			}
		}
		dump.Tools = append(dump.Tools, st)
	}
	sort.Slice(dump.Tools, func(i, j int) bool { return dump.Tools[i].Name < dump.Tools[j].Name })

	rs, err := cs.ListResources(ctx, nil)
	if err != nil {
		return fmt.Errorf("server: list resources: %w", err)
	}
	for _, r := range rs.Resources {
		dump.Resources = append(dump.Resources, SchemaResource{
			Name: r.Name, URI: r.URI, MIMEType: r.MIMEType, Description: r.Description,
		})
	}
	tmpl, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		return fmt.Errorf("server: list resource templates: %w", err)
	}
	for _, r := range tmpl.ResourceTemplates {
		dump.Resources = append(dump.Resources, SchemaResource{
			Name: r.Name, URITemplate: r.URITemplate, MIMEType: r.MIMEType, Description: r.Description,
		})
	}
	sort.Slice(dump.Resources, func(i, j int) bool { return dump.Resources[i].key() < dump.Resources[j].key() })

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(dump)
}

// key is how a resource is identified in a diff: its URI, or its
// template when it has one.
func (r SchemaResource) key() string {
	if r.URI != "" {
		return r.URI
	}
	return r.URITemplate
}
