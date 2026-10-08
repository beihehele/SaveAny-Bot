package parser

import (
	"context"
	"crypto/md5"
	"fmt"
	"maps"
	"slices"
)

type Parser interface {
	CanHandle(url string) bool
	Parse(ctx context.Context, url string) (*Item, error)
}

// ContextualParser optionally makes URL matching cancellable while preserving
// the Parser interface implemented by existing native parsers and plugins.
type ContextualParser interface {
	Parser
	CanHandleContext(ctx context.Context, url string) bool
}

// CanHandleWithContext uses cancellable matching when the parser supports it.
func CanHandleWithContext(ctx context.Context, p Parser, url string) bool {
	if ctx.Err() != nil {
		return false
	}
	var matched bool
	if contextual, ok := p.(ContextualParser); ok {
		matched = contextual.CanHandleContext(ctx, url)
	} else {
		matched = p.CanHandle(url)
	}
	return matched && ctx.Err() == nil
}

type ConfigurableParser interface {
	Parser
	Configure(config map[string]any) error
	Name() string
}

// Resource is a single downloadable resource with metadata.
type Resource struct {
	URL       string            `json:"url"`
	Filename  string            `json:"filename"` // with ext
	MimeType  string            `json:"mime_type"`
	Extension string            `json:"extension"` // e.g. "mp4"
	Size      int64             `json:"size"`      // 0 when unknown
	Hash      map[string]string `json:"hash"`      // {"md5": "...", "sha256": "..."}
	Headers   map[string]string `json:"headers"`   // HTTP headers when downloading
	Extra     map[string]any    `json:"extra"`
}

// Item represents a parsed item with metadata and resources.
type Item struct {
	Site        string         `json:"site"`
	URL         string         `json:"url"` // original URL of the item
	Title       string         `json:"title"`
	Author      string         `json:"author"`
	Description string         `json:"description"`
	Tags        []string       `json:"tags"`
	Resources   []Resource     `json:"resources"`
	Extra       map[string]any `json:"extra"`
}

func (r *Resource) FileName() string {
	return r.Filename
}

func (r *Resource) FileSize() int64 {
	return r.Size
}

func (r *Resource) ID() string {
	h := md5.New()
	h.Write([]byte(r.URL))
	h.Write([]byte(r.Filename))
	h.Write([]byte(r.MimeType))
	h.Write([]byte(r.Extension))
	fmt.Fprintf(h, "%d", r.Size)

	for _, k := range slices.Sorted(maps.Keys(r.Hash)) {
		h.Write([]byte(k))
		h.Write([]byte(r.Hash[k]))
	}

	for _, k := range slices.Sorted(maps.Keys(r.Headers)) {
		h.Write([]byte(k))
		h.Write([]byte(r.Headers[k]))
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}
