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
