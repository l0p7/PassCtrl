package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gavv/httpexpect/v2"
	cmdmocks "github.com/l0p7/passctrl/cmd/mocks"
	"github.com/l0p7/passctrl/internal/config"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type integrationProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	wg     sync.WaitGroup
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func startServerProcess(t *testing.T, configPath string, env map[string]string) *integrationProcess {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-config", configPath)
	cmd.Dir = "."
	cacheRoot := filepath.Join(os.TempDir(), "passctrl-integration")
	cacheDir := filepath.Join(cacheRoot, "gocache")
	moduleCache := filepath.Join(cacheRoot, "gomodcache")
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		cancel()
		t.Fatalf("failed to create gocache dir: %v", err)
	}
	if err := os.MkdirAll(moduleCache, 0o750); err != nil {
		cancel()
		t.Fatalf("failed to create gomodcache dir: %v", err)
	}
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOCACHE="+cacheDir, "GOMODCACHE="+moduleCache)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("failed to start server process: %v", err)
	}

	proc := &integrationProcess{cmd: cmd, cancel: cancel, stdout: stdout, stderr: stderr}
	proc.wg.Add(1)
	go func() {
		defer proc.wg.Done()
		_ = cmd.Wait()
	}()
	return proc
}

func (p *integrationProcess) stop(t *testing.T) {
	t.Helper()
	if p == nil {
		return
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(os.Interrupt)
	}
	p.cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGKILL)
		}
	}
	if t.Failed() {
		if out := strings.TrimSpace(p.stdout.String()); out != "" {
			t.Logf("server stdout:\n%s", out)
		}
		if errOut := strings.TrimSpace(p.stderr.String()); errOut != "" {
			t.Logf("server stderr:\n%s", errOut)
		}
	}
}

func waitForEndpoint(t *testing.T, client httpDoer, target string, timeout time.Duration, headers map[string]string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		require.NoError(t, err, "failed to build probe request")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req) // #nosec G107 - test helper for local server
		if err == nil {
			status := resp.StatusCode
			require.NoError(t, resp.Body.Close(), "failed to close readiness probe body")
			if status < 500 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Failf(t, "server readiness", "server did not respond successfully within %v", timeout)
}

func writeIntegrationConfig(t *testing.T, dir string, port int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("failed to ensure rules folder: %v", err)
	}
	cfg := map[string]any{
		"server": map[string]any{
			"listen": map[string]any{
				"address": "127.0.0.1",
				"port":    port,
			},
			"logging": map[string]any{
				"format":            "text",
				"level":             "warn",
				"correlationHeader": "X-Request-ID",
			},
			"rules": map[string]any{
				"rulesFolder": "",
			},
			"cache": map[string]any{
				"backend":    "memory",
				"ttlSeconds": 5,
			},
		},
		"endpoints": map[string]any{
			"default": map[string]any{
				"rules": []map[string]any{
					{"name": "allow-all"},
				},
				"responsePolicy": map[string]any{
					"pass": map[string]any{
						"body": "integration ok",
						"headers": map[string]any{
							"custom": map[string]string{
								"X-Test": "integration",
							},
						},
					},
				},
			},
		},
		"rules": map[string]any{
			"allow-all": map[string]any{
				"conditions": map[string]any{
					"pass": []string{"true"},
				},
			},
		},
	}

	contents, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	path := filepath.Join(dir, "integration-config.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

func allocatePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate port: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", l.Addr())
	}
	port := addr.Port
	if cerr := l.Close(); cerr != nil {
		t.Fatalf("failed to close listener: %v", cerr)
	}
	return port
}

func integrationURL(port int, path string) string {
	u := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:   path,
	}
	return u.String()
}

func TestIntegrationServerStartup(t *testing.T) {
	if os.Getenv("PASSCTRL_INTEGRATION") == "" {
		t.Skip("set PASSCTRL_INTEGRATION=1 to run integration tests")
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	temp := t.TempDir()
	port := allocatePort(t)
	configPath := writeIntegrationConfig(t, temp, port)

	loader := config.NewLoader("PASSCTRL", configPath)
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err, "failed to load integration config")
	require.Contains(t, cfg.Endpoints, "default", "expected default endpoint to be configured")
	require.Contains(t, cfg.Rules, "allow-all", "expected allow-all rule to be configured")

	process := startServerProcess(t, configPath, map[string]string{
		"PASSCTRL_SERVER__LOGGING__LEVEL": "debug",
	})
	defer process.stop(t)

	client := &http.Client{Timeout: 5 * time.Second}
	waitForEndpoint(t, client, integrationURL(port, "/auth"), 45*time.Second, map[string]string{
		"Authorization": "Bearer integration",
	})

	expect := httpexpect.WithConfig(httpexpect.Config{
		BaseURL:  integrationURL(port, ""),
		Reporter: httpexpect.NewRequireReporter(t),
		Client:   client,
	})

	testCases := []struct {
		name           string
		method         string
		path           string
		headers        map[string]string
		expectedStatus int
		expectedBody   string
		expectedHeader map[string]string
	}{
		{
			name:           "default endpoint passes authenticated request",
			method:         http.MethodGet,
			path:           "/auth",
			headers:        map[string]string{"Authorization": "Bearer integration"},
			expectedStatus: http.StatusOK,
			expectedBody:   "integration ok",
			expectedHeader: map[string]string{"X-Test": "integration"},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := expect.Request(tc.method, tc.path)
			for k, v := range tc.headers {
				req = req.WithHeader(k, v)
			}
			result := req.Expect()
			result.Status(tc.expectedStatus)
			if tc.expectedBody != "" {
				result.Body().IsEqual(tc.expectedBody)
			}
			for header, value := range tc.expectedHeader {
				result.Header(header).IsEqual(value)
			}
		})
	}
}

func TestWaitForEndpointRetriesUntilReady(t *testing.T) {
	t.Parallel()

	client := cmdmocks.NewMockHTTPDoer(t)
	target := integrationURL(8080, "/healthz")

	client.EXPECT().
		Do(mock.Anything).
		Return(nil, context.DeadlineExceeded).
		Once()

	client.EXPECT().
		Do(mock.Anything).
		Return(&http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("bad gateway")),
		}, nil).
		Once()

	client.EXPECT().
		Do(mock.Anything).
		Return(&http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil).
		Once()

	waitForEndpoint(t, client, target, time.Second, map[string]string{"X-Test": "1"})
}
