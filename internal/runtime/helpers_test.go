package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/admission"
	"github.com/stretchr/testify/require"
)

func TestCacheKeyFromRequest_Authorization(t *testing.T) {
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"basic", "bearer"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req.Header.Set("Authorization", "Bearer token123")
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	require.Equal(t, "auth:Bearer token123|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_CustomHeader(t *testing.T) {
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Header: []string{"x-api-key", "x-session-token"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req.Header.Set("X-API-Key", "secret123")
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	require.Equal(t, "header:x-api-key:secret123|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_QueryParam(t *testing.T) {
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Query: []string{"token", "api_key"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data?token=xyz789&other=value", http.NoBody)
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	require.Equal(t, "query:token:xyz789|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_PriorityOrder(t *testing.T) {
	// Test that Authorization header takes priority over custom headers and query params
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
			Header:        []string{"x-api-key"},
			Query:         []string{"token"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data?token=query-token", http.NoBody)
	req.Header.Set("Authorization", "Bearer auth-token")
	req.Header.Set("X-API-Key", "header-token")
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	// Should use Authorization header (highest priority)
	require.Equal(t, "auth:Bearer auth-token|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_HeaderOverQuery(t *testing.T) {
	// Test that custom headers take priority over query params
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Header: []string{"x-api-key"},
			Query:  []string{"token"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data?token=query-token", http.NoBody)
	req.Header.Set("X-API-Key", "header-token")
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	// Should use custom header (higher priority than query)
	require.Equal(t, "header:x-api-key:header-token|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_FallbackToIP(t *testing.T) {
	// When no credentials are found, should fall back to IP
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req.RemoteAddr = "192.168.1.100:12345"

	key := cacheKeyFromRequest(req, "test-endpoint", cfg)

	require.Equal(t, "ip:192.168.1.100:12345|test-endpoint|/api/data", key)
}

func TestCacheKeyFromRequest_DifferentCredentialsProduceDifferentKeys(t *testing.T) {
	// SECURITY: Users with different credentials must get different cache keys
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
		},
	}

	req1 := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req1.Header.Set("Authorization", "Bearer user1-token")
	req1.RemoteAddr = "192.168.1.100:12345"

	req2 := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req2.Header.Set("Authorization", "Bearer user2-token")
	req2.RemoteAddr = "192.168.1.100:12345"

	key1 := cacheKeyFromRequest(req1, "test-endpoint", cfg)
	key2 := cacheKeyFromRequest(req2, "test-endpoint", cfg)

	// Different users must have different cache keys
	require.NotEqual(t, key1, key2)
	require.Contains(t, key1, "user1-token")
	require.Contains(t, key2, "user2-token")
}

func TestCacheKeyFromRequest_SameCredentialsSameKeys(t *testing.T) {
	// PERFORMANCE: Same user from different IPs should get same cache key
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
		},
	}

	req1 := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req1.Header.Set("Authorization", "Bearer same-token")
	req1.RemoteAddr = "192.168.1.100:12345"

	req2 := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req2.Header.Set("Authorization", "Bearer same-token")
	req2.RemoteAddr = "10.0.0.50:54321" // Different IP

	key1 := cacheKeyFromRequest(req1, "test-endpoint", cfg)
	key2 := cacheKeyFromRequest(req2, "test-endpoint", cfg)

	// Same credential should produce same cache key regardless of IP
	require.Equal(t, key1, key2)
}

func TestCacheKeyFromRequest_HeaderAuthDifferentFromQueryAuth(t *testing.T) {
	// SECURITY: Same value in different auth sources should produce different keys
	cfgHeader := &admission.Config{
		Allow: admission.AllowConfig{
			Header: []string{"x-token"},
		},
	}

	cfgQuery := &admission.Config{
		Allow: admission.AllowConfig{
			Query: []string{"token"},
		},
	}

	reqHeader := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	reqHeader.Header.Set("X-Token", "abc123")

	reqQuery := httptest.NewRequest(http.MethodGet, "http://example.com/api/data?token=abc123", http.NoBody)

	keyHeader := cacheKeyFromRequest(reqHeader, "test-endpoint", cfgHeader)
	keyQuery := cacheKeyFromRequest(reqQuery, "test-endpoint", cfgQuery)

	// Same value in different sources must produce different keys
	require.NotEqual(t, keyHeader, keyQuery)
	require.Contains(t, keyHeader, "header:")
	require.Contains(t, keyQuery, "query:")
}

func TestCacheKeyFromRequest_NilInputs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	cfg := &admission.Config{}

	// Nil request should return empty
	require.Empty(t, cacheKeyFromRequest(nil, "test", cfg))

	// Nil config should return empty
	require.Empty(t, cacheKeyFromRequest(req, "test", nil))
}

func TestCacheKeyFromRequest_PathIsolation(t *testing.T) {
	// Same credential but different paths should produce different keys
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
		},
	}

	req1 := httptest.NewRequest(http.MethodGet, "http://example.com/api/users", http.NoBody)
	req1.Header.Set("Authorization", "Bearer token")

	req2 := httptest.NewRequest(http.MethodGet, "http://example.com/api/posts", http.NoBody)
	req2.Header.Set("Authorization", "Bearer token")

	key1 := cacheKeyFromRequest(req1, "test-endpoint", cfg)
	key2 := cacheKeyFromRequest(req2, "test-endpoint", cfg)

	require.NotEqual(t, key1, key2)
	require.Contains(t, key1, "/api/users")
	require.Contains(t, key2, "/api/posts")
}

func TestCacheKeyFromRequest_EndpointIsolation(t *testing.T) {
	// Same credential but different endpoints should produce different keys
	cfg := &admission.Config{
		Allow: admission.AllowConfig{
			Authorization: []string{"bearer"},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/data", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")

	key1 := cacheKeyFromRequest(req, "endpoint-A", cfg)
	key2 := cacheKeyFromRequest(req, "endpoint-B", cfg)

	require.NotEqual(t, key1, key2)
	require.Contains(t, key1, "endpoint-A")
	require.Contains(t, key2, "endpoint-B")
}
