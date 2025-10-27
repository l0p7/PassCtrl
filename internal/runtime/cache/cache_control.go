package cache

import (
	"strconv"
	"strings"
	"time"
)

// CacheControlDirective represents parsed Cache-Control header directives
// from a backend response. Used to determine TTL when followCacheControl is enabled.
type CacheControlDirective struct {
	MaxAge  *int // max-age directive value in seconds
	SMaxAge *int // s-maxage directive value in seconds (shared cache preference)
	NoCache bool // no-cache directive present
	NoStore bool // no-store directive present
	Private bool // private directive present
}

// ParseCacheControl parses a Cache-Control header string and returns
// the relevant directives for caching decisions.
//
// Format: Cache-Control: directive1, directive2=value, directive3
//
// Supported directives:
//   - max-age=<seconds>
//   - s-maxage=<seconds>
//   - no-cache
//   - no-store
//   - private
//
// Unknown directives are silently ignored.
func ParseCacheControl(header string) CacheControlDirective {
	directive := CacheControlDirective{}

	if header == "" {
		return directive
	}

	// Split by comma and process each directive
	parts := strings.Split(header, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Check for key=value directives
		if strings.Contains(part, "=") {
			kv := strings.SplitN(part, "=", 2)
			key := strings.TrimSpace(strings.ToLower(kv[0]))
			value := strings.TrimSpace(kv[1])

			switch key {
			case "max-age":
				if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
					directive.MaxAge = &seconds
				}
			case "s-maxage":
				if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
					directive.SMaxAge = &seconds
				}
			}
		} else {
			// Boolean directives
			key := strings.ToLower(part)
			switch key {
			case "no-cache":
				directive.NoCache = true
			case "no-store":
				directive.NoStore = true
			case "private":
				directive.Private = true
			}
		}
	}

	return directive
}

// GetTTL derives the cache TTL from the directive according to precedence rules.
//
// Precedence (highest to lowest):
//  1. Don't cache directives (no-cache, no-store, private) → 0 seconds
//  2. s-maxage (shared cache directive)
//  3. max-age
//  4. No directive → nil (fall back to manual TTL)
//
// Returns nil if no caching directive is present, allowing the caller
// to fall back to configured TTL values.
func (d CacheControlDirective) GetTTL() *time.Duration {
	// 1. Don't cache directives take precedence
	if d.NoCache || d.NoStore || d.Private {
		zero := time.Duration(0)
		return &zero
	}

	// 2. s-maxage (shared cache preference)
	if d.SMaxAge != nil {
		ttl := time.Duration(*d.SMaxAge) * time.Second
		return &ttl
	}

	// 3. max-age
	if d.MaxAge != nil {
		ttl := time.Duration(*d.MaxAge) * time.Second
		return &ttl
	}

	// 4. No directive - return nil to signal fallback
	return nil
}
