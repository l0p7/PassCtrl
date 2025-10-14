package runtime

import "net/http"

// cacheKeyFromRequest derives a stable cache key so the same caller and path
// can be replayed without re-evaluating rules.
func cacheKeyFromRequest(r *http.Request, endpoint string) string {
	token := r.Header.Get("Authorization")
	if token == "" {
		token = r.RemoteAddr
	}
	return token + "|" + endpoint + "|" + r.URL.Path
}
