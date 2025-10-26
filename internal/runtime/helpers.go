package runtime

import (
	"net/http"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/admission"
)

// cacheKeyFromRequest derives a stable cache key by extracting the credential
// from the request using the endpoint's authentication configuration.
// This mirrors the admission agent's credential extraction logic to ensure
// cache keys properly isolate different credentials (preventing cache poisoning).
//
// Priority order (same as admission):
//  1. Authorization header (if allowed)
//  2. Custom headers (if allowed)
//  3. Query parameters (if allowed)
//  4. None/anonymous (handled by caller via empty cache key)
func cacheKeyFromRequest(r *http.Request, endpoint string, authCfg *admission.Config) string {
	if r == nil || authCfg == nil {
		return ""
	}

	credential := extractCredential(r, authCfg)
	return credential + "|" + endpoint + "|" + r.URL.Path
}

// extractCredential mirrors admission agent's credential extraction logic.
func extractCredential(r *http.Request, authCfg *admission.Config) string {
	// 1. Check Authorization header (if allowed)
	if len(authCfg.Allow.Authorization) > 0 {
		if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
			return "auth:" + authHeader
		}
	}

	// 2. Check custom headers (if allowed)
	for _, headerName := range authCfg.Allow.Header {
		if headerValue := strings.TrimSpace(r.Header.Get(headerName)); headerValue != "" {
			return "header:" + headerName + ":" + headerValue
		}
	}

	// 3. Check query parameters (if allowed)
	query := r.URL.Query()
	for _, paramName := range authCfg.Allow.Query {
		if paramValue := query.Get(paramName); paramValue != "" {
			return "query:" + paramName + ":" + paramValue
		}
	}

	// 4. No credential found - use IP address as fallback
	// Note: This handles non-none endpoints that don't have credentials yet
	return "ip:" + r.RemoteAddr
}
