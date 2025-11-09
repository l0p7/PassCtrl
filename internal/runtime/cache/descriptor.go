package cache

import (
	"encoding/json"
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
// - Session-specific headers (correlation, tracing) are excluded from the hash
// - Format: method|url|header1:value1|header2:value2|body
//
// Parameters:
// - excludeHeaders: list of header names (case-insensitive) to exclude from hash
func (d BackendDescriptor) Hash(excludeHeaders ...string) string {
	h := fnv.New64a()

	// Build exclusion map for O(1) lookup
	exclude := make(map[string]bool, len(excludeHeaders))
	for _, name := range excludeHeaders {
		exclude[strings.ToLower(name)] = true
	}

	// Write method
	_, _ = h.Write([]byte(d.Method))
	_, _ = h.Write([]byte("|"))

	// Write URL
	_, _ = h.Write([]byte(d.URL))
	_, _ = h.Write([]byte("|"))

	// Write headers in sorted order for determinism, excluding session-specific headers
	if len(d.Headers) > 0 {
		keys := make([]string, 0, len(d.Headers))
		for k := range d.Headers {
			// Skip excluded headers (correlation, tracing, etc.)
			if exclude[strings.ToLower(k)] {
				continue
			}
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

// HashUpstreamVariables computes a deterministic hash of all upstream exported variables
// using FNV-1a. This hash is used in strict cache mode to invalidate caches when upstream
// rule variables change.
//
// The hash is computed from a canonical representation:
// - Rule names are sorted alphabetically
// - Variable names within each rule are sorted alphabetically
// - Format: ruleName.varName=value|ruleName.varName=value|...
//
// Returns a hex-encoded hash string suitable for use in cache keys.
// Returns empty string if upstreamVars is nil or empty.
// HashUpstreamVariables computes a deterministic hash of all upstream exported variables
// using FNV-1a. This hash is used in strict cache mode to invalidate caches when upstream
// rule variables change.
//
// The hash is computed from a canonical representation:
// - Rule names are sorted alphabetically
// - Variable names within each rule are sorted alphabetically
// - Variable values are JSON-encoded for deterministic representation (handles nested maps/slices)
// - Format: ruleName.varName=jsonValue|ruleName.varName=jsonValue|...
//
// Returns a hex-encoded hash string suitable for use in cache keys.
// Returns empty string if upstreamVars is nil or empty.
func HashUpstreamVariables(upstreamVars map[string]map[string]any) string {
	if len(upstreamVars) == 0 {
		return ""
	}

	h := fnv.New64a()

	// Sort rule names for determinism
	ruleNames := make([]string, 0, len(upstreamVars))
	for ruleName := range upstreamVars {
		ruleNames = append(ruleNames, ruleName)
	}
	sort.Strings(ruleNames)

	// Build canonical string: ruleName.varName=value|
	for _, ruleName := range ruleNames {
		vars := upstreamVars[ruleName]

		// Sort variable names for determinism
		varNames := make([]string, 0, len(vars))
		for varName := range vars {
			varNames = append(varNames, varName)
		}
		sort.Strings(varNames)

		// Write each variable in sorted order
		for _, varName := range varNames {
			_, _ = h.Write([]byte(ruleName))
			_, _ = h.Write([]byte("."))
			_, _ = h.Write([]byte(varName))
			_, _ = h.Write([]byte("="))

			// Use JSON encoding for deterministic representation of complex types
			// json.Marshal produces sorted keys for maps, ensuring determinism
			valueBytes, err := json.Marshal(vars[varName])
			if err != nil {
				// Fallback to empty string if marshaling fails
				// This shouldn't happen for typical variable types
				_, _ = h.Write([]byte(""))
			} else {
				_, _ = h.Write(valueBytes)
			}

			_, _ = h.Write([]byte("|"))
		}
	}

	return fmt.Sprintf("%016x", h.Sum64())
}
