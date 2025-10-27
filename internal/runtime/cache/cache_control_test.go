package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseCacheControl_Empty(t *testing.T) {
	directive := ParseCacheControl("")
	require.Nil(t, directive.MaxAge)
	require.Nil(t, directive.SMaxAge)
	require.False(t, directive.NoCache)
	require.False(t, directive.NoStore)
	require.False(t, directive.Private)
}

func TestParseCacheControl_MaxAge(t *testing.T) {
	directive := ParseCacheControl("max-age=300")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 300, *directive.MaxAge)
	require.Nil(t, directive.SMaxAge)
	require.False(t, directive.NoCache)
}

func TestParseCacheControl_SMaxAge(t *testing.T) {
	directive := ParseCacheControl("s-maxage=600")
	require.NotNil(t, directive.SMaxAge)
	require.Equal(t, 600, *directive.SMaxAge)
	require.Nil(t, directive.MaxAge)
}

func TestParseCacheControl_BothMaxAges(t *testing.T) {
	directive := ParseCacheControl("max-age=300, s-maxage=600")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 300, *directive.MaxAge)
	require.NotNil(t, directive.SMaxAge)
	require.Equal(t, 600, *directive.SMaxAge)
}

func TestParseCacheControl_NoCache(t *testing.T) {
	directive := ParseCacheControl("no-cache")
	require.True(t, directive.NoCache)
	require.False(t, directive.NoStore)
	require.False(t, directive.Private)
}

func TestParseCacheControl_NoStore(t *testing.T) {
	directive := ParseCacheControl("no-store")
	require.True(t, directive.NoStore)
	require.False(t, directive.NoCache)
}

func TestParseCacheControl_Private(t *testing.T) {
	directive := ParseCacheControl("private")
	require.True(t, directive.Private)
	require.False(t, directive.NoCache)
}

func TestParseCacheControl_MultipleDirectives(t *testing.T) {
	directive := ParseCacheControl("max-age=300, no-cache, private")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 300, *directive.MaxAge)
	require.True(t, directive.NoCache)
	require.True(t, directive.Private)
}

func TestParseCacheControl_ComplexHeader(t *testing.T) {
	directive := ParseCacheControl("max-age=600, s-maxage=1200, must-revalidate, public")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 600, *directive.MaxAge)
	require.NotNil(t, directive.SMaxAge)
	require.Equal(t, 1200, *directive.SMaxAge)
	// Unknown directives (must-revalidate, public) are ignored
	require.False(t, directive.NoCache)
}

func TestParseCacheControl_WhitespaceHandling(t *testing.T) {
	directive := ParseCacheControl("  max-age=300  ,  s-maxage=600  ")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 300, *directive.MaxAge)
	require.NotNil(t, directive.SMaxAge)
	require.Equal(t, 600, *directive.SMaxAge)
}

func TestParseCacheControl_CaseInsensitive(t *testing.T) {
	directive := ParseCacheControl("Max-Age=300, S-MAXAGE=600, No-Cache")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 300, *directive.MaxAge)
	require.NotNil(t, directive.SMaxAge)
	require.Equal(t, 600, *directive.SMaxAge)
	require.True(t, directive.NoCache)
}

func TestParseCacheControl_InvalidMaxAge(t *testing.T) {
	directive := ParseCacheControl("max-age=invalid")
	require.Nil(t, directive.MaxAge)
}

func TestParseCacheControl_NegativeMaxAge(t *testing.T) {
	directive := ParseCacheControl("max-age=-100")
	require.Nil(t, directive.MaxAge, "Negative values should be ignored")
}

func TestParseCacheControl_ZeroMaxAge(t *testing.T) {
	directive := ParseCacheControl("max-age=0")
	require.NotNil(t, directive.MaxAge)
	require.Equal(t, 0, *directive.MaxAge)
}

func TestParseCacheControl_UnknownDirectives(t *testing.T) {
	directive := ParseCacheControl("must-revalidate, public, immutable")
	require.Nil(t, directive.MaxAge)
	require.Nil(t, directive.SMaxAge)
	require.False(t, directive.NoCache)
	// Unknown directives should be silently ignored
}

func TestGetTTL_NoDirective(t *testing.T) {
	directive := CacheControlDirective{}
	ttl := directive.GetTTL()
	require.Nil(t, ttl, "No directive should return nil for fallback")
}

func TestGetTTL_MaxAge(t *testing.T) {
	maxAge := 300
	directive := CacheControlDirective{
		MaxAge: &maxAge,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, 300*time.Second, *ttl)
}

func TestGetTTL_SMaxAge(t *testing.T) {
	sMaxAge := 600
	directive := CacheControlDirective{
		SMaxAge: &sMaxAge,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, 600*time.Second, *ttl)
}

func TestGetTTL_SMaxAgePrecedence(t *testing.T) {
	maxAge := 300
	sMaxAge := 600
	directive := CacheControlDirective{
		MaxAge:  &maxAge,
		SMaxAge: &sMaxAge,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, 600*time.Second, *ttl, "s-maxage should take precedence over max-age")
}

func TestGetTTL_NoCache(t *testing.T) {
	maxAge := 300
	directive := CacheControlDirective{
		MaxAge:  &maxAge,
		NoCache: true,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, time.Duration(0), *ttl, "no-cache should override max-age")
}

func TestGetTTL_NoStore(t *testing.T) {
	maxAge := 300
	directive := CacheControlDirective{
		MaxAge:  &maxAge,
		NoStore: true,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, time.Duration(0), *ttl, "no-store should override max-age")
}

func TestGetTTL_Private(t *testing.T) {
	maxAge := 300
	directive := CacheControlDirective{
		MaxAge:  &maxAge,
		Private: true,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, time.Duration(0), *ttl, "private should override max-age")
}

func TestGetTTL_AllNoCacheDirectives(t *testing.T) {
	maxAge := 300
	sMaxAge := 600
	directive := CacheControlDirective{
		MaxAge:  &maxAge,
		SMaxAge: &sMaxAge,
		NoCache: true,
		NoStore: true,
		Private: true,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, time.Duration(0), *ttl, "Don't cache directives should take precedence")
}

func TestGetTTL_ZeroMaxAge(t *testing.T) {
	maxAge := 0
	directive := CacheControlDirective{
		MaxAge: &maxAge,
	}
	ttl := directive.GetTTL()
	require.NotNil(t, ttl)
	require.Equal(t, time.Duration(0), *ttl)
}

func TestIntegration_RealWorldHeaders(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected *time.Duration
	}{
		{
			name:     "CDN standard",
			header:   "max-age=300, s-maxage=3600",
			expected: ptr(3600 * time.Second),
		},
		{
			name:     "No caching",
			header:   "no-cache, no-store, must-revalidate",
			expected: ptr(time.Duration(0)),
		},
		{
			name:     "Private cache",
			header:   "private, max-age=600",
			expected: ptr(time.Duration(0)),
		},
		{
			name:     "Simple max-age",
			header:   "max-age=120",
			expected: ptr(120 * time.Second),
		},
		{
			name:     "No directive",
			header:   "public, must-revalidate",
			expected: nil,
		},
		{
			name:     "Zero max-age",
			header:   "max-age=0",
			expected: ptr(time.Duration(0)),
		},
		{
			name:     "Complex CDN",
			header:   "public, max-age=300, s-maxage=1200, stale-while-revalidate=60",
			expected: ptr(1200 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directive := ParseCacheControl(tt.header)
			ttl := directive.GetTTL()

			if tt.expected == nil {
				require.Nil(t, ttl)
			} else {
				require.NotNil(t, ttl)
				require.Equal(t, *tt.expected, *ttl)
			}
		})
	}
}

func TestParseCacheControl_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		header string
		check  func(*testing.T, CacheControlDirective)
	}{
		{
			name:   "Empty string",
			header: "",
			check: func(t *testing.T, d CacheControlDirective) {
				require.Nil(t, d.MaxAge)
				require.Nil(t, d.SMaxAge)
			},
		},
		{
			name:   "Only commas",
			header: ",,,",
			check: func(t *testing.T, d CacheControlDirective) {
				require.Nil(t, d.MaxAge)
			},
		},
		{
			name:   "Malformed key=value",
			header: "max-age=",
			check: func(t *testing.T, d CacheControlDirective) {
				require.Nil(t, d.MaxAge)
			},
		},
		{
			name:   "Multiple equals",
			header: "max-age=300=400",
			check: func(t *testing.T, d CacheControlDirective) {
				// Should parse first value after split
				require.Nil(t, d.MaxAge)
			},
		},
		{
			name:   "Very large number",
			header: "max-age=31536000", // 1 year
			check: func(t *testing.T, d CacheControlDirective) {
				require.NotNil(t, d.MaxAge)
				require.Equal(t, 31536000, *d.MaxAge)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directive := ParseCacheControl(tt.header)
			tt.check(t, directive)
		})
	}
}

// Helper function to create pointer to duration
func ptr(d time.Duration) *time.Duration {
	return &d
}
