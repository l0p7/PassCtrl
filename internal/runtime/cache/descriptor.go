package cache

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// BackendDescriptor represents a canonical backend request used for cache key generation.
// It includes all components that affect the backend response: method, URL, headers, and body.
type BackendDescriptor struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// Hash computes a deterministic hash of the backend descriptor using FNV-1a.
// Returns a hex-encoded hash string suitable for use in cache keys.
//
// The hash is computed from a canonical representation:
// - Headers are sorted by key for determinism
// - Format: method|url|header1:value1|header2:value2|body
func (d BackendDescriptor) Hash() string {
	h := fnv.New64a()

	// Write method
	_, _ = h.Write([]byte(d.Method))
	_, _ = h.Write([]byte("|"))

	// Write URL
	_, _ = h.Write([]byte(d.URL))
	_, _ = h.Write([]byte("|"))

	// Write headers in sorted order for determinism
	if len(d.Headers) > 0 {
		keys := make([]string, 0, len(d.Headers))
		for k := range d.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var parts []string
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s:%s", k, d.Headers[k]))
		}
		_, _ = h.Write([]byte(strings.Join(parts, "|")))
	}
	_, _ = h.Write([]byte("|"))

	// Write body
	_, _ = h.Write([]byte(d.Body))

	return fmt.Sprintf("%016x", h.Sum64())
}
