package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/admission"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/responsepolicy"
	"github.com/l0p7/passctrl/internal/runtime/resultcaching"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
)

func newTestPipelineState(req *http.Request) *pipeline.State {
	return pipeline.NewState(req, "test", cacheKeyFromRequest(req, "test"), "")
}

func TestNewPipelineState(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "bearer-token")
	req.Header.Set("X-Ignore", "noop")

	state := newTestPipelineState(req)

	if state.CacheKey() != "bearer-token|test|/v1/auth" {
		t.Fatalf("unexpected cache key: %q", state.CacheKey())
	}
	if state.Raw.Method != http.MethodPost {
		t.Errorf("expected method %s, got %s", http.MethodPost, state.Raw.Method)
	}
	if state.Raw.Headers["authorization"] != "bearer-token" {
		t.Errorf("authorization header not captured: %#v", state.Raw.Headers)
	}
	if state.Response.Headers == nil {
		t.Fatalf("response headers map should be initialized")
	}
	if state.Forward.Headers == nil || state.Forward.Query == nil {
		t.Fatalf("forward state should be initialized with empty maps")
	}
}

func TestAdmissionAgentExecute(t *testing.T) {
	trusted := admission.ParseCIDRs([]string{"127.0.0.0/8"})
	t.Run("passes with authorization and trusted proxy", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), false)
		t.Logf("trusted networks: %#v", agent)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "pass" {
			t.Fatalf("expected pass, got %s", res.Status)
		}
		if !state.Admission.Authenticated || !state.Admission.TrustedProxy {
			t.Fatalf("expected authenticated via trusted proxy: %#v", state.Admission)
		}
		if state.Admission.ClientIP != "203.0.113.5" {
			t.Fatalf("expected client ip from forwarded header, got %s", state.Admission.ClientIP)
		}
		if state.Admission.ForwardedFor != "203.0.113.5" {
			t.Fatalf("expected sanitized forwarded chain, got %s", state.Admission.ForwardedFor)
		}
		if req.Header.Get("X-Forwarded-For") != "203.0.113.5" {
			t.Fatalf("expected forwarded header to be normalized on the request")
		}
	})

	t.Run("rejects untrusted proxy in production", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "198.51.100.10:443"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.7")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail for untrusted proxy, got %s", res.Status)
		}
		if state.Admission.Reason != "untrusted proxy rejected" {
			t.Fatalf("unexpected reason: %s", state.Admission.Reason)
		}
	})

	t.Run("accepts forwarded chain when remote is trusted", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "pass" {
			t.Fatalf("expected pass when remote is trusted, got %s (reason=%s)", res.Status, state.Admission.Reason)
		}
		if !state.Admission.TrustedProxy {
			t.Fatalf("expected trusted proxy flag to be true")
		}
		if state.Admission.ClientIP != "203.0.113.5" {
			t.Fatalf("expected client ip from first forwarded hop, got %s", state.Admission.ClientIP)
		}
	})

	t.Run("strips forwarded headers in development", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), true)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "198.51.100.10:443"
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 203.0.113.8")
		req.Header.Set("X-Forwarded-Prefix", "/staging")
		req.Header.Set("Forwarded", "for=203.0.113.7;proto=https")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail due to missing auth header, got %s", res.Status)
		}
		if !state.Admission.ProxyStripped {
			t.Fatalf("expected proxy headers to be stripped in development mode")
		}
		if req.Header.Get("X-Forwarded-For") != "" {
			t.Fatalf("expected forwarded header removed after sanitization")
		}
		if req.Header.Get("Forwarded") != "" {
			t.Fatalf("expected RFC7239 forwarded header removed after sanitization")
		}
		if state.Admission.Forwarded != "" {
			t.Fatalf("expected forwarded metadata cleared when stripping headers")
		}
		if !strings.Contains(state.Admission.Reason, "authorization header missing") {
			t.Fatalf("expected admission reason to mention missing authorization: %s", state.Admission.Reason)
		}
		if req.Header.Get("X-Forwarded-Prefix") != "" {
			t.Fatalf("expected forwarded prefix header removed after sanitization")
		}
	})

	t.Run("development mode keeps forwarded chain when remote is trusted", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), true)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "pass" {
			t.Fatalf("expected pass with trusted remote, got %s (reason=%s)", res.Status, state.Admission.Reason)
		}
		if state.Admission.ProxyStripped {
			t.Fatalf("did not expect forwarded headers stripped in development with trusted remote")
		}
		if !state.Admission.TrustedProxy {
			t.Fatalf("expected trusted proxy flag to be true")
		}
		if state.Admission.ClientIP != "203.0.113.5" {
			t.Fatalf("expected client ip from forwarded chain, got %s", state.Admission.ClientIP)
		}
		if req.Header.Get("X-Forwarded-For") == "" {
			t.Fatalf("expected forwarded header retained in development mode when remote trusted")
		}
	})

	t.Run("rejects invalid forwarded chain", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, not-an-ip")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail for invalid forwarded chain, got %s", res.Status)
		}
		if state.Admission.Reason != "invalid forwarded chain" {
			t.Fatalf("unexpected reason: %s", state.Admission.Reason)
		}
	})

	t.Run("accepts RFC7239 forwarded header", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Forwarded", "For=\"[2001:db8::1]:443\";Proto=https;By=203.0.113.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "pass" {
			t.Fatalf("expected pass for valid forwarded header, got %s", res.Status)
		}
		if state.Admission.ClientIP != "2001:db8::1" {
			t.Fatalf("expected client ip from RFC7239 header, got %s", state.Admission.ClientIP)
		}
		expected := "for=\"[2001:db8::1]:443\"; proto=https; by=203.0.113.10"
		if state.Admission.Forwarded != expected {
			t.Fatalf("unexpected sanitized forwarded header: %s", state.Admission.Forwarded)
		}
		if req.Header.Get("Forwarded") != expected {
			t.Fatalf("expected forwarded header normalized on request: %s", req.Header.Get("Forwarded"))
		}
	})

	t.Run("rejects mismatched forwarded metadata", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		req.Header.Set("Forwarded", "for=198.51.100.9")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail for mismatched forwarded metadata, got %s", res.Status)
		}
		if state.Admission.Reason != "forwarded metadata mismatch between headers" {
			t.Fatalf("unexpected reason: %s", state.Admission.Reason)
		}
	})

	t.Run("rejects obfuscated forwarded directive", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Forwarded", "for=_hidden")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail for obfuscated forwarded directive, got %s", res.Status)
		}
		if state.Admission.Reason != "forwarded metadata missing for directive" {
			t.Fatalf("unexpected reason: %s", state.Admission.Reason)
		}
	})

	t.Run("captures invalid remote address", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "bad-addr"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail for invalid remote address, got %s", res.Status)
		}
		if state.Admission.Reason != "invalid remote address" {
			t.Fatalf("unexpected reason: %s", state.Admission.Reason)
		}
	})

	t.Run("records decision snapshot on success", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "pass" {
			t.Fatalf("expected pass status, got %s", res.Status)
		}
		if state.Admission.Decision != "pass" {
			t.Fatalf("expected decision snapshot to record pass, got %s", state.Admission.Decision)
		}
		if state.Admission.Snapshot == nil {
			t.Fatalf("snapshot should be populated on success")
		}
		if reason, ok := state.Admission.Snapshot["reason"].(string); !ok || !strings.Contains(reason, "accepted") {
			t.Fatalf("expected snapshot reason to include accepted, got %#v", state.Admission.Snapshot["reason"])
		}
	})

	t.Run("records decision snapshot on missing credentials", func(t *testing.T) {
		agent := admission.New(trusted, false)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		if res.Status != "fail" {
			t.Fatalf("expected fail status when authorization missing, got %s", res.Status)
		}
		if state.Admission.Decision != "fail" {
			t.Fatalf("expected decision snapshot to record fail, got %s", state.Admission.Decision)
		}
		if state.Admission.Snapshot == nil {
			t.Fatalf("snapshot should be populated on failure")
		}
		if reason, ok := state.Admission.Snapshot["reason"].(string); !ok || !strings.Contains(reason, "missing") {
			t.Fatalf("expected snapshot reason to capture missing credential, got %#v", state.Admission.Snapshot["reason"])
		}
	})
}

func TestRuleChainAgentExecute(t *testing.T) {
	t.Run("cached", func(t *testing.T) {
		state := &pipeline.State{}
		state.Cache.Hit = true
		state.Cache.Decision = "pass"

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "cached" {
			t.Fatalf("expected cached status, got %s", res.Status)
		}
		if state.Rule.Outcome != "pass" || !state.Rule.FromCache {
			t.Fatalf("expected rule to reuse cached decision: %#v", state.Rule)
		}
	})

	t.Run("admission failure", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = false

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "short_circuited" {
			t.Fatalf("expected short circuit status, got %s", res.Status)
		}
		if state.Rule.ShouldExecute {
			t.Fatalf("rule execution should be disabled on admission failure")
		}
	})

	t.Run("ready", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = true

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "ready" {
			t.Fatalf("expected ready status, got %s", res.Status)
		}
		if !state.Rule.ShouldExecute {
			t.Fatalf("rule execution should proceed after admission success")
		}
		if plan, _ := state.Plan().(rulechain.ExecutionPlan); len(plan.Rules) != len(rulechain.DefaultDefinitions(nil)) {
			t.Fatalf("expected rule plan to include default rules")
		}
	})
}

func TestRuleExecutionAgentExecute(t *testing.T) {
	agent := newRuleExecutionAgent(nil, nil, nil)

	t.Run("skip on cache", func(t *testing.T) {
		state := &pipeline.State{Rule: pipeline.RuleState{FromCache: true}}
		res := agent.Execute(context.Background(), nil, state)
		if res.Status != "skipped" {
			t.Fatalf("expected skip status, got %s", res.Status)
		}
	})

	t.Run("no rules defined defaults to pass", func(t *testing.T) {
		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "pass" || res.Status != "pass" {
			t.Fatalf("expected pass outcome, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if state.Rule.Reason != "no rules defined" {
			t.Fatalf("expected default reason for empty plan, got %s", state.Rule.Reason)
		}
	})

	t.Run("fail when condition matches", func(t *testing.T) {
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "deny",
			Conditions: rulechain.ConditionSpec{
				Fail: []string{`forward.headers["x-passctrl-deny"] == "true"`},
			},
			FailMessage: "denied by header",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"x-passctrl-deny": "true"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "fail" || res.Status != "fail" {
			t.Fatalf("expected fail outcome, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if len(state.Rule.History) != 1 || state.Rule.History[0].Outcome != "fail" {
			t.Fatalf("expected history to record failure: %#v", state.Rule.History)
		}
		if state.Rule.Reason != "denied by header" {
			t.Fatalf("unexpected failure reason: %s", state.Rule.Reason)
		}
	})

	t.Run("error when condition matches", func(t *testing.T) {
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "error",
			Conditions: rulechain.ConditionSpec{
				Error: []string{`forward.query["error"] == "true"`},
			},
			ErrorMessage: "error toggle requested",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{Query: map[string]string{"error": "true"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "error" || res.Status != "error" {
			t.Fatalf("expected error outcome, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
	})

	t.Run("pass outcome when condition satisfied", func(t *testing.T) {
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "pass",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`forward.headers["authorization"] == "token"`},
			},
			PassMessage: "allowed",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "pass" || res.Status != "pass" {
			t.Fatalf("expected pass outcome, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if state.Rule.Reason != "allowed" {
			t.Fatalf("unexpected pass reason: %s", state.Rule.Reason)
		}
	})

	t.Run("fails when required condition missing", func(t *testing.T) {
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "requires-query",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{
					`forward.headers["authorization"] == "token"`,
					`lookup(forward.query, "allow") == "true"`,
				},
			},
			FailMessage: "required condition missing",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "fail" || res.Status != "fail" {
			t.Fatalf("expected fail outcome when condition missing, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if state.Rule.Reason != "required condition missing" {
			t.Fatalf("unexpected failure reason: %s", state.Rule.Reason)
		}
	})

	t.Run("pass when all predicates satisfied", func(t *testing.T) {
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "requires-query",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{
					`forward.headers["authorization"] == "token"`,
					`lookup(forward.query, "allow") == "true"`,
				},
			},
			PassMessage: "all conditions met",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{
			Headers: map[string]string{"authorization": "token"},
			Query:   map[string]string{"allow": "true"},
		}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "pass" || res.Status != "pass" {
			t.Fatalf("expected pass outcome when all predicates met, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if state.Rule.Reason != "all conditions met" {
			t.Fatalf("unexpected pass reason: %s", state.Rule.Reason)
		}
	})

	t.Run("backend response evaluated with cel", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := fmt.Fprint(w, `{"allowed": true, "count": 5}`); err != nil {
				t.Fatalf("write backend response: %v", err)
			}
		}))
		t.Cleanup(server.Close)

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "backend-pass",
			Backend: rulechain.BackendDefinitionSpec{
				URL: server.URL,
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`backend.body.allowed == true`},
				Fail: []string{`backend.body.count < 1`},
			},
			PassMessage: "backend allowed",
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if state.Rule.Outcome != "pass" || res.Status != "pass" {
			t.Fatalf("expected backend-driven pass, got state=%s result=%s", state.Rule.Outcome, res.Status)
		}
		if state.Rule.Reason != "backend allowed" {
			t.Fatalf("unexpected pass reason: %s", state.Rule.Reason)
		}
		if !state.Backend.Requested || state.Backend.Status != http.StatusOK {
			t.Fatalf("expected backend request to be recorded: %#v", state.Backend)
		}
		body, ok := state.Backend.Body.(map[string]any)
		if !ok || body["allowed"] != true {
			t.Fatalf("expected backend body to be stored: %#v", state.Backend.Body)
		}
	})

	t.Run("forwards curated metadata to backend", func(t *testing.T) {
		var (
			seenAuth   string
			seenCustom string
			seenProxy  string
			seenQuery  string
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			seenAuth = r.Header.Get("Authorization")
			seenCustom = r.Header.Get("X-Test")
			seenProxy = r.Header.Get("X-Forwarded-For")
			seenQuery = r.URL.RawQuery
			if _, err := fmt.Fprint(w, `{"allowed": true}`); err != nil {
				t.Fatalf("write backend response: %v", err)
			}
		}))
		t.Cleanup(server.Close)

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "forwarding",
			Backend: rulechain.BackendDefinitionSpec{
				URL:                 server.URL + "/check",
				Method:              http.MethodGet,
				ForwardProxyHeaders: true,
				Headers: forwardpolicy.CategoryConfig{
					Allow:  []string{"authorization"},
					Custom: map[string]string{"x-test": "custom"},
				},
				Query: forwardpolicy.CategoryConfig{
					Allow: []string{"allow"},
				},
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`backend.body.allowed == true`},
			},
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{
			Forward: pipeline.ForwardState{
				Headers: map[string]string{"authorization": "Bearer original"},
				Query:   map[string]string{"allow": "true"},
			},
			Admission: pipeline.AdmissionState{
				Authenticated: true,
				ForwardedFor:  "198.51.100.10",
				Forwarded:     "for=198.51.100.10",
			},
		}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if res.Status != "pass" {
			t.Fatalf("expected pass status, got %s", res.Status)
		}
		if seenAuth != "Bearer original" {
			t.Fatalf("backend should observe forwarded authorization header, got %s", seenAuth)
		}
		if seenCustom != "custom" {
			t.Fatalf("backend should receive custom header, got %s", seenCustom)
		}
		if seenProxy != "198.51.100.10" {
			t.Fatalf("backend should receive sanitized forwarded header, got %s", seenProxy)
		}
		values, err := url.ParseQuery(seenQuery)
		if err != nil {
			t.Fatalf("parse backend query: %v", err)
		}
		if values.Get("allow") != "true" {
			t.Fatalf("expected allow query parameter forwarded, got %s", values.Get("allow"))
		}
		if len(state.Backend.Pages) != 1 {
			t.Fatalf("expected single page recorded, got %d", len(state.Backend.Pages))
		}
	})

	// New test: renders backend body from inline template and file
	t.Run("renders backend body from templates", func(t *testing.T) {
		var seenBodyInline string
		var seenBodyFile string

		serverInline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			seenBodyInline = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok": true}`)
		}))
		t.Cleanup(serverInline.Close)

		serverFile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			seenBodyFile = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok": true}`)
		}))
		t.Cleanup(serverFile.Close)

		dir := t.TempDir()
		sandbox, err := templates.NewSandbox(dir, true, []string{"TOKEN"})
		if err != nil {
			t.Fatalf("sandbox create: %v", err)
		}
		t.Setenv("TOKEN", "secret")
		renderer := templates.NewRenderer(sandbox)

		// Create a file template
		path := filepath.Join(dir, "body.txt")
		if err := os.WriteFile(path, []byte("file-{{ env \"TOKEN\" }}"), 0o600); err != nil {
			t.Fatalf("write template file: %v", err)
		}

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
			{
				Name:       "inline",
				Backend:    rulechain.BackendDefinitionSpec{URL: serverInline.URL, Method: http.MethodPost, Body: "inline-{{ env \"TOKEN\" }}"},
				Conditions: rulechain.ConditionSpec{Pass: []string{"true"}},
			},
			{
				Name:       "file",
				Backend:    rulechain.BackendDefinitionSpec{URL: serverFile.URL, Method: http.MethodPost, BodyFile: fmt.Sprintf("{{ \"%s\" }}", path)},
				Conditions: rulechain.ConditionSpec{Pass: []string{"true"}},
			},
		}, renderer)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		agentWithRenderer := newRuleExecutionAgent(nil, nil, renderer)
		res := agentWithRenderer.Execute(context.Background(), nil, state)
		if res.Status != "pass" {
			t.Fatalf("expected pass, got %s", res.Status)
		}
		if seenBodyInline != "inline-secret" {
			t.Fatalf("expected inline body rendered, got %q", seenBodyInline)
		}
		if seenBodyFile != "file-secret" {
			t.Fatalf("expected file body rendered, got %q", seenBodyFile)
		}
	})

	t.Run("follows link header pagination", func(t *testing.T) {
		var server *httptest.Server
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
			if page == "" {
				page = "1"
			}
			w.Header().Set("Content-Type", "application/json")
			if page == "1" {
				w.Header().Set("Link", fmt.Sprintf("<%s?page=2>; rel=\"next\"", server.URL+"/paginate"))
				if _, err := fmt.Fprint(w, `{"allowed": false}`); err != nil {
					t.Fatalf("write backend response: %v", err)
				}
				return
			}
			if _, err := fmt.Fprint(w, `{"allowed": true}`); err != nil {
				t.Fatalf("write backend response: %v", err)
			}
		})
		server = httptest.NewServer(handler)
		t.Cleanup(server.Close)

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "paginate",
			Backend: rulechain.BackendDefinitionSpec{
				URL:   server.URL + "/paginate?page=1",
				Query: forwardpolicy.CategoryConfig{Allow: []string{"allow"}},
				Pagination: rulechain.BackendPaginationSpec{
					Type:     "link-header",
					MaxPages: 3,
				},
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{
					`size(backend.pages) == 2`,
					`backend.pages[1].body.allowed == true`,
				},
			},
		}}, nil)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{
			Forward: pipeline.ForwardState{Query: map[string]string{"allow": "true"}},
			Admission: pipeline.AdmissionState{
				Authenticated: true,
			},
		}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if res.Status != "pass" {
			t.Fatalf("expected pass status after pagination, got %s", res.Status)
		}
		if len(state.Backend.Pages) != 2 {
			t.Fatalf("expected two backend pages recorded, got %d", len(state.Backend.Pages))
		}
		if state.Backend.Status != http.StatusOK || !state.Backend.Accepted {
			t.Fatalf("expected final backend status accepted, got %#v", state.Backend)
		}
		if body, ok := state.Backend.Body.(map[string]any); !ok || body["allowed"] != true {
			t.Fatalf("expected final backend body to reflect second page: %#v", state.Backend.Body)
		}
		if !strings.Contains(state.Backend.BodyText, "allowed") {
			t.Fatalf("expected body text to capture payload, got %s", state.Backend.BodyText)
		}
	})

	t.Run("renders template reason with sandbox context", func(t *testing.T) {
		dir := t.TempDir()
		sandbox, err := templates.NewSandbox(dir, true, []string{"ALLOWED"})
		if err != nil {
			t.Fatalf("sandbox create: %v", err)
		}
		t.Setenv("ALLOWED", "visible")
		renderer := templates.NewRenderer(sandbox)

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "templated",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`forward.headers["authorization"] == "token"`},
			},
			PassMessage: "{{ env \"ALLOWED\" }}:{{ index .forward.Headers \"authorization\" }}",
		}}, renderer)
		if err != nil {
			t.Fatalf("compile rule: %v", err)
		}

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		if res.Status != "pass" {
			t.Fatalf("expected pass status for templated rule, got %s", res.Status)
		}
		if state.Rule.Reason != "visible:token" {
			t.Fatalf("expected rendered template reason, got %s", state.Rule.Reason)
		}
	})
}

func TestResponsePolicyAgentExecute(t *testing.T) {
	agent := responsepolicy.New()

	cases := map[string]struct {
		outcome string
		expect  int
	}{
		"pass":  {outcome: "pass", expect: http.StatusOK},
		"fail":  {outcome: "fail", expect: http.StatusForbidden},
		"error": {outcome: "error", expect: http.StatusBadGateway},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			state := &pipeline.State{}
			state.Rule.Outcome = tc.outcome
			res := agent.Execute(context.Background(), nil, state)

			if res.Status != "rendered" {
				t.Fatalf("unexpected status: %s", res.Status)
			}
			if state.Response.Status != tc.expect {
				t.Fatalf("expected %d, got %d", tc.expect, state.Response.Status)
			}
			if state.Response.Headers["X-PassCtrl-Outcome"] != tc.outcome {
				t.Fatalf("expected outcome header to match %s", tc.outcome)
			}
		})
	}
}

func TestResultCachingAgentExecute(t *testing.T) {
	decisionCache := cache.NewMemory(5 * time.Minute)
	agent := resultcaching.New(resultcaching.Config{Cache: decisionCache, TTL: 5 * time.Minute})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)

	state := newTestPipelineState(req)
	state.Rule.Outcome = "pass"
	state.Response.Status = http.StatusOK
	state.Response.Message = "granted"

	res := agent.Execute(req.Context(), req, state)
	if res.Status != "stored" {
		t.Fatalf("expected stored status, got %s", res.Status)
	}
	if !state.Cache.Stored {
		t.Fatalf("cache state should record stored decision")
	}
	entry, ok, err := decisionCache.Lookup(req.Context(), state.CacheKey())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatalf("cache should contain stored decision")
	}
	if entry.Decision != "pass" {
		t.Fatalf("expected cached decision to be pass, got %s", entry.Decision)
	}

	t.Run("error outcome bypasses cache", func(t *testing.T) {
		state := newTestPipelineState(req)
		state.Rule.Outcome = "error"

		res := agent.Execute(req.Context(), req, state)
		if res.Status != "bypassed" {
			t.Fatalf("expected bypassed status for error, got %s", res.Status)
		}
	})
}
