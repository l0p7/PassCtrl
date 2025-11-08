package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

// RuleCacheEntry represents a cached rule execution result.
// Only the winning outcome's variables and headers are cached.
type RuleCacheEntry struct {
	Outcome   string            `json:"outcome"`   // "pass" or "fail" (never "error")
	Reason    string            `json:"reason"`    // Rule outcome reason
	Exported  map[string]any    `json:"exported"`  // Exported variables from winning outcome
	Headers   map[string]string `json:"headers"`   // Custom response headers
	StoredAt  time.Time         `json:"storedAt"`  // When the entry was stored
	ExpiresAt time.Time         `json:"expiresAt"` // When the entry expires
}

// buildRuleCacheKey constructs the cache key for a rule execution.
//
// Format: credential | endpoint | path | rule-name | backend-hash | upstream-vars-hash
//
// Components:
//   - credential: From state.CacheKey() (already includes credential, endpoint, path)
//   - rule-name: Name of the rule being cached
//   - backend-hash: Hash of the rendered backend request (method, URL, headers, body)
//   - upstream-vars-hash: Hash of all upstream exported variables (only if strict mode)
//
// The base cache key (credential|endpoint|path) is already computed and stored in state.
func buildRuleCacheKey(
	baseKey string,
	ruleName string,
	backendHash string,
	upstreamHash string,
) string {
	parts := []string{
		baseKey,
		ruleName,
		backendHash,
	}

	// Add upstream vars hash only if present (strict mode)
	if upstreamHash != "" {
		parts = append(parts, upstreamHash)
	}

	return strings.Join(parts, "|")
}

// buildBackendHash computes the hash of a rendered backend request for cache key generation.
// The descriptor should contain the fully-rendered request components (URL, headers, body).
// Session-specific headers (correlation, tracing, proxy, timing) are excluded from the hash
// to enable caching across requests with different session metadata.
// Returns empty string if descriptor has no URL (indicating no backend configured).
func buildBackendHash(descriptor cache.BackendDescriptor, correlationHeader string, includeProxyHeaders *bool) string {
	if descriptor.URL == "" {
		return ""
	}

	// Determine if proxy headers should be included in cache key
	// Default (nil) = true (safe: include proxy headers in cache key)
	// Explicit false = exclude proxy headers (opt-out for better cache hit rates)
	shouldIncludeProxyHeaders := true
	if includeProxyHeaders != nil {
		shouldIncludeProxyHeaders = *includeProxyHeaders
	}

	// Build list of headers to exclude from cache key
	// These are session-specific headers that should not affect cache decisions
	excludeHeaders := []string{}

	// Always exclude distributed tracing and timing headers
	excludeHeaders = append(excludeHeaders,
		// Distributed tracing headers (W3C Trace Context, Zipkin B3, cloud providers)
		// These track request propagation through distributed systems
		"traceparent",
		"tracestate",
		"x-b3-traceid",
		"x-b3-spanid",
		"x-b3-parentspanid",
		"x-b3-sampled",
		"x-b3-flags",
		"x-cloud-trace-context",
		"x-amzn-trace-id",
		"uber-trace-id",

		// Request timing and telemetry headers
		// These contain request start times and performance metrics
		"x-request-start",
		"x-timer",
	)

	// Conditionally exclude proxy headers based on configuration
	// SECURITY WARNING: Excluding proxy headers can cause cache correctness issues
	// if backends use client IP, geo-location, or proxy metadata for decision-making.
	// Only set includeProxyHeaders=false if you are certain backends don't rely on these.
	if !shouldIncludeProxyHeaders {
		excludeHeaders = append(excludeHeaders,
			// Forward proxy headers (RFC 7239 and X-Forwarded-* conventions)
			// These contain client IP, proxy chain, and routing metadata unique per request
			"forwarded",
			"x-forwarded-for",
			"x-forwarded-host",
			"x-forwarded-proto",
			"x-forwarded-port",
			"x-forwarded-prefix",
			"x-real-ip",
			"x-original-forwarded-for",
			"true-client-ip",

			// CDN and edge metadata
			// These contain CDN-specific routing and identification data
			"cf-ray",
			"cf-connecting-ip",
			"cf-ipcountry",
			"cf-visitor",
		)
	}

	// Add correlation header if configured
	// This allows custom correlation headers beyond the common patterns
	if correlationHeader != "" {
		excludeHeaders = append(excludeHeaders, correlationHeader)
	}

	return descriptor.Hash(excludeHeaders...)
}

// buildUpstreamVarsHash computes the hash of all upstream exported variables.
// Returns empty string if strict mode is disabled.
func buildUpstreamVarsHash(strict bool, state *pipeline.State) string {
	if !strict {
		return ""
	}

	// Get all upstream exported variables from state.Variables.Rules
	if len(state.Variables.Rules) == 0 {
		return ""
	}

	return cache.HashUpstreamVariables(state.Variables.Rules)
}

// lookupRuleCache attempts to retrieve a cached rule execution result.
// Returns the entry and true if found, or nil and false if not found or on error.
//
// NOTE: The current cache.Entry type is designed for endpoint-level caching.
// For per-rule caching, we encode the RuleCacheEntry as JSON in the Decision field.
func lookupRuleCache(
	ctx context.Context,
	cacheBackend cache.DecisionCache,
	cacheKey string,
) (*RuleCacheEntry, bool) {
	if cacheBackend == nil || cacheKey == "" {
		return nil, false
	}

	entry, ok, err := cacheBackend.Lookup(ctx, cacheKey)
	if err != nil || !ok {
		return nil, false
	}

	// Deserialize the cached entry from the Decision field
	var ruleEntry RuleCacheEntry
	if err := json.Unmarshal([]byte(entry.Decision), &ruleEntry); err != nil {
		return nil, false
	}

	// Check expiration
	if time.Now().After(ruleEntry.ExpiresAt) {
		return nil, false
	}

	return &ruleEntry, true
}

// storeRuleCache stores a rule execution result in the cache.
func storeRuleCache(
	ctx context.Context,
	cacheBackend cache.DecisionCache,
	cacheKey string,
	outcome string,
	reason string,
	exported map[string]any,
	headers map[string]string,
	ttl time.Duration,
) error {
	if cacheBackend == nil || cacheKey == "" || ttl <= 0 {
		return nil // Don't cache
	}

	// Only cache pass/fail outcomes (never error)
	if outcome != "pass" && outcome != "fail" {
		return nil
	}

	now := time.Now()
	ruleEntry := RuleCacheEntry{
		Outcome:   outcome,
		Reason:    reason,
		Exported:  exported,
		Headers:   headers,
		StoredAt:  now,
		ExpiresAt: now.Add(ttl),
	}

	// Serialize the entry as JSON
	data, err := json.Marshal(ruleEntry)
	if err != nil {
		return fmt.Errorf("serialize rule cache entry: %w", err)
	}

	// Store in cache using the existing Entry structure
	// We encode the RuleCacheEntry as JSON in the Decision field
	cacheEntry := cache.Entry{
		Decision:  string(data),
		Response:  cache.Response{}, // Empty for rule caching
		StoredAt:  ruleEntry.StoredAt,
		ExpiresAt: ruleEntry.ExpiresAt,
	}

	return cacheBackend.Store(ctx, cacheKey, cacheEntry)
}

// restoreFromCache applies a cached rule result to the state.
func restoreFromCache(entry *RuleCacheEntry, ruleName string, state *pipeline.State) {
	if entry == nil || state == nil {
		return
	}

	// Restore exported variables to state
	if state.Variables.Rules == nil {
		state.Variables.Rules = make(map[string]map[string]any)
	}
	if len(entry.Exported) > 0 {
		state.Variables.Rules[ruleName] = entry.Exported
		state.Rule.Variables.Exported = entry.Exported
	}

	// Restore response headers
	if state.Response.Headers == nil {
		state.Response.Headers = make(map[string]string)
	}
	for k, v := range entry.Headers {
		state.Response.Headers[k] = v
	}
}
