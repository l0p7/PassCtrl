package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBackendDescriptorHash_Deterministic(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users/123",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users/123",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.Equal(t, hash1, hash2, "Same descriptor should produce same hash")
	require.Len(t, hash1, 16, "Hash should be 16 hex characters (64-bit FNV-1a)")
}

func TestBackendDescriptorHash_DifferentURL(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users/123",
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users/456",
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.NotEqual(t, hash1, hash2, "Different URLs should produce different hashes")
}

func TestBackendDescriptorHash_DifferentMethod(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/users",
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.NotEqual(t, hash1, hash2, "Different methods should produce different hashes")
}

func TestBackendDescriptorHash_DifferentHeaders(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"X-Tenant": "tenant1",
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"X-Tenant": "tenant2",
		},
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.NotEqual(t, hash1, hash2, "Different header values should produce different hashes")
}

func TestBackendDescriptorHash_DifferentBody(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/users",
		Body:   `{"name":"Alice"}`,
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/users",
		Body:   `{"name":"Bob"}`,
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.NotEqual(t, hash1, hash2, "Different bodies should produce different hashes")
}

func TestBackendDescriptorHash_HeaderOrderIndependent(t *testing.T) {
	// Map iteration order is undefined in Go, but our hash should be deterministic
	// because we sort headers internally
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token",
			"Content-Type":  "application/json",
			"X-Tenant":      "acme",
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"X-Tenant":      "acme",
			"Authorization": "Bearer token",
			"Content-Type":  "application/json",
		},
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.Equal(t, hash1, hash2, "Header insertion order should not affect hash")
}

func TestBackendDescriptorHash_EmptyFields(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
	}

	desc2 := BackendDescriptor{
		Method:  "GET",
		URL:     "https://api.example.com/users",
		Headers: map[string]string{},
		Body:    "",
	}

	hash1 := desc1.Hash()
	hash2 := desc2.Hash()

	require.Equal(t, hash1, hash2, "Empty headers/body should be equivalent to nil/empty")
}

func TestBackendDescriptorHash_NoHeaders(t *testing.T) {
	desc := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
	}

	hash := desc.Hash()

	require.NotEmpty(t, hash)
	require.Len(t, hash, 16)
}

func TestBackendDescriptorHash_ComplexScenario(t *testing.T) {
	// Simulate a real-world backend request
	desc := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.internal/users/123/permissions",
		Headers: map[string]string{
			"Authorization": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			"Content-Type":  "application/json",
			"X-Request-ID":  "req-abc-123",
			"X-Tenant":      "acme-corp",
			"X-Api-Version": "v2",
		},
		Body: `{"permissions":["read","write"],"resource":"documents"}`,
	}

	hash := desc.Hash()

	require.NotEmpty(t, hash)
	require.Len(t, hash, 16)

	// Verify determinism by computing multiple times
	for i := 0; i < 10; i++ {
		require.Equal(t, hash, desc.Hash())
	}
}

func TestBackendDescriptorHash_ExcludeHeaders(t *testing.T) {
	// Same descriptor with different correlation headers should produce same hash
	// when correlation header is excluded
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-abc-123", // Unique per request
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-xyz-789", // Different correlation header
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	// Without exclusion, hashes should be different
	hash1WithoutExclude := desc1.Hash()
	hash2WithoutExclude := desc2.Hash()
	require.NotEqual(t, hash1WithoutExclude, hash2WithoutExclude,
		"Different X-Request-ID should produce different hashes without exclusion")

	// With exclusion, hashes should be identical
	hash1WithExclude := desc1.Hash("x-request-id")
	hash2WithExclude := desc2.Hash("x-request-id")
	require.Equal(t, hash1WithExclude, hash2WithExclude,
		"Different X-Request-ID should produce same hash when excluded")
}

func TestBackendDescriptorHash_ExcludeMultipleHeaders(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-abc-123",
			"X-Trace-ID":    "trace-def-456",
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-xyz-789", // Different
			"X-Trace-ID":    "trace-uvw-012",   // Different
			"Content-Type":  "application/json",
		},
		Body: `{"key":"value"}`,
	}

	// Exclude both correlation headers
	hash1 := desc1.Hash("x-request-id", "x-trace-id")
	hash2 := desc2.Hash("x-request-id", "x-trace-id")

	require.Equal(t, hash1, hash2,
		"Multiple excluded headers should not affect hash equality")
}

func TestBackendDescriptorHash_ExcludeHeadersCaseInsensitive(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"X-Request-ID": "request-123",
			"x-trace-id":   "trace-456",
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"X-Request-ID": "request-789",
			"x-trace-id":   "trace-012",
		},
	}

	// Test various case combinations
	hash1 := desc1.Hash("X-Request-ID", "x-trace-id")
	hash2 := desc2.Hash("x-request-id", "X-TRACE-ID")

	require.Equal(t, hash1, hash2,
		"Header exclusion should be case-insensitive")
}

func TestBackendDescriptorHash_NonExcludedHeadersStillAffectHash(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-abc",
			"X-Tenant":      "tenant1", // Should affect hash
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-xyz", // Different but excluded
			"X-Tenant":      "tenant2",     // Different and NOT excluded
		},
	}

	hash1 := desc1.Hash("x-request-id")
	hash2 := desc2.Hash("x-request-id")

	require.NotEqual(t, hash1, hash2,
		"Non-excluded headers (X-Tenant) should still affect hash")
}

func TestBackendDescriptorHash_ExcludeNonExistentHeader(t *testing.T) {
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
		},
	}

	// Exclude a header that doesn't exist
	hash1 := desc1.Hash("x-nonexistent-header")
	hash2 := desc2.Hash("x-nonexistent-header")

	require.Equal(t, hash1, hash2,
		"Excluding non-existent headers should not cause issues")
}

func TestBackendDescriptorHash_EmptyExclusionList(t *testing.T) {
	desc := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/users",
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Request-ID":  "request-abc",
		},
	}

	hashWithEmpty := desc.Hash()
	hashWithExplicitEmpty := desc.Hash("")

	require.Equal(t, hashWithEmpty, hashWithExplicitEmpty,
		"Empty exclusion list should be equivalent to no exclusions")
}

func TestBackendDescriptorHash_RealWorldCachingScenario(t *testing.T) {
	// Simulate real-world scenario: same backend request across different HTTP requests
	// Each request has unique correlation headers but should cache the same decision

	createDescriptor := func(requestID, traceID string) BackendDescriptor {
		return BackendDescriptor{
			Method: "POST",
			URL:    "https://auth.example.com/api/validate",
			Headers: map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer user-token-xyz",
				"X-Request-ID":  requestID, // Unique per request
				"X-Trace-ID":    traceID,   // Unique per request
			},
			Body: `{"token":"user-token-xyz"}`,
		}
	}

	// Create 5 descriptors simulating 5 different HTTP requests
	descriptors := []BackendDescriptor{
		createDescriptor("req-001", "trace-001"),
		createDescriptor("req-002", "trace-002"),
		createDescriptor("req-003", "trace-003"),
		createDescriptor("req-004", "trace-004"),
		createDescriptor("req-005", "trace-005"),
	}

	// All should produce the same hash when correlation headers are excluded
	excludedHeaders := []string{"x-request-id", "x-trace-id"}
	hashes := make([]string, len(descriptors))
	for i, desc := range descriptors {
		hashes[i] = desc.Hash(excludedHeaders...)
	}

	// Verify all hashes are identical
	for i := 1; i < len(hashes); i++ {
		require.Equal(t, hashes[0], hashes[i],
			"All requests should produce the same cache key when correlation headers are excluded")
	}

	// But if correlation headers are NOT excluded, hashes should be different
	hashesWithoutExclusion := make([]string, len(descriptors))
	for i, desc := range descriptors {
		hashesWithoutExclusion[i] = desc.Hash()
	}

	for i := 1; i < len(hashesWithoutExclusion); i++ {
		require.NotEqual(t, hashesWithoutExclusion[0], hashesWithoutExclusion[i],
			"Without exclusion, each request should have a different cache key")
	}
}

func TestBackendDescriptorHash_ExcludeForwardProxyHeaders(t *testing.T) {
	// Forward proxy headers should be automatically excluded from cache keys
	// even when forwarded to backends
	desc1 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/validate",
		Headers: map[string]string{
			"Authorization":      "Bearer token123",
			"Content-Type":       "application/json",
			"Forwarded":          "for=192.0.2.1;host=example.com;proto=https",
			"X-Forwarded-For":    "192.0.2.1, 198.51.100.1",
			"X-Forwarded-Host":   "example.com",
			"X-Forwarded-Proto":  "https",
			"X-Forwarded-Port":   "443",
			"X-Forwarded-Prefix": "/api",
		},
		Body: `{"action":"check"}`,
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/validate",
		Headers: map[string]string{
			"Authorization":      "Bearer token123",
			"Content-Type":       "application/json",
			"Forwarded":          "for=203.0.113.1;host=different.com;proto=http", // Different
			"X-Forwarded-For":    "203.0.113.1, 198.51.100.2",                     // Different
			"X-Forwarded-Host":   "different.com",                                 // Different
			"X-Forwarded-Proto":  "http",                                          // Different
			"X-Forwarded-Port":   "80",                                            // Different
			"X-Forwarded-Prefix": "/v2/api",                                       // Different
		},
		Body: `{"action":"check"}`,
	}

	// Exclude all forward proxy headers
	excludeHeaders := []string{
		"forwarded",
		"x-forwarded-for",
		"x-forwarded-host",
		"x-forwarded-proto",
		"x-forwarded-port",
		"x-forwarded-prefix",
	}

	hash1 := desc1.Hash(excludeHeaders...)
	hash2 := desc2.Hash(excludeHeaders...)

	require.Equal(t, hash1, hash2,
		"Different forward proxy headers should produce same hash when excluded")

	// Without exclusion, hashes should be different
	hash1NoExclude := desc1.Hash()
	hash2NoExclude := desc2.Hash()
	require.NotEqual(t, hash1NoExclude, hash2NoExclude,
		"Different forward proxy headers should produce different hashes without exclusion")
}

func TestBackendDescriptorHash_ExcludeCombinedSessionHeaders(t *testing.T) {
	// Real-world scenario: correlation header + forward proxy headers
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://auth.example.com/api/check",
		Headers: map[string]string{
			"Authorization":    "Bearer user-token-abc",
			"Content-Type":     "application/json",
			"X-Request-ID":     "req-123",        // Correlation (unique)
			"X-Forwarded-For":  "192.0.2.1",      // Proxy (unique)
			"X-Forwarded-Host": "api.example.com", // Proxy (unique)
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://auth.example.com/api/check",
		Headers: map[string]string{
			"Authorization":    "Bearer user-token-abc",
			"Content-Type":     "application/json",
			"X-Request-ID":     "req-456",         // Different correlation
			"X-Forwarded-For":  "203.0.113.5",     // Different proxy
			"X-Forwarded-Host": "other.example.com", // Different proxy
		},
	}

	// Exclude both correlation and proxy headers (simulating buildBackendHash behavior)
	excludeHeaders := []string{
		"x-request-id",
		"forwarded",
		"x-forwarded-for",
		"x-forwarded-host",
		"x-forwarded-proto",
		"x-forwarded-port",
		"x-forwarded-prefix",
	}

	hash1 := desc1.Hash(excludeHeaders...)
	hash2 := desc2.Hash(excludeHeaders...)

	require.Equal(t, hash1, hash2,
		"Requests with different session headers should produce same cache key")
}

func TestBackendDescriptorHash_ExcludeDistributedTracingHeaders(t *testing.T) {
	// Distributed tracing headers should be excluded from cache keys
	desc1 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/process",
		Headers: map[string]string{
			"Authorization":         "Bearer token123",
			"Content-Type":          "application/json",
			"Traceparent":           "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
			"Tracestate":            "congo=t61rcWkgMzE",
			"X-B3-TraceId":          "80f198ee56343ba864fe8b2a57d3eff7",
			"X-B3-SpanId":           "e457b5a2e4d86bd1",
			"X-B3-ParentSpanId":     "05e3ac9a4f6e3b90",
			"X-B3-Sampled":          "1",
			"X-Cloud-Trace-Context": "105445aa7843bc8bf206b120001000/1;o=1",
			"X-Amzn-Trace-Id":       "Root=1-67891233-abcdef012345678912345678",
			"Uber-Trace-Id":         "e5ca5e4e93e4d2be:e5ca5e4e93e4d2be:0:1",
		},
		Body: `{"data":"test"}`,
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/process",
		Headers: map[string]string{
			"Authorization":         "Bearer token123",
			"Content-Type":          "application/json",
			"Traceparent":           "00-different-trace-id-here-00000000000-02", // Different
			"Tracestate":            "vendor=differentvalue",                      // Different
			"X-B3-TraceId":          "differenttraceid1234567890abcdef",          // Different
			"X-B3-SpanId":           "differentspanid",                            // Different
			"X-B3-ParentSpanId":     "differentparent",                            // Different
			"X-B3-Sampled":          "0",                                          // Different
			"X-Cloud-Trace-Context": "different-cloud-trace/2;o=0",               // Different
			"X-Amzn-Trace-Id":       "Root=1-99999999-different",                 // Different
			"Uber-Trace-Id":         "different:uber:trace:id",                   // Different
		},
		Body: `{"data":"test"}`,
	}

	// Exclude all tracing headers
	excludeHeaders := []string{
		"traceparent", "tracestate",
		"x-b3-traceid", "x-b3-spanid", "x-b3-parentspanid", "x-b3-sampled", "x-b3-flags",
		"x-cloud-trace-context", "x-amzn-trace-id", "uber-trace-id",
	}

	hash1 := desc1.Hash(excludeHeaders...)
	hash2 := desc2.Hash(excludeHeaders...)

	require.Equal(t, hash1, hash2,
		"Different tracing headers should produce same hash when excluded")
}

func TestBackendDescriptorHash_ExcludeCDNHeaders(t *testing.T) {
	// CDN-specific headers should be excluded from cache keys
	desc1 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/data",
		Headers: map[string]string{
			"Authorization":    "Bearer token123",
			"CF-Ray":           "8a1b2c3d4e5f6g7h-LAX",
			"CF-Connecting-IP": "192.0.2.1",
			"CF-IPCountry":     "US",
			"CF-Visitor":       `{"scheme":"https"}`,
		},
	}

	desc2 := BackendDescriptor{
		Method: "GET",
		URL:    "https://api.example.com/data",
		Headers: map[string]string{
			"Authorization":    "Bearer token123",
			"CF-Ray":           "9z8y7x6w5v4u3t2s-SFO", // Different
			"CF-Connecting-IP": "203.0.113.50",         // Different
			"CF-IPCountry":     "CA",                   // Different
			"CF-Visitor":       `{"scheme":"http"}`,    // Different
		},
	}

	excludeHeaders := []string{"cf-ray", "cf-connecting-ip", "cf-ipcountry", "cf-visitor"}

	hash1 := desc1.Hash(excludeHeaders...)
	hash2 := desc2.Hash(excludeHeaders...)

	require.Equal(t, hash1, hash2,
		"Different CDN headers should produce same hash when excluded")
}

func TestBackendDescriptorHash_ExcludeAllSessionHeaders(t *testing.T) {
	// Comprehensive test: all session-specific headers together
	desc1 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/validate",
		Headers: map[string]string{
			// Functional headers (should affect cache)
			"Authorization": "Bearer user-token-123",
			"Content-Type":  "application/json",

			// Session headers (should NOT affect cache)
			"X-Request-ID":          "req-001",
			"X-Forwarded-For":       "192.0.2.1",
			"X-Forwarded-Host":      "example.com",
			"Forwarded":             "for=192.0.2.1",
			"X-Real-IP":             "192.0.2.1",
			"Traceparent":           "00-trace1-span1-01",
			"X-B3-TraceId":          "trace1",
			"X-Cloud-Trace-Context": "cloud1",
			"X-Request-Start":       "1234567890",
			"CF-Ray":                "ray1",
		},
		Body: `{"action":"check"}`,
	}

	desc2 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/validate",
		Headers: map[string]string{
			// Same functional headers
			"Authorization": "Bearer user-token-123",
			"Content-Type":  "application/json",

			// All session headers different
			"X-Request-ID":          "req-999",
			"X-Forwarded-For":       "203.0.113.99",
			"X-Forwarded-Host":      "different.com",
			"Forwarded":             "for=203.0.113.99",
			"X-Real-IP":             "203.0.113.99",
			"Traceparent":           "00-trace999-span999-01",
			"X-B3-TraceId":          "trace999",
			"X-Cloud-Trace-Context": "cloud999",
			"X-Request-Start":       "9876543210",
			"CF-Ray":                "ray999",
		},
		Body: `{"action":"check"}`,
	}

	// Exclude all session-specific headers (simulating buildBackendHash)
	excludeHeaders := []string{
		"x-request-id",
		"forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
		"x-forwarded-port", "x-forwarded-prefix", "x-real-ip", "x-original-forwarded-for",
		"true-client-ip",
		"traceparent", "tracestate", "x-b3-traceid", "x-b3-spanid",
		"x-b3-parentspanid", "x-b3-sampled", "x-b3-flags",
		"x-cloud-trace-context", "x-amzn-trace-id", "uber-trace-id",
		"x-request-start", "x-timer",
		"cf-ray", "cf-connecting-ip", "cf-ipcountry", "cf-visitor",
	}

	hash1 := desc1.Hash(excludeHeaders...)
	hash2 := desc2.Hash(excludeHeaders...)

	require.Equal(t, hash1, hash2,
		"All session headers should be excluded, only functional headers affect cache")

	// Verify that functional headers still affect the cache
	desc3 := BackendDescriptor{
		Method: "POST",
		URL:    "https://api.example.com/validate",
		Headers: map[string]string{
			"Authorization": "Bearer DIFFERENT-TOKEN", // Different functional header
			"Content-Type":  "application/json",
			"X-Request-ID":  "req-001", // Same session header as desc1
		},
		Body: `{"action":"check"}`,
	}

	hash3 := desc3.Hash(excludeHeaders...)
	require.NotEqual(t, hash1, hash3,
		"Different Authorization header should produce different cache key")
}

func TestHashUpstreamVariables_EmptyInput(t *testing.T) {
	// Nil map should return empty string
	hash := HashUpstreamVariables(nil)
	require.Empty(t, hash)

	// Empty map should return empty string
	hash = HashUpstreamVariables(map[string]map[string]any{})
	require.Empty(t, hash)
}

func TestHashUpstreamVariables_Deterministic(t *testing.T) {
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"tier":    "premium",
		},
		"validate-token": {
			"session_id": "abc-xyz",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"tier":    "premium",
		},
		"validate-token": {
			"session_id": "abc-xyz",
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.Equal(t, hash1, hash2, "Same variables should produce same hash")
	require.Len(t, hash1, 16, "Hash should be 16 hex characters (64-bit FNV-1a)")
	require.NotEmpty(t, hash1)
}

func TestHashUpstreamVariables_RuleOrderIndependent(t *testing.T) {
	// Rule names sorted differently but same content
	vars1 := map[string]map[string]any{
		"rule-a": {"var1": "value1"},
		"rule-b": {"var2": "value2"},
		"rule-c": {"var3": "value3"},
	}

	vars2 := map[string]map[string]any{
		"rule-c": {"var3": "value3"},
		"rule-a": {"var1": "value1"},
		"rule-b": {"var2": "value2"},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.Equal(t, hash1, hash2, "Rule insertion order should not affect hash")
}

func TestHashUpstreamVariables_VariableOrderIndependent(t *testing.T) {
	// Variables within a rule sorted differently but same content
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id":  "123",
			"tier":     "premium",
			"email":    "user@example.com",
			"status":   "active",
			"metadata": "...",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"metadata": "...",
			"status":   "active",
			"email":    "user@example.com",
			"tier":     "premium",
			"user_id":  "123",
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.Equal(t, hash1, hash2, "Variable insertion order should not affect hash")
}

func TestHashUpstreamVariables_DifferentValues(t *testing.T) {
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"tier":    "premium",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"tier":    "free", // Changed value
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.NotEqual(t, hash1, hash2, "Different values should produce different hashes")
}

func TestHashUpstreamVariables_DifferentVariableNames(t *testing.T) {
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"session_id": "123", // Different variable name
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.NotEqual(t, hash1, hash2, "Different variable names should produce different hashes")
}

func TestHashUpstreamVariables_DifferentRuleNames(t *testing.T) {
	vars1 := map[string]map[string]any{
		"rule-a": {
			"var1": "value1",
		},
	}

	vars2 := map[string]map[string]any{
		"rule-b": {
			"var1": "value1", // Same variable, different rule
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.NotEqual(t, hash1, hash2, "Different rule names should produce different hashes")
}

func TestHashUpstreamVariables_AdditionalVariable(t *testing.T) {
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"tier":    "premium", // Additional variable
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.NotEqual(t, hash1, hash2, "Additional variables should produce different hashes")
}

func TestHashUpstreamVariables_AdditionalRule(t *testing.T) {
	vars1 := map[string]map[string]any{
		"rule-a": {
			"var1": "value1",
		},
	}

	vars2 := map[string]map[string]any{
		"rule-a": {
			"var1": "value1",
		},
		"rule-b": {
			"var2": "value2", // Additional rule
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.NotEqual(t, hash1, hash2, "Additional rules should produce different hashes")
}

func TestHashUpstreamVariables_VariousTypes(t *testing.T) {
	// Test different value types that fmt.Sprint can handle
	vars := map[string]map[string]any{
		"rule-types": {
			"string_var": "text",
			"int_var":    42,
			"float_var":  3.14,
			"bool_var":   true,
			"nil_var":    nil,
		},
	}

	hash := HashUpstreamVariables(vars)

	require.NotEmpty(t, hash)
	require.Len(t, hash, 16)

	// Verify determinism
	for i := 0; i < 10; i++ {
		require.Equal(t, hash, HashUpstreamVariables(vars))
	}
}

func TestHashUpstreamVariables_RealWorldScenario(t *testing.T) {
	// Simulate a realistic multi-rule chain with upstream variables
	vars := map[string]map[string]any{
		"validate-token": {
			"user_id":    "user-abc-123",
			"session_id": "sess-xyz-789",
			"expires_at": "2024-12-31T23:59:59Z",
		},
		"lookup-user": {
			"user_id":  "user-abc-123",
			"tier":     "premium",
			"email":    "user@example.com",
			"status":   "active",
			"metadata": map[string]any{"region": "us-west", "plan": "pro"},
		},
		"fetch-permissions": {
			"permissions": []string{"read", "write", "admin"},
			"roles":       []string{"user", "moderator"},
		},
	}

	hash := HashUpstreamVariables(vars)

	require.NotEmpty(t, hash)
	require.Len(t, hash, 16)

	// Verify determinism across multiple computations
	for i := 0; i < 10; i++ {
		require.Equal(t, hash, HashUpstreamVariables(vars))
	}
}

func TestHashUpstreamVariables_SameValuesRefresh(t *testing.T) {
	// Simulate the scenario from design doc where upstream rule refreshes
	// but returns same values - should produce same hash (cache hit)
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123", // Same value, refreshed at different time
		},
	}

	hash1 := HashUpstreamVariables(vars1)
	hash2 := HashUpstreamVariables(vars2)

	require.Equal(t, hash1, hash2, "Same values should produce same hash even after refresh")
}

func TestHashUpstreamVariables_SingleRule(t *testing.T) {
	vars := map[string]map[string]any{
		"single-rule": {
			"var1": "value1",
		},
	}

	hash := HashUpstreamVariables(vars)

	require.NotEmpty(t, hash)
	require.Len(t, hash, 16)
}

func TestHashUpstreamVariables_NestedMapDeterminism(t *testing.T) {
	// This test verifies the fix for non-deterministic hashing when variables contain maps.
	// Prior to the fix, fmt.Fprint on map values would produce non-deterministic output
	// due to Go's randomized map iteration order. Now we use JSON encoding which is deterministic.

	// Create two identical variable sets with nested maps
	vars1 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"metadata": map[string]any{
				"region": "us-west",
				"plan":   "pro",
				"tier":   "premium",
			},
			"tags": map[string]string{
				"department": "engineering",
				"team":       "platform",
				"role":       "admin",
			},
		},
	}

	vars2 := map[string]map[string]any{
		"lookup-user": {
			"user_id": "123",
			"metadata": map[string]any{
				"tier":   "premium",
				"region": "us-west",
				"plan":   "pro",
			},
			"tags": map[string]string{
				"role":       "admin",
				"department": "engineering",
				"team":       "platform",
			},
		},
	}

	// Compute hashes multiple times to ensure determinism
	hashes1 := make([]string, 100)
	hashes2 := make([]string, 100)

	for i := 0; i < 100; i++ {
		hashes1[i] = HashUpstreamVariables(vars1)
		hashes2[i] = HashUpstreamVariables(vars2)
	}

	// All hashes from vars1 should be identical
	for i := 1; i < len(hashes1); i++ {
		require.Equal(t, hashes1[0], hashes1[i],
			"Hash should be deterministic across multiple computations for vars1")
	}

	// All hashes from vars2 should be identical
	for i := 1; i < len(hashes2); i++ {
		require.Equal(t, hashes2[0], hashes2[i],
			"Hash should be deterministic across multiple computations for vars2")
	}

	// vars1 and vars2 have the same logical content (just different map iteration order)
	// so they should produce the same hash
	require.Equal(t, hashes1[0], hashes2[0],
		"Logically identical nested maps should produce the same hash regardless of insertion order")
}
