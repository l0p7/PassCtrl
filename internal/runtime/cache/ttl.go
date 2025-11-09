package cache

import (
	"time"
)

// RuleCacheTTLConfig mirrors the config.RuleCacheTTLConfig type to avoid circular dependencies.
// It holds duration strings for pass/fail/error outcomes.
type RuleCacheTTLConfig struct {
	Pass  string // Duration: "5m", "30s", etc.
	Fail  string // Duration: "30s", "1m", etc.
	Error string // Always "0s" - errors never cached
}

// GetTTL parses and returns the configured TTL for the given outcome.
// Error outcomes always return 0 (never cached).
// Returns 0 if the duration string is empty or invalid.
func (c RuleCacheTTLConfig) GetTTL(outcome string) time.Duration {
	if outcome == "error" {
		return 0
	}

	var durationStr string
	switch outcome {
	case "pass":
		durationStr = c.Pass
	case "fail":
		durationStr = c.Fail
	default:
		return 0
	}

	if durationStr == "" {
		return 0
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0
	}
	return duration
}

// RuleCacheConfig mirrors the config.RuleCacheConfig type to avoid circular dependencies.
type RuleCacheConfig struct {
	FollowCacheControl bool
	TTL                RuleCacheTTLConfig
	Strict             *bool // nil = true (default)
}

// GetTTL returns the configured TTL for the given outcome from rule config.
// Error outcomes always return 0 (never cached).
func (c RuleCacheConfig) GetTTL(outcome string) time.Duration {
	return c.TTL.GetTTL(outcome)
}

// CalculateEffectiveTTL computes the final cache TTL by applying the ceiling hierarchy.
//
// Hierarchy (highest to lowest precedence):
//  1. Error outcomes → Always 0 (hardcoded, never cached)
//  2. Backend Cache-Control (if followCacheControl=true and header present)
//  3. Rule manual TTL
//  4. Endpoint TTL ceiling
//  5. Server max TTL ceiling
//
// The effective TTL is the minimum of all applicable ceilings.
//
// Parameters:
//   - outcome: The rule outcome ("pass", "fail", or "error")
//   - serverMaxTTL: Global TTL ceiling (0 = no ceiling)
//   - endpointTTL: Endpoint-level TTL ceiling per outcome (empty strings = no ceiling)
//   - ruleConfig: Rule cache configuration
//   - backendHeaders: Response headers from backend (for Cache-Control parsing)
//
// Returns:
//   - The effective TTL (0 = don't cache)
func CalculateEffectiveTTL(
	outcome string,
	serverMaxTTL time.Duration,
	endpointTTL RuleCacheTTLConfig,
	ruleConfig RuleCacheConfig,
	backendHeaders map[string]string,
) time.Duration {
	// 1. Error outcomes never cached (highest precedence)
	if outcome == "error" {
		return 0
	}

	// Start with maximum possible duration
	effectiveTTL := time.Duration(0)

	// 2. Check backend Cache-Control if enabled
	if ruleConfig.FollowCacheControl {
		if cacheControlHeader, ok := backendHeaders["cache-control"]; ok {
			directive := ParseCacheControl(cacheControlHeader)
			if backendTTL := directive.GetTTL(); backendTTL != nil {
				effectiveTTL = *backendTTL
				// If backend says don't cache (0), respect it immediately
				if effectiveTTL == 0 {
					return 0
				}
				// Backend TTL found, now apply ceilings
				effectiveTTL = applyTTLCeilings(effectiveTTL, serverMaxTTL, endpointTTL, outcome)
				return effectiveTTL
			}
		}
	}

	// 3. Use rule manual TTL
	ruleTTL := ruleConfig.GetTTL(outcome)
	if ruleTTL == 0 {
		return 0 // Rule says don't cache
	}

	effectiveTTL = ruleTTL

	// 4. & 5. Apply endpoint and server ceilings
	effectiveTTL = applyTTLCeilings(effectiveTTL, serverMaxTTL, endpointTTL, outcome)

	return effectiveTTL
}

// applyTTLCeilings applies the endpoint and server TTL ceilings to the given TTL.
// Returns the minimum of the given TTL and all applicable ceilings.
func applyTTLCeilings(ttl time.Duration, serverMaxTTL time.Duration, endpointTTL RuleCacheTTLConfig, outcome string) time.Duration {
	result := ttl

	// Apply endpoint ceiling
	endpointCeiling := endpointTTL.GetTTL(outcome)
	if endpointCeiling > 0 {
		result = min(result, endpointCeiling)
	}

	// Apply server ceiling
	if serverMaxTTL > 0 && serverMaxTTL < result {
		result = serverMaxTTL
	}

	return result
}
