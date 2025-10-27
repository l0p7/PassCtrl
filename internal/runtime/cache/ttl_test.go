package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuleCacheTTLConfig_GetTTL(t *testing.T) {
	config := RuleCacheTTLConfig{
		Pass:  "5m",
		Fail:  "30s",
		Error: "10m", // Should be ignored
	}

	require.Equal(t, 5*time.Minute, config.GetTTL("pass"))
	require.Equal(t, 30*time.Second, config.GetTTL("fail"))
	require.Equal(t, time.Duration(0), config.GetTTL("error"), "Error should always return 0")
}

func TestRuleCacheTTLConfig_GetTTL_Empty(t *testing.T) {
	config := RuleCacheTTLConfig{}

	require.Equal(t, time.Duration(0), config.GetTTL("pass"))
	require.Equal(t, time.Duration(0), config.GetTTL("fail"))
	require.Equal(t, time.Duration(0), config.GetTTL("error"))
}

func TestRuleCacheTTLConfig_GetTTL_Invalid(t *testing.T) {
	config := RuleCacheTTLConfig{
		Pass: "invalid",
		Fail: "not-a-duration",
	}

	require.Equal(t, time.Duration(0), config.GetTTL("pass"))
	require.Equal(t, time.Duration(0), config.GetTTL("fail"))
}

func TestCalculateEffectiveTTL_ErrorOutcome(t *testing.T) {
	// Error outcomes always return 0, regardless of config
	ttl := CalculateEffectiveTTL(
		"error",
		time.Hour,
		RuleCacheTTLConfig{Error: "10m"},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Error: "10m"},
		},
		map[string]string{"cache-control": "max-age=600"},
	)

	require.Equal(t, time.Duration(0), ttl, "Error outcomes never cached")
}

func TestCalculateEffectiveTTL_RuleManualTTL(t *testing.T) {
	// No Cache-Control, use rule manual TTL
	ttl := CalculateEffectiveTTL(
		"pass",
		0,                    // No server ceiling
		RuleCacheTTLConfig{}, // No endpoint ceiling
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "5m"},
		},
		nil,
	)

	require.Equal(t, 5*time.Minute, ttl)
}

func TestCalculateEffectiveTTL_BackendCacheControl_Respected(t *testing.T) {
	// Backend Cache-Control takes precedence when followCacheControl=true
	ttl := CalculateEffectiveTTL(
		"pass",
		0,
		RuleCacheTTLConfig{},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "10m"}, // Should be ignored
		},
		map[string]string{"cache-control": "max-age=300"},
	)

	require.Equal(t, 300*time.Second, ttl, "Backend Cache-Control should take precedence")
}

func TestCalculateEffectiveTTL_BackendCacheControl_Ignored(t *testing.T) {
	// Backend Cache-Control ignored when followCacheControl=false
	ttl := CalculateEffectiveTTL(
		"pass",
		0,
		RuleCacheTTLConfig{},
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "10m"},
		},
		map[string]string{"cache-control": "max-age=300"},
	)

	require.Equal(t, 10*time.Minute, ttl, "Should use rule TTL when followCacheControl=false")
}

func TestCalculateEffectiveTTL_BackendNoCache(t *testing.T) {
	// Backend says don't cache with no-cache directive
	ttl := CalculateEffectiveTTL(
		"pass",
		time.Hour,
		RuleCacheTTLConfig{Pass: "20m"},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "10m"},
		},
		map[string]string{"cache-control": "no-cache"},
	)

	require.Equal(t, time.Duration(0), ttl, "Backend no-cache should be respected immediately")
}

func TestCalculateEffectiveTTL_EndpointCeiling(t *testing.T) {
	// Rule TTL capped by endpoint ceiling
	ttl := CalculateEffectiveTTL(
		"pass",
		0,                               // No server ceiling
		RuleCacheTTLConfig{Pass: "10m"}, // Endpoint ceiling
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "20m"}, // Rule wants 20m
		},
		nil,
	)

	require.Equal(t, 10*time.Minute, ttl, "Should be capped by endpoint ceiling")
}

func TestCalculateEffectiveTTL_ServerCeiling(t *testing.T) {
	// Rule TTL capped by server ceiling
	ttl := CalculateEffectiveTTL(
		"pass",
		30*time.Minute,       // Server ceiling
		RuleCacheTTLConfig{}, // No endpoint ceiling
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "1h"}, // Rule wants 1h
		},
		nil,
	)

	require.Equal(t, 30*time.Minute, ttl, "Should be capped by server ceiling")
}

func TestCalculateEffectiveTTL_MultipleCeilings_PickMinimum(t *testing.T) {
	// Multiple ceilings - should pick minimum
	ttl := CalculateEffectiveTTL(
		"pass",
		1*time.Hour,                     // Server ceiling: 1h
		RuleCacheTTLConfig{Pass: "20m"}, // Endpoint ceiling: 20m (smallest)
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "30m"}, // Rule: 30m
		},
		nil,
	)

	require.Equal(t, 20*time.Minute, ttl, "Should pick minimum of all ceilings")
}

func TestCalculateEffectiveTTL_BackendCappedByCeilings(t *testing.T) {
	// Backend TTL should also be capped by endpoint/server ceilings
	ttl := CalculateEffectiveTTL(
		"pass",
		15*time.Minute,                  // Server ceiling: 15m
		RuleCacheTTLConfig{Pass: "10m"}, // Endpoint ceiling: 10m (smallest)
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "5m"},
		},
		map[string]string{"cache-control": "max-age=1200"}, // Backend: 20m
	)

	require.Equal(t, 10*time.Minute, ttl, "Backend TTL should be capped by endpoint ceiling")
}

func TestCalculateEffectiveTTL_ZeroCeilingsNoEffect(t *testing.T) {
	// Zero ceilings (no ceiling) should not cap
	ttl := CalculateEffectiveTTL(
		"pass",
		0,                    // No server ceiling
		RuleCacheTTLConfig{}, // No endpoint ceiling
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "1h"},
		},
		nil,
	)

	require.Equal(t, 1*time.Hour, ttl, "Zero ceilings should not limit")
}

func TestCalculateEffectiveTTL_RuleZeroTTL(t *testing.T) {
	// Rule with zero TTL means don't cache
	ttl := CalculateEffectiveTTL(
		"pass",
		time.Hour,
		RuleCacheTTLConfig{Pass: "30m"},
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Pass: "0s"}, // Don't cache
		},
		nil,
	)

	require.Equal(t, time.Duration(0), ttl, "Rule TTL of 0 means don't cache")
}

func TestCalculateEffectiveTTL_FailOutcome(t *testing.T) {
	// Test with fail outcome
	ttl := CalculateEffectiveTTL(
		"fail",
		time.Hour,
		RuleCacheTTLConfig{Fail: "2m"}, // Endpoint ceiling for fail
		RuleCacheConfig{
			FollowCacheControl: false,
			TTL:                RuleCacheTTLConfig{Fail: "5m"},
		},
		nil,
	)

	require.Equal(t, 2*time.Minute, ttl, "Should use fail outcome TTL and ceiling")
}

func TestCalculateEffectiveTTL_MissingCacheControlHeader(t *testing.T) {
	// followCacheControl=true but no Cache-Control header, fall back to rule TTL
	ttl := CalculateEffectiveTTL(
		"pass",
		0,
		RuleCacheTTLConfig{},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "10m"},
		},
		map[string]string{}, // No Cache-Control header
	)

	require.Equal(t, 10*time.Minute, ttl, "Should fall back to rule TTL")
}

func TestCalculateEffectiveTTL_EmptyCacheControlHeader(t *testing.T) {
	// followCacheControl=true but empty Cache-Control header
	ttl := CalculateEffectiveTTL(
		"pass",
		0,
		RuleCacheTTLConfig{},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "10m"},
		},
		map[string]string{"cache-control": ""},
	)

	require.Equal(t, 10*time.Minute, ttl, "Empty Cache-Control should fall back to rule TTL")
}

func TestCalculateEffectiveTTL_SMaxAgePrecedence(t *testing.T) {
	// s-maxage should take precedence over max-age
	ttl := CalculateEffectiveTTL(
		"pass",
		0,
		RuleCacheTTLConfig{},
		RuleCacheConfig{
			FollowCacheControl: true,
			TTL:                RuleCacheTTLConfig{Pass: "5m"},
		},
		map[string]string{"cache-control": "max-age=300, s-maxage=600"},
	)

	require.Equal(t, 600*time.Second, ttl, "s-maxage should take precedence")
}

func TestCalculateEffectiveTTL_ComplexScenario(t *testing.T) {
	// Real-world complex scenario
	tests := []struct {
		name           string
		outcome        string
		serverMaxTTL   time.Duration
		endpointTTL    RuleCacheTTLConfig
		ruleConfig     RuleCacheConfig
		backendHeaders map[string]string
		expectedTTL    time.Duration
		description    string
	}{
		{
			name:         "CDN backend with ceilings",
			outcome:      "pass",
			serverMaxTTL: 1 * time.Hour,
			endpointTTL:  RuleCacheTTLConfig{Pass: "20m"},
			ruleConfig: RuleCacheConfig{
				FollowCacheControl: true,
				TTL:                RuleCacheTTLConfig{Pass: "5m"},
			},
			backendHeaders: map[string]string{"cache-control": "max-age=300, s-maxage=3600"},
			expectedTTL:    20 * time.Minute,
			description:    "Backend says 1h (s-maxage), but endpoint ceiling is 20m",
		},
		{
			name:         "Private response",
			outcome:      "pass",
			serverMaxTTL: 1 * time.Hour,
			endpointTTL:  RuleCacheTTLConfig{Pass: "20m"},
			ruleConfig: RuleCacheConfig{
				FollowCacheControl: true,
				TTL:                RuleCacheTTLConfig{Pass: "10m"},
			},
			backendHeaders: map[string]string{"cache-control": "private, max-age=600"},
			expectedTTL:    0,
			description:    "Private directive means don't cache",
		},
		{
			name:         "No backend header, rule capped",
			outcome:      "pass",
			serverMaxTTL: 30 * time.Minute,
			endpointTTL:  RuleCacheTTLConfig{Pass: "15m"},
			ruleConfig: RuleCacheConfig{
				FollowCacheControl: true,
				TTL:                RuleCacheTTLConfig{Pass: "1h"},
			},
			backendHeaders: nil,
			expectedTTL:    15 * time.Minute,
			description:    "Rule wants 1h, but endpoint ceiling is 15m",
		},
		{
			name:         "Fail outcome with different ceilings",
			outcome:      "fail",
			serverMaxTTL: 10 * time.Minute,
			endpointTTL:  RuleCacheTTLConfig{Fail: "2m"},
			ruleConfig: RuleCacheConfig{
				FollowCacheControl: false,
				TTL:                RuleCacheTTLConfig{Fail: "5m"},
			},
			backendHeaders: nil,
			expectedTTL:    2 * time.Minute,
			description:    "Fail outcome capped by endpoint ceiling",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl := CalculateEffectiveTTL(
				tt.outcome,
				tt.serverMaxTTL,
				tt.endpointTTL,
				tt.ruleConfig,
				tt.backendHeaders,
			)
			require.Equal(t, tt.expectedTTL, ttl, tt.description)
		})
	}
}

func TestCalculateEffectiveTTL_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		outcome     string
		expectedTTL time.Duration
	}{
		{
			name:        "Unknown outcome",
			outcome:     "unknown",
			expectedTTL: 0,
		},
		{
			name:        "Empty outcome",
			outcome:     "",
			expectedTTL: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ttl := CalculateEffectiveTTL(
				tt.outcome,
				time.Hour,
				RuleCacheTTLConfig{Pass: "10m"},
				RuleCacheConfig{
					FollowCacheControl: false,
					TTL:                RuleCacheTTLConfig{Pass: "5m"},
				},
				nil,
			)
			require.Equal(t, tt.expectedTTL, ttl)
		})
	}
}
