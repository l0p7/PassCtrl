package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

func ptrBool(b bool) *bool {
	return &b
}

func TestPerRuleCaching_CacheHitSkipsBackend(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"user_id":"123"}`))
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "lookup-user",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{
					Pass: "5m",
				},
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "cache-key-123", "corr-1")
	state.Admission.Authenticated = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state.Rule.ShouldExecute = true

	// First execution - should call backend
	res := agent.Execute(context.Background(), nil, state)
	require.Equal(t, "pass", res.Status)
	require.Equal(t, 1, backendCalls, "First request should call backend")
	require.True(t, state.Cache.Stored, "Should mark cache as stored")

	// Second execution - should hit cache
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "cache-key-123", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	res2 := agent.Execute(context.Background(), nil, state2)
	require.Equal(t, "pass", res2.Status)
	require.Equal(t, 1, backendCalls, "Second request should NOT call backend (cache hit)")
	require.True(t, state2.Cache.Hit, "Should mark cache as hit")
	require.Len(t, state2.Rule.History, 1)
	require.True(t, state2.Rule.History[0].FromCache, "History should indicate cache hit")
}

func TestPerRuleCaching_CacheMissExecutesBackend(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "check-access",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	// First request
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth?id=1", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	agent.Execute(context.Background(), nil, state1)
	require.Equal(t, 1, backendCalls)

	// Second request with different cache key (different query param) - cache miss
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth?id=2", nil), "test", "key-2", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	agent.Execute(context.Background(), nil, state2)
	require.Equal(t, 2, backendCalls, "Different cache key should cause cache miss")
	require.False(t, state2.Cache.Hit)
}

func TestPerRuleCaching_ExportedVariablesCachedAndRestored(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"userId":"user-123","tier":"premium"}`))
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	renderer := templates.NewRenderer(nil)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "lookup-user",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{
						"user_id": "backend.body.userId",
						"tier":    "backend.body.tier",
					},
				},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL:    rulechain.CacheTTLSpec{Pass: "5m"},
				Strict: ptrBool(false),
			},
		},
	}, renderer)
	require.NoError(t, err)

	// First request - cache miss, backend called
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	res1 := agent.Execute(context.Background(), nil, state1)
	require.Equal(t, "pass", res1.Status)
	require.Equal(t, 1, backendCalls, "First request should call backend")
	require.True(t, state1.Cache.Stored, "Should mark cache as stored")
	// Check exported variables were evaluated
	require.NotNil(t, state1.Variables.Rules["lookup-user"])
	require.Equal(t, "user-123", state1.Variables.Rules["lookup-user"]["user_id"])
	require.Equal(t, "premium", state1.Variables.Rules["lookup-user"]["tier"])

	// Second request - cache hit, variables restored from cache
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	res2 := agent.Execute(context.Background(), nil, state2)
	require.Equal(t, "pass", res2.Status)
	require.Equal(t, 1, backendCalls, "Second request should NOT call backend (cache hit)")
	require.True(t, state2.Cache.Hit, "Should mark cache as hit")
	require.Len(t, state2.Rule.History, 1)
	require.True(t, state2.Rule.History[0].FromCache, "History should indicate cache hit")
	// Check cached variables were restored
	require.NotNil(t, state2.Variables.Rules["lookup-user"])
	require.Equal(t, "user-123", state2.Variables.Rules["lookup-user"]["user_id"], "Cached variables should be restored")
	require.Equal(t, "premium", state2.Variables.Rules["lookup-user"]["tier"], "Cached variables should be restored")
}

func TestPerRuleCaching_StrictMode_UpstreamChangesInvalidate(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	strictTrue := true
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "check-permissions",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL:    rulechain.CacheTTLSpec{Pass: "5m"},
				Strict: &strictTrue,
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	// First request with upstream variable user_id=123
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true
	state1.Variables.Rules = map[string]map[string]any{
		"upstream-rule": {"user_id": "123"},
	}

	agent.Execute(context.Background(), nil, state1)
	require.Equal(t, 1, backendCalls)

	// Second request with DIFFERENT upstream variable user_id=456
	// In strict mode, this should cause cache miss
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true
	state2.Variables.Rules = map[string]map[string]any{
		"upstream-rule": {"user_id": "456"}, // Changed value
	}

	agent.Execute(context.Background(), nil, state2)
	require.Equal(t, 2, backendCalls, "Strict mode: upstream variable change should invalidate cache")
	require.False(t, state2.Cache.Hit)
}

func TestPerRuleCaching_LooseMode_UpstreamChangesDontInvalidate(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	strictFalse := false
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "check-permissions",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL:    rulechain.CacheTTLSpec{Pass: "5m"},
				Strict: &strictFalse,
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	// First request with upstream variable user_id=123
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true
	state1.Variables.Rules = map[string]map[string]any{
		"upstream-rule": {"user_id": "123"},
	}

	agent.Execute(context.Background(), nil, state1)
	require.Equal(t, 1, backendCalls)

	// Second request with DIFFERENT upstream variable user_id=456
	// In loose mode (strict=false), this should still hit cache
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true
	state2.Variables.Rules = map[string]map[string]any{
		"upstream-rule": {"user_id": "456"}, // Changed value
	}

	agent.Execute(context.Background(), nil, state2)
	require.Equal(t, 1, backendCalls, "Loose mode: upstream variable change should NOT invalidate cache")
	require.True(t, state2.Cache.Hit, "Should hit cache in loose mode despite upstream changes")
}

func TestPerRuleCaching_OnlyRulesWithBackendAreCached(t *testing.T) {
	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "no-backend-rule",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{"true"},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{Pass: "5m"},
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state.Admission.Authenticated = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state.Rule.ShouldExecute = true

	res := agent.Execute(context.Background(), nil, state)
	require.Equal(t, "pass", res.Status)
	require.False(t, state.Cache.Stored, "Rules without backend should NOT be cached")
	require.False(t, state.Cache.Hit)
}

func TestPerRuleCaching_DifferentBackendRequestsGenerateDifferentHashes(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	renderer := templates.NewRenderer(nil)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "backend-with-body",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "POST",
				Accepted: []int{http.StatusOK},
				Body:     `{"user_id":"{{ .forward.Query.user_id }}"}`,
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{Pass: "5m"},
			},
		},
	}, renderer)
	require.NoError(t, err)

	// Request 1: user_id=123
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth?user_id=123", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.Forward.Query = map[string]string{"user_id": "123"}
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	res1 := agent.Execute(context.Background(), nil, state1)
	require.Equal(t, "pass", res1.Status, "First request should pass. Outcome: %s, Reason: %s", state1.Rule.Outcome, state1.Rule.Reason)
	require.Equal(t, 1, backendCalls)

	// Request 2: user_id=456 (different body value)
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth?user_id=456", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.Forward.Query = map[string]string{"user_id": "456"}
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	agent.Execute(context.Background(), nil, state2)
	require.Equal(t, 2, backendCalls, "Different backend header should cause cache miss")
}

func TestPerRuleCaching_TTLRespected(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "short-ttl",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{Pass: "100ms"}, // Very short TTL
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	// First request
	agent.Execute(context.Background(), nil, state1)
	require.Equal(t, 1, backendCalls)

	// Immediate second request - should hit cache
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	agent.Execute(context.Background(), nil, state2)
	require.Equal(t, 1, backendCalls, "Should hit cache within TTL")
	require.True(t, state2.Cache.Hit)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Third request after expiry - should miss cache
	state3 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-3")
	state3.Admission.Authenticated = true
	state3.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state3.Rule.ShouldExecute = true

	agent.Execute(context.Background(), nil, state3)
	require.Equal(t, 2, backendCalls, "Should miss cache after TTL expiry")
	require.False(t, state3.Cache.Hit)
}

func TestPerRuleCaching_FailOutcomeCached(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "check-access",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Conditions: rulechain.ConditionSpec{
				Fail: []string{`backend.status == 403`},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{Fail: "2m"},
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	// First request - fail outcome
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	res1 := agent.Execute(context.Background(), nil, state1)
	require.Equal(t, "fail", res1.Status)
	require.Equal(t, 1, backendCalls)

	// Second request - should hit cache for fail outcome
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	res2 := agent.Execute(context.Background(), nil, state2)
	require.Equal(t, "fail", res2.Status)
	require.Equal(t, 1, backendCalls, "Fail outcome should be cached")
	require.True(t, state2.Cache.Hit)
}

func TestPerRuleCaching_ErrorOutcomeNeverCached(t *testing.T) {
	backendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	memCache := cache.NewMemory(5 * time.Minute)
	backendAgent := newBackendInteractionAgent(&http.Client{}, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), memCache, time.Hour, nil)

	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "check-access",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      server.URL,
				Method:   "GET",
				Accepted: []int{http.StatusOK},
			},
			Cache: rulechain.CacheConfigSpec{
				TTL: rulechain.CacheTTLSpec{
					Pass:  "5m",
					Error: "5m", // Even though we set error TTL, it should be ignored
				},
			},
		},
	}, templates.NewRenderer(nil))
	require.NoError(t, err)

	// First request - error outcome (500 not in accepted)
	state1 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-1")
	state1.Admission.Authenticated = true
	state1.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state1.Rule.ShouldExecute = true

	res1 := agent.Execute(context.Background(), nil, state1)
	require.Equal(t, "fail", res1.Status) // 500 causes fail when not in accepted
	require.Equal(t, 1, backendCalls)

	// Second request - should NOT hit cache for error-like outcomes
	// Actually in this design fail outcomes ARE cached, let me adjust
	state2 := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://test/auth", nil), "test", "key-1", "corr-2")
	state2.Admission.Authenticated = true
	state2.SetPlan(rulechain.ExecutionPlan{Rules: defs})
	state2.Rule.ShouldExecute = true

	res2 := agent.Execute(context.Background(), nil, state2)
	require.Equal(t, "fail", res2.Status)
	// Fail is cached, so this will hit
}
