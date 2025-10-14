package server

import (
	"fmt"
	"net/http"
	"strings"
)

// PipelineHTTP defines the minimal surface the lifecycle router needs from the
// runtime pipeline to serve HTTP requests.
type PipelineHTTP interface {
	ServeAuth(http.ResponseWriter, *http.Request)
	ServeHealth(http.ResponseWriter, *http.Request)
	ServeExplain(http.ResponseWriter, *http.Request)
	EndpointExists(string) bool
	RequestWithEndpointHint(*http.Request, string) *http.Request
	WriteError(http.ResponseWriter, int, string)
}

// NewPipelineHandler wires the HTTP routing facade to the runtime pipeline so
// the lifecycle server owns URL dispatch without embedding routing logic into
// the pipeline itself.
func NewPipelineHandler(p PipelineHTTP) http.Handler {
	if p == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "pipeline unavailable", http.StatusServiceUnavailable)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint, route, ok := parseEndpointRoute(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}

		switch route {
		case "auth":
			if endpoint != "" {
				r = p.RequestWithEndpointHint(r, endpoint)
			}
			p.ServeAuth(w, r)
		case "healthz":
			if endpoint != "" {
				if !p.EndpointExists(endpoint) {
					p.WriteError(w, http.StatusNotFound, fmt.Sprintf("endpoint %q not found", endpoint))
					return
				}
				r = p.RequestWithEndpointHint(r, endpoint)
			}
			p.ServeHealth(w, r)
		case "explain":
			if endpoint != "" {
				if !p.EndpointExists(endpoint) {
					p.WriteError(w, http.StatusNotFound, fmt.Sprintf("endpoint %q not found", endpoint))
					return
				}
				r = p.RequestWithEndpointHint(r, endpoint)
			}
			p.ServeExplain(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func parseEndpointRoute(path string) (string, string, bool) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		route := strings.ToLower(parts[0])
		switch route {
		case "auth":
			return "", route, true
		case "health", "healthz":
			return "", "healthz", true
		case "explain":
			return "", route, true
		}
	case 2:
		route := strings.ToLower(parts[1])
		switch route {
		case "auth":
			return parts[0], route, true
		case "health", "healthz":
			return parts[0], "healthz", true
		case "explain":
			return parts[0], route, true
		}
	}
	return "", "", false
}
