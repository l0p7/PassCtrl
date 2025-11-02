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
