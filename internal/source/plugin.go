package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Timeouts per command; downloads of hi-res albums can take long.
var (
	TimeoutQuery    = time.Minute
	TimeoutDownload = 30 * time.Minute
)

// Plugin is one source executable.
type Plugin struct {
	Name string
	Path string
}

// Discover finds plugins in dir: executable regular files (or symlinks to
// them), named by their file name without extension. A missing dir means
// no plugins.
func Discover(dir string) ([]*Plugin, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Plugin
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || e.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name())) // follows symlinks
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		out = append(out, &Plugin{Name: name, Path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Info asks the plugin to describe itself and checks the protocol version.
func (p *Plugin) Info(ctx context.Context) (Info, error) {
	var info Info
	if err := p.call(ctx, "info", struct{}{}, &info, TimeoutQuery); err != nil {
		return Info{}, err
	}
	if info.Protocol != ProtocolVersion {
		return info, fmt.Errorf("%s speaks protocol %d, ymlib needs %d", p.Name, info.Protocol, ProtocolVersion)
	}
	return info, nil
}

// Search returns items matching query.
func (p *Plugin) Search(ctx context.Context, query string, kind Kind, limit int) ([]Item, error) {
	var resp struct {
		Results []Item `json:"results"`
	}
	req := map[string]any{"query": query, "kind": kind, "limit": limit}
	if err := p.call(ctx, "search", req, &resp, TimeoutQuery); err != nil {
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
	if err := p.call(ctx, "resolve", map[string]any{"id": id, "kind": kind}, &resp, TimeoutQuery); err != nil {
		return Item{}, nil, err
	}
	if kind == KindTrack && len(resp.Tracks) == 0 {
		resp.Tracks = []Item{resp.Item}
	}
	return resp.Item, resp.Tracks, nil
}

// Download asks the plugin to save track id into destDir and returns the
// file's absolute path. The file must be inside destDir.
func (p *Plugin) Download(ctx context.Context, id, destDir string) (string, error) {
	var resp struct {
		File string `json:"file"`
	}
	if err := p.call(ctx, "download", map[string]any{"id": id, "dest_dir": destDir}, &resp, TimeoutDownload); err != nil {
		return "", err
	}
	if resp.File == "" {
		return "", fmt.Errorf("%s: download returned no file", p.Name)
	}
	path := resp.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(destDir, path)
	}
	path = filepath.Clean(path)
	if rel, err := filepath.Rel(destDir, path); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s: downloaded file %q is outside %s", p.Name, resp.File, destDir)
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%s: downloaded file missing: %w", p.Name, err)
	}
	return path, nil
}

// call runs one plugin command.
func (p *Plugin) call(ctx context.Context, command string, req, resp any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	in, err := json.Marshal(req)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.Path, command)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, n: 10 << 20}
	cmd.Stderr = &limitWriter{w: &stderr, n: 64 << 10}
	runErr := cmd.Run()

	var envelope struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(stdout.Bytes(), &envelope)
	switch {
	case envelope.Error != "":
		return fmt.Errorf("%s %s: %s", p.Name, command, envelope.Error)
	case ctx.Err() != nil:
		return fmt.Errorf("%s %s: %w", p.Name, command, ctx.Err())
	case runErr != nil:
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return fmt.Errorf("%s %s: %s", p.Name, command, msg)
	}
	if err := json.Unmarshal(stdout.Bytes(), resp); err != nil {
		return fmt.Errorf("%s %s: invalid JSON response: %w", p.Name, command, err)
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// limitWriter keeps the first n bytes and discards the rest.
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k]) // a bytes.Buffer never fails
		l.n -= k
	}
	return len(p), nil
}

// Registry is the set of available plugins.
type Registry struct{ plugins map[string]*Plugin }

// NewRegistry indexes plugins by name.
func NewRegistry(plugins []*Plugin) *Registry {
	r := &Registry{plugins: map[string]*Plugin{}}
	for _, p := range plugins {
		r.plugins[p.Name] = p
	}
	return r
}

// Get returns the plugin called name.
func (r *Registry) Get(name string) (*Plugin, error) {
	if p, ok := r.plugins[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("no source plugin %q (see `ymlib sources`)", name)
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
