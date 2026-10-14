package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"your-music-library/internal/cache"
)

// ErrNotFound is returned for HTTP 404.
var ErrNotFound = errors.New("not found")

// HTTPOptions configure a provider's HTTP access.
type HTTPOptions struct {
	UserAgent string
	// Header is added to every request (e.g. Authorization). Never part of
	// cache keys.
	Header http.Header
	// MinInterval is the least time between two requests (rate limit).
	MinInterval time.Duration
	Cache       *cache.Store // JSON responses; optional
	Client      *http.Client // default: 30s timeout
	MaxRetries  int          // for 429 and 5xx; default 3
}

type httpClient struct {
	opts HTTPOptions
	mu   sync.Mutex
	next time.Time // earliest time for the next request
}

func newHTTP(opts HTTPOptions) *httpClient {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	return &httpClient{opts: opts}
}

// wait blocks until the rate limit allows a request.
func (c *httpClient) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(c.opts.MinInterval)
	c.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// getJSON fetches url into v, using the cache when possible.
func (c *httpClient) getJSON(ctx context.Context, url string, v any) error {
	key := "GET " + url
	if c.opts.Cache != nil {
		if body, ok := c.opts.Cache.Get(ctx, key); ok {
			return json.Unmarshal(body, v)
		}
	}
	body, _, err := c.get(ctx, url, "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decoding %s: %w", url, err)
	}
	if c.opts.Cache != nil {
		c.opts.Cache.Set(key, body)
	}
	return nil
}

// get performs a GET with rate limiting and retries on 429/5xx.
func (c *httpClient) get(ctx context.Context, url, accept string) ([]byte, string, error) {
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, "", err
		}
		for k, vs := range c.opts.Header {
			req.Header[k] = vs
		}
		req.Header.Set("User-Agent", c.opts.UserAgent)
		req.Header.Set("Accept", accept)
		resp, err := c.opts.Client.Do(req)
		if err != nil {
			return nil, "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		resp.Body.Close()
		if err != nil {
			return nil, "", err
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return body, resp.Header.Get("Content-Type"), nil
		case resp.StatusCode == http.StatusNotFound:
			return nil, "", fmt.Errorf("%s: %w", url, ErrNotFound)
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < c.opts.MaxRetries:
			delay := backoff
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				delay = time.Duration(s) * time.Second
			}
			backoff *= 2
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, "", ctx.Err()
			case <-t.C:
			}
		default:
			snippet := string(body)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, "", fmt.Errorf("%s: HTTP %d: %s", url, resp.StatusCode, snippet)
		}
	}
}

// ImageFetcher downloads cover images, caching them as files.
type ImageFetcher struct {
	http  *httpClient
	files *cache.Files
}

// NewImageFetcher returns a fetcher; files may be nil (no caching).
func NewImageFetcher(userAgent string, files *cache.Files) *ImageFetcher {
	return &ImageFetcher{http: newHTTP(HTTPOptions{UserAgent: userAgent}), files: files}
}

// Fetch returns the image at url and its MIME type.
func (f *ImageFetcher) Fetch(ctx context.Context, url string) ([]byte, string, error) {
	if f.files != nil {
		if data, ok := f.files.Get(url); ok {
			return data, http.DetectContentType(data), nil
		}
	}
	data, _, err := f.http.get(ctx, url, "image/*")
	if err != nil {
		return nil, "", err
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, "", fmt.Errorf("%s: not a JPEG or PNG image (%s)", url, mime)
	}
	if f.files != nil {
		f.files.Put(url, data)
	}
	return data, mime, nil
}
