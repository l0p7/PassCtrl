package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	runtimemocks "github.com/l0p7/passctrl/internal/mocks/runtime"
	"github.com/l0p7/passctrl/internal/runtime/admission"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/responsepolicy"
	"github.com/l0p7/passctrl/internal/runtime/resultcaching"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var bearerOnlyAdmission = admission.Config{
	Allow: admission.AllowConfig{Authorization: []string{"bearer"}},
}

func newTestPipelineState(req *http.Request) *pipeline.State {
	return pipeline.NewState(req, "test", cacheKeyFromRequest(req, "test"), "")
}

func TestNewPipelineState(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "bearer-token")
	req.Header.Set("X-Ignore", "noop")

	state := newTestPipelineState(req)

	require.Equal(t, "bearer-token|test|/v1/auth", state.CacheKey())
	require.Equal(t, http.MethodPost, state.Raw.Method)
	require.Equal(t, "bearer-token", state.Raw.Headers["authorization"])
	require.NotNil(t, state.Response.Headers)
	require.NotNil(t, state.Forward.Headers)
	require.NotNil(t, state.Forward.Query)
}

func TestAdmissionAgentExecute(t *testing.T) {
	trusted := admission.ParseCIDRs([]string{"127.0.0.0/8"})
	t.Run("passes with authorization and trusted proxy", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), false, bearerOnlyAdmission)
		t.Logf("trusted networks: %#v", agent)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "pass", res.Status)
		require.True(t, state.Admission.Authenticated)
		require.True(t, state.Admission.TrustedProxy)
		require.Equal(t, "203.0.113.5", state.Admission.ClientIP)
		require.Equal(t, "203.0.113.5", state.Admission.ForwardedFor)
		require.Equal(t, "203.0.113.5", req.Header.Get("X-Forwarded-For"))
	})

	t.Run("rejects untrusted proxy in production", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "198.51.100.10:443"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.7")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "untrusted proxy rejected", state.Admission.Reason)
	})

	t.Run("accepts forwarded chain when remote is trusted", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "pass", res.Status, "reason=%s", state.Admission.Reason)
		require.True(t, state.Admission.TrustedProxy)
		require.Equal(t, "203.0.113.5", state.Admission.ClientIP)
	})

	t.Run("strips forwarded headers in development", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), true, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "198.51.100.10:443"
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 203.0.113.8")
		req.Header.Set("X-Forwarded-Prefix", "/staging")
		req.Header.Set("Forwarded", "for=203.0.113.7;proto=https")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.True(t, state.Admission.ProxyStripped)
		require.Empty(t, req.Header.Get("X-Forwarded-For"))
		require.Empty(t, req.Header.Get("Forwarded"))
		require.Empty(t, state.Admission.Forwarded)
		require.Contains(t, state.Admission.Reason, "no allowed credentials present")
		require.Empty(t, req.Header.Get("X-Forwarded-Prefix"))
	})

	t.Run("development mode keeps forwarded chain when remote is trusted", func(t *testing.T) {
		agent := admission.New(admission.ParseCIDRs([]string{"127.0.0.0/8", "198.51.100.0/24"}), true, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "pass", res.Status, "reason=%s", state.Admission.Reason)
		require.False(t, state.Admission.ProxyStripped)
		require.True(t, state.Admission.TrustedProxy)
		require.Equal(t, "203.0.113.5", state.Admission.ClientIP)
		require.NotEmpty(t, req.Header.Get("X-Forwarded-For"))
	})

	t.Run("rejects invalid forwarded chain", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5, not-an-ip")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "invalid forwarded chain", state.Admission.Reason)
	})

	t.Run("accepts RFC7239 forwarded header", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Forwarded", "For=\"[2001:db8::1]:443\";Proto=https;By=203.0.113.10")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "pass", res.Status, "expected pass for valid forwarded header")
		require.Equal(t, "2001:db8::1", state.Admission.ClientIP)
		expected := "for=\"[2001:db8::1]:443\"; proto=https; by=203.0.113.10"
		require.Equal(t, expected, state.Admission.Forwarded)
		require.Equal(t, expected, req.Header.Get("Forwarded"))
	})

	t.Run("rejects mismatched forwarded metadata", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		req.Header.Set("Forwarded", "for=198.51.100.9")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "forwarded metadata mismatch between headers", state.Admission.Reason)
	})

	t.Run("rejects obfuscated forwarded directive", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Forwarded", "for=_hidden")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "forwarded metadata missing for directive", state.Admission.Reason)
	})

	t.Run("captures invalid remote address", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "bad-addr"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "invalid remote address", state.Admission.Reason)
	})

	t.Run("records decision snapshot on success", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer token")

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "pass", res.Status)
		require.Equal(t, "pass", state.Admission.Decision)
		require.NotNil(t, state.Admission.Snapshot)
		reason, ok := state.Admission.Snapshot["reason"].(string)
		require.True(t, ok)
		require.Contains(t, reason, "authentication requirements satisfied")
	})

	t.Run("records decision snapshot on missing credentials", func(t *testing.T) {
		agent := admission.New(trusted, false, bearerOnlyAdmission)
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.RemoteAddr = "127.0.0.1:12345"

		state := newTestPipelineState(req)
		res := agent.Execute(req.Context(), req, state)

		require.Equal(t, "fail", res.Status)
		require.Equal(t, "fail", state.Admission.Decision)
		require.NotNil(t, state.Admission.Snapshot)
		reason, ok := state.Admission.Snapshot["reason"].(string)
		require.True(t, ok)
		require.Contains(t, reason, "no allowed credentials present")
	})
}

func TestRuleChainAgentExecute(t *testing.T) {
	t.Run("cached", func(t *testing.T) {
		state := &pipeline.State{}
		state.Cache.Hit = true
		state.Cache.Decision = "pass"

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		require.Equal(t, "cached", res.Status)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.True(t, state.Rule.FromCache)
	})

	t.Run("admission failure", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = false

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		require.Equal(t, "short_circuited", res.Status)
		require.False(t, state.Rule.ShouldExecute)
	})

	t.Run("ready", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = true

		agent := rulechain.NewAgent(rulechain.DefaultDefinitions(nil))
		res := agent.Execute(context.Background(), nil, state)

		require.Equal(t, "ready", res.Status)
		require.True(t, state.Rule.ShouldExecute)
		plan, ok := state.Plan().(rulechain.ExecutionPlan)
		require.True(t, ok)
		require.Len(t, plan.Rules, len(rulechain.DefaultDefinitions(nil)))
	})
}

func TestRuleExecutionAgentExecute(t *testing.T) {
	newAgent := func(client httpDoer) *ruleExecutionAgent {
		return newRuleExecutionAgent(client, nil, nil)
	}

	t.Run("skip on cache", func(t *testing.T) {
		agent := newAgent(nil)
		state := &pipeline.State{Rule: pipeline.RuleState{FromCache: true}}
		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "skipped", res.Status)
	})

	t.Run("no rules defined defaults to pass", func(t *testing.T) {
		agent := newAgent(nil)
		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "no rules defined", state.Rule.Reason)
	})

	t.Run("fail when condition matches", func(t *testing.T) {
		agent := newAgent(nil)
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name:        "deny",
			Conditions:  rulechain.ConditionSpec{Fail: []string{`forward.headers["x-passctrl-deny"] == "true"`}},
			FailMessage: "denied by header",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"x-passctrl-deny": "true"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "fail", state.Rule.Outcome)
		require.Equal(t, "fail", res.Status)
		require.Len(t, state.Rule.History, 1)
		require.Equal(t, "fail", state.Rule.History[0].Outcome)
		require.Equal(t, "denied by header", state.Rule.Reason)
	})

	t.Run("error when condition matches", func(t *testing.T) {
		agent := newAgent(nil)
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name:         "error",
			Conditions:   rulechain.ConditionSpec{Error: []string{`forward.query["error"] == "true"`}},
			ErrorMessage: "error toggle requested",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{Query: map[string]string{"error": "true"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "error", state.Rule.Outcome)
		require.Equal(t, "error", res.Status)
	})

	t.Run("pass outcome when condition satisfied", func(t *testing.T) {
		agent := newAgent(nil)
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name:        "pass",
			Conditions:  rulechain.ConditionSpec{Pass: []string{`forward.headers["authorization"] == "token"`}},
			PassMessage: "allowed",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "allowed", state.Rule.Reason)
	})

	t.Run("fails when required condition missing", func(t *testing.T) {
		agent := newAgent(nil)
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "requires-query",
			Conditions: rulechain.ConditionSpec{Pass: []string{
				`forward.headers["authorization"] == "token"`,
				`lookup(forward.query, "allow") == "true"`,
			}},
			FailMessage: "required condition missing",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "fail", state.Rule.Outcome)
		require.Equal(t, "fail", res.Status)
		require.Equal(t, "required condition missing", state.Rule.Reason)
	})

	t.Run("pass when all predicates satisfied", func(t *testing.T) {
		agent := newAgent(nil)
		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "requires-query",
			Conditions: rulechain.ConditionSpec{Pass: []string{
				`forward.headers["authorization"] == "token"`,
				`lookup(forward.query, "allow") == "true"`,
			}},
			PassMessage: "all conditions met",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{
			Headers: map[string]string{"authorization": "token"},
			Query:   map[string]string{"allow": "true"},
		}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "all conditions met", state.Rule.Reason)
	})

	t.Run("backend response evaluated with cel", func(t *testing.T) {
		backendURL := "https://backend.test/cel"
		mockClient := runtimemocks.NewMockHTTPDoer(t)
		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, backendURL, req.URL.String())
				return newBackendResponse(http.StatusOK, `{"allowed": true, "count": 5}`, map[string]string{"Content-Type": "application/json"}), nil
			}).Once()

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "backend-pass",
			Backend: rulechain.BackendDefinitionSpec{
				URL: backendURL,
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`backend.body.allowed == true`},
				Fail: []string{`backend.body.count < 1`},
			},
			PassMessage: "backend allowed",
		}}, nil)
		require.NoError(t, err)

		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := newAgent(mockClient).Execute(context.Background(), nil, state)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "backend allowed", state.Rule.Reason)
		require.True(t, state.Backend.Requested)
		require.Equal(t, http.StatusOK, state.Backend.Status)
		body, ok := state.Backend.Body.(map[string]any)
		require.True(t, ok)
		require.Equal(t, true, body["allowed"])
	})

	t.Run("forwards curated metadata to backend", func(t *testing.T) {
		backendURL := "https://backend.test/check"
		mockClient := runtimemocks.NewMockHTTPDoer(t)
		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "https", req.URL.Scheme)
				require.Equal(t, "backend.test", req.URL.Host)
				require.Equal(t, "/check", req.URL.Path)
				require.Equal(t, "Bearer original", req.Header.Get("Authorization"))
				require.Equal(t, "custom", req.Header.Get("X-Test"))
				require.Equal(t, "198.51.100.10", req.Header.Get("X-Forwarded-For"))
				require.Equal(t, "true", req.URL.Query().Get("allow"))
				return newBackendResponse(http.StatusOK, `{"allowed": true}`, map[string]string{"Content-Type": "application/json"}), nil
			}).Once()

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "forwarding",
			Backend: rulechain.BackendDefinitionSpec{
				URL:                 backendURL,
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
		require.NoError(t, err)

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

		res := newAgent(mockClient).Execute(context.Background(), nil, state)
		require.Equal(t, "pass", res.Status)
		require.Len(t, state.Backend.Pages, 1)
	})

	t.Run("renders backend body from templates", func(t *testing.T) {
		dir := t.TempDir()
		sandbox, err := templates.NewSandbox(dir, true, []string{"TOKEN"})
		require.NoError(t, err)
		t.Setenv("TOKEN", "secret")
		renderer := templates.NewRenderer(sandbox)

		path := filepath.Join(dir, "body.txt")
		require.NoError(t, os.WriteFile(path, []byte("file-{{ env \"TOKEN\" }}"), 0o600))

		inlineURL := "https://backend.test/inline"
		fileURL := "https://backend.test/file"

		mockClient := runtimemocks.NewMockHTTPDoer(t)
		var inlineBodies []string
		var fileBodies []string

		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, inlineURL, req.URL.String())
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				_ = req.Body.Close()
				inlineBodies = append(inlineBodies, string(data))
				return newBackendResponse(http.StatusOK, `{"ok": true}`, map[string]string{"Content-Type": "application/json"}), nil
			}).Once()

		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, fileURL, req.URL.String())
				data, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				_ = req.Body.Close()
				fileBodies = append(fileBodies, string(data))
				return newBackendResponse(http.StatusOK, `{"ok": true}`, map[string]string{"Content-Type": "application/json"}), nil
			}).Once()

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
			{
				Name:       "inline",
				Backend:    rulechain.BackendDefinitionSpec{URL: inlineURL, Method: http.MethodPost, Body: "inline-{{ env \"TOKEN\" }}"},
				Conditions: rulechain.ConditionSpec{Pass: []string{"true"}},
			},
			{
				Name:       "file",
				Backend:    rulechain.BackendDefinitionSpec{URL: fileURL, Method: http.MethodPost, BodyFile: fmt.Sprintf("{{ \"%s\" }}", path)},
				Conditions: rulechain.ConditionSpec{Pass: []string{"true"}},
			},
		}, renderer)
		require.NoError(t, err)

		state := &pipeline.State{}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := newRuleExecutionAgent(mockClient, nil, renderer).Execute(context.Background(), nil, state)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "pass", state.Rule.Outcome)
		require.Len(t, state.Rule.History, 2)
		require.True(t, state.Backend.Requested)
		require.True(t, state.Backend.Accepted)
		require.Contains(t, inlineBodies, "inline-secret")
		require.Contains(t, fileBodies, "file-secret")
	})

	t.Run("follows link header pagination", func(t *testing.T) {
		initialURL := "https://backend.test/paginate?page=1"
		nextURL := "https://backend.test/paginate?page=2"
		mockClient := runtimemocks.NewMockHTTPDoer(t)

		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "https", req.URL.Scheme)
				require.Equal(t, "backend.test", req.URL.Host)
				require.Equal(t, "/paginate", req.URL.Path)
				require.Equal(t, "1", req.URL.Query().Get("page"))
				require.Equal(t, "true", req.URL.Query().Get("allow"))
				resp := newBackendResponse(http.StatusOK, `{"allowed": false}`, map[string]string{"Content-Type": "application/json"})
				resp.Header.Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", nextURL))
				return resp, nil
			}).Once()

		mockClient.EXPECT().
			Do(mock.AnythingOfType("*http.Request")).
			RunAndReturn(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "https", req.URL.Scheme)
				require.Equal(t, "backend.test", req.URL.Host)
				require.Equal(t, "/paginate", req.URL.Path)
				require.Equal(t, "2", req.URL.Query().Get("page"))
				require.Equal(t, "true", req.URL.Query().Get("allow"))
				return newBackendResponse(http.StatusOK, `{"allowed": true}`, map[string]string{"Content-Type": "application/json"}), nil
			}).Once()

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "paginate",
			Backend: rulechain.BackendDefinitionSpec{
				URL:   initialURL,
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
		require.NoError(t, err)

		state := &pipeline.State{
			Forward: pipeline.ForwardState{Query: map[string]string{"allow": "true"}},
			Admission: pipeline.AdmissionState{
				Authenticated: true,
			},
		}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := newAgent(mockClient).Execute(context.Background(), nil, state)
		require.Equal(t, "pass", res.Status)
		require.Len(t, state.Backend.Pages, 2)
		require.Equal(t, http.StatusOK, state.Backend.Status)
		require.True(t, state.Backend.Accepted)
		body, ok := state.Backend.Body.(map[string]any)
		require.True(t, ok)
		require.Equal(t, true, body["allowed"])
		require.Contains(t, state.Backend.BodyText, "allowed")
	})

	t.Run("renders template reason with sandbox context", func(t *testing.T) {
		agent := newAgent(nil)
		dir := t.TempDir()
		sandbox, err := templates.NewSandbox(dir, true, []string{"ALLOWED"})
		require.NoError(t, err)
		t.Setenv("ALLOWED", "visible")
		renderer := templates.NewRenderer(sandbox)

		defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
			Name: "templated",
			Conditions: rulechain.ConditionSpec{
				Pass: []string{`forward.headers["authorization"] == "token"`},
			},
			PassMessage: "{{ env \"ALLOWED\" }}:{{ index .forward.Headers \"authorization\" }}",
		}}, renderer)
		require.NoError(t, err)

		state := &pipeline.State{Forward: pipeline.ForwardState{Headers: map[string]string{"authorization": "token"}}}
		state.Rule.ShouldExecute = true
		state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

		res := agent.Execute(context.Background(), nil, state)
		require.Equal(t, "pass", res.Status)
		require.Equal(t, "visible:token", state.Rule.Reason)
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

			require.Equal(t, "rendered", res.Status)
			require.Equal(t, tc.expect, state.Response.Status)
			require.Equal(t, tc.outcome, state.Response.Headers["X-PassCtrl-Outcome"])
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
	require.Equal(t, "stored", res.Status)
	require.True(t, state.Cache.Stored)
	entry, ok, err := decisionCache.Lookup(req.Context(), state.CacheKey())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "pass", entry.Decision)

	t.Run("error outcome bypasses cache", func(t *testing.T) {
		state := newTestPipelineState(req)
		state.Rule.Outcome = "error"

		res := agent.Execute(req.Context(), req, state)
		require.Equal(t, "bypassed", res.Status)
	})
}
