package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/config"
)

// Timeouts per request; downloads of hi-res albums can take long.
var (
	TimeoutHealth   = 2 * time.Second
	TimeoutQuery    = time.Minute
	TimeoutDownload = 30 * time.Minute
)

// Error codes a plugin answers with.
const (
	CodeNotFound  = "not_found"
	CodeTemporary = "temporary"
	CodePermanent = "permanent"
)

// Error is a failure reported by a plugin.
type Error struct {
	Plugin  string
	Op      string // health, search, resolve, download
	Code    string // CodeNotFound, CodeTemporary or CodePermanent
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s %s: %s", e.Plugin, e.Op, e.Message) }

// IsPermanent reports whether retrying err cannot help: the plugin said
// not_found or permanent.
func IsPermanent(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == CodeNotFound || e.Code == CodePermanent)
}

// ErrOffline is wrapped by errors from a plugin that could not be reached.
var ErrOffline = errors.New("offline")

// IsOffline reports whether err means the plugin's server did not answer.
func IsOffline(err error) bool { return errors.Is(err, ErrOffline) }

// Plugin is a registered plugin server.
type Plugin struct {
	Name    string
	URL     string // base URL, e.g. http://127.0.0.1:8765
	Enabled bool
	// Client sends the requests; nil means a default client. Timeouts
	// come from the request contexts.
	Client *http.Client
}

func (p *Plugin) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

// Health asks the plugin who it is and checks the protocol version.
func (p *Plugin) Health(ctx context.Context) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, TimeoutHealth)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("health"), nil)
	if err != nil {
		return Health{}, err
	}
	var h Health
	if err := p.do(req, "health", &h); err != nil {
		return Health{}, err
	}
	if h.Protocol != ProtocolVersion {
		return h, &Error{Plugin: p.Name, Op: "health", Code: CodePermanent,
			Message: fmt.Sprintf("speaks protocol %d, ymlib supports %d", h.Protocol, ProtocolVersion)}
	}
	return h, nil
}

// Search returns items matching query.
func (p *Plugin) Search(ctx context.Context, query string, kind Kind, limit int) ([]Item, error) {
	var resp struct {
		Results []Item `json:"results"`
	}
	req := map[string]any{"query": query, "kind": kind, "limit": limit}
	if err := p.post(ctx, "search", req, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Results {
		if resp.Results[i].Kind == "" {
			resp.Results[i].Kind = kind
		}
	}
	return resp.Results, nil
}

// Resolve returns an item and, for albums, its tracks.
func (p *Plugin) Resolve(ctx context.Context, id string, kind Kind) (Item, []Item, error) {
	var resp struct {
		Item   Item   `json:"item"`
		Tracks []Item `json:"tracks"`
	}
	if err := p.post(ctx, "resolve", map[string]any{"id": id, "kind": kind}, &resp); err != nil {
		return Item{}, nil, err
	}
	if kind == KindTrack && len(resp.Tracks) == 0 {
		resp.Tracks = []Item{resp.Item}
	}
	return resp.Item, resp.Tracks, nil
}

// Download streams track id into destDir and returns the file's path. The
// name comes from Content-Disposition (base name only) or from the id and
// Content-Type. An existing file is never overwritten.
func (p *Plugin) Download(ctx context.Context, id, destDir string) (_ string, err error) {
	ctx, cancel := context.WithTimeout(ctx, TimeoutDownload)
	defer cancel()
	body, err := json.Marshal(map[string]any{"id": id})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("download"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.send(req, "download")
	if err != nil {
		return "", err
	}
	defer cleanup.Close(&err, resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", p.errorFrom(resp, "download")
	}

	name := fileName(resp.Header, id)
	tmp, err := os.CreateTemp(destDir, ".download-*")
	if err != nil {
		return "", err
	}
	n, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", errors.Join(&Error{Plugin: p.Name, Op: "download", Code: CodeTemporary, Message: err.Error()}, cleanup.Remove(tmp.Name()))
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", errors.Join(&Error{Plugin: p.Name, Op: "download", Code: CodeTemporary,
			Message: fmt.Sprintf("got %d of %d bytes", n, resp.ContentLength)}, cleanup.Remove(tmp.Name()))
	}
	path := filepath.Join(destDir, name)
	if _, err := os.Lstat(path); err == nil {
		return "", errors.Join(fmt.Errorf("%s download: %s already exists", p.Name, path), cleanup.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", errors.Join(err, cleanup.Remove(tmp.Name()))
	}
	return path, nil
}

// fileName picks a safe file name for a download.
func fileName(h http.Header, id string) string {
	if _, params, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil {
		if name := safeName(filepath.Base(filepath.FromSlash(strings.ReplaceAll(params["filename"], `\`, "/")))); name != "" {
			return name
		}
	}
	name := safeName(id)
	if name == "" {
		name = "download"
	}
	return name + extension(h.Get("Content-Type"))
}

// safeName keeps a plain file name: no separators, no leading dots, no
// control characters.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, s)
	return strings.TrimLeft(strings.TrimSpace(s), ".")
}

func extension(contentType string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch mt {
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	}
	if !strings.HasPrefix(mt, "audio/") {
		return "" // e.g. text/plain sniffed by the server: no useful extension
	}
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func (p *Plugin) endpoint(path string) string { return strings.TrimRight(p.URL, "/") + "/" + path }

// post sends a JSON request to path and decodes the JSON answer.
func (p *Plugin) post(ctx context.Context, op string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, TimeoutQuery)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(op), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.do(req, op, out)
}

// do sends req and decodes a 2xx JSON answer into out.
func (p *Plugin) do(req *http.Request, op string, out any) (err error) {
	resp, err := p.send(req, op)
	if err != nil {
		return err
	}
	defer cleanup.Close(&err, resp.Body)
	if resp.StatusCode/100 != 2 {
		return p.errorFrom(resp, op)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(out); err != nil {
		return &Error{Plugin: p.Name, Op: op, Code: CodeTemporary, Message: "invalid JSON answer: " + err.Error()}
	}
	return nil
}

// send performs the request. Failing to get an answer means the plugin
// is offline, unless the caller gave up.
func (p *Plugin) send(req *http.Request, op string) (*http.Response, error) {
	resp, err := p.client().Do(req)
	if err == nil {
		return resp, nil
	}
	if ctxErr := req.Context().Err(); ctxErr != nil && !errors.Is(ctxErr, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%s %s: %w", p.Name, op, ctxErr)
	}
	return nil, fmt.Errorf("%s %s: %w at %s (%v)", p.Name, op, ErrOffline, p.URL, unwrapURLError(err))
}

func unwrapURLError(err error) error {
	if u := errors.Unwrap(err); u != nil {
		return u
	}
	return err
}

// errorFrom reads a plugin's error answer.
func (p *Plugin) errorFrom(resp *http.Response, op string) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // a cut-off body still gives the status
	e := &Error{Plugin: p.Name, Op: op, Code: CodeTemporary}
	if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
		e.Message = envelope.Error.Message
		switch envelope.Error.Code {
		case CodeNotFound, CodePermanent:
			e.Code = envelope.Error.Code
		}
		return e
	}
	e.Message = resp.Status
	if text := strings.TrimSpace(string(data)); text != "" && len(text) < 200 {
		e.Message += ": " + text
	}
	return e
}

// Registry is the set of registered plugins.
type Registry struct{ plugins map[string]*Plugin }

// NewRegistry indexes plugins by name.
func NewRegistry(plugins []*Plugin) *Registry {
	r := &Registry{plugins: map[string]*Plugin{}}
	for _, p := range plugins {
		r.plugins[p.Name] = p
	}
	return r
}

// FromConfig builds the registry of the plugins registered in config.
func FromConfig(c config.PluginsConfig) *Registry {
	plugins := make([]*Plugin, 0, len(c.Registered))
	for name, e := range c.Registered {
		plugins = append(plugins, &Plugin{Name: name, URL: e.URL, Enabled: e.Enabled})
	}
	return NewRegistry(plugins)
}

// Get returns the plugin called name.
func (r *Registry) Get(name string) (*Plugin, error) {
	if p, ok := r.plugins[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("no plugin %q is registered (see `ymlib plugin list`)", name)
}

// All returns plugins sorted by name.
func (r *Registry) All() []*Plugin {
	out := make([]*Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Enabled returns the enabled plugins sorted by name.
func (r *Registry) Enabled() []*Plugin {
	var out []*Plugin
	for _, p := range r.All() {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// Status is the result of a health check.
type Status struct {
	Plugin *Plugin
	Health Health
	Err    error // nil: online and compatible
}

// Online reports whether the plugin answered and is compatible.
func (s Status) Online() bool { return s.Err == nil }

// Check asks every plugin in ps for its health, in parallel, and returns
// the results in the same order.
func Check(ctx context.Context, ps []*Plugin) []Status {
	out := make([]Status, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			h, err := p.Health(ctx)
			out[i] = Status{Plugin: p, Health: h, Err: err}
		})
	}
	wg.Wait()
	return out
}
