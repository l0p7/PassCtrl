package admission

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

// Config describes the admission rules for an endpoint.
type Config struct {
	Required  bool
	Allow     AllowConfig
	Challenge ChallengeConfig
}

// AllowConfig lists the credential providers accepted by the endpoint.
type AllowConfig struct {
	Authorization []string
	Header        []string
	Query         []string
	None          bool
}

// ChallengeConfig controls the WWW-Authenticate response emitted on failure.
type ChallengeConfig struct {
	Type    string
	Realm   string
	Charset string
}

type Agent struct {
	trustedNetworks []netip.Prefix
	developmentMode bool
	cfg             Config
}

func New(trusted []netip.Prefix, development bool, cfg Config) *Agent {
	return &Agent{
		trustedNetworks: trusted,
		developmentMode: development,
		cfg:             sanitizeConfig(cfg),
	}
}

func (a *Agent) Name() string { return "admission" }

func (a *Agent) Execute(_ context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	state.Admission.CapturedAt = time.Now().UTC()
	state.Admission.TrustedProxy = false
	state.Admission.ProxyStripped = false
	state.Admission.ProxyNote = ""
	state.Admission.ForwardedFor = strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	state.Admission.Forwarded = strings.TrimSpace(r.Header.Get("Forwarded"))
	state.Admission.ClientIP = remoteHost(r.RemoteAddr)
	state.Admission.Decision = ""
	state.Admission.Snapshot = nil
	state.Admission.Allow = admissionAllowSnapshot(a.cfg.Allow)
	state.Admission.Credentials = nil

	if state.Admission.ForwardedFor != "" || state.Admission.Forwarded != "" {
		addr, err := parseRemoteIP(r.RemoteAddr)
		if err != nil {
			state.Admission.Authenticated = false
			state.Admission.Reason = "invalid remote address"
			return a.finish(state, "fail")
		}

		chain, err := a.prepareForwardedMetadata(r, state)
		if err != nil {
			if !a.handleUntrustedProxy(r, state, err.Error(), "forwarded headers stripped due to invalid metadata") {
				return a.finish(state, "fail")
			}
		} else if !a.forwardedChainTrusted(chain) {
			if !a.handleUntrustedProxy(r, state, "forwarded chain includes untrusted proxy", "forwarded headers stripped due to untrusted proxy chain") {
				return a.finish(state, "fail")
			}
		} else if !a.isTrusted(addr) {
			if !a.handleUntrustedProxy(r, state, "untrusted proxy rejected", "forwarded headers stripped from untrusted proxy") {
				return a.finish(state, "fail")
			}
		} else if len(chain) > 0 {
			state.Admission.TrustedProxy = true
			state.Admission.ClientIP = chain[0].String()
		}
	}

	matches := a.collectCredentials(r)
	state.Admission.Credentials = matches

	if len(matches) > 0 {
		state.Admission.Authenticated = true
		state.Admission.Reason = annotateReason("authentication requirements satisfied", state.Admission.ProxyNote)
	} else {
		if a.cfg.Required {
			state.Admission.Authenticated = false
			state.Admission.Reason = annotateReason("no allowed credentials present", state.Admission.ProxyNote)
			if header := a.challengeHeader(); header != "" {
				if state.Response.Headers == nil {
					state.Response.Headers = make(map[string]string)
				}
				state.Response.Headers["WWW-Authenticate"] = header
				if state.Response.Status == 0 {
					state.Response.Status = http.StatusUnauthorized
				}
				if strings.TrimSpace(state.Response.Message) == "" {
					state.Response.Message = "authentication required"
				}
			}
		} else {
			state.Admission.Authenticated = true
			state.Admission.Reason = annotateReason("optional authentication not provided", state.Admission.ProxyNote)
		}
	}

	return a.finish(state, "")
}

func (a *Agent) finish(state *pipeline.State, status string) pipeline.Result {
	decision := "fail"
	if state.Admission.Authenticated {
		decision = "pass"
	}
	state.Admission.Decision = decision
	state.Admission.Snapshot = map[string]any{
		"decision":      decision,
		"authenticated": state.Admission.Authenticated,
		"reason":        state.Admission.Reason,
		"clientIp":      state.Admission.ClientIP,
		"trustedProxy":  state.Admission.TrustedProxy,
		"proxyStripped": state.Admission.ProxyStripped,
		"forwardedFor":  state.Admission.ForwardedFor,
		"forwarded":     state.Admission.Forwarded,
		"proxyNote":     state.Admission.ProxyNote,
		"capturedAt":    state.Admission.CapturedAt,
		"allow": map[string]any{
			"authorization": append([]string{}, state.Admission.Allow.Authorization...),
			"header":        append([]string{}, state.Admission.Allow.Header...),
			"query":         append([]string{}, state.Admission.Allow.Query...),
			"none":          state.Admission.Allow.None,
		},
		"credentials": cloneAdmissionCredentials(state.Admission.Credentials),
	}
	if status == "" {
		status = decision
	}
	return pipeline.Result{
		Name:    a.Name(),
		Status:  status,
		Details: state.Admission.Reason,
		Meta: map[string]any{
			"clientIp":      state.Admission.ClientIP,
			"trustedProxy":  state.Admission.TrustedProxy,
			"proxyStripped": state.Admission.ProxyStripped,
			"decision":      decision,
		},
	}
}

func (a *Agent) handleUntrustedProxy(r *http.Request, state *pipeline.State, reason, note string) bool {
	if a.developmentMode {
		state.Admission.ProxyStripped = true
		state.Admission.ProxyNote = note
		stripForwardedHeaders(r)
		state.Admission.ForwardedFor = ""
		state.Admission.Forwarded = ""
		return true
	}

	state.Admission.Authenticated = false
	state.Admission.Reason = reason
	state.Admission.ProxyStripped = false
	state.Admission.ProxyNote = note
	return false
}

func (a *Agent) prepareForwardedMetadata(r *http.Request, state *pipeline.State) ([]netip.Addr, error) {
	var canonical []netip.Addr

	if state.Admission.Forwarded != "" {
		chain, sanitized, err := parseRFC7239Forwarded(state.Admission.Forwarded)
		if err != nil {
			if errors.Is(err, errForwardedChainMissing) || errors.Is(err, errForwardedDirectiveEmpty) {
				return nil, err
			}
			return nil, errForwardedChainInvalid
		}
		if len(chain) == 0 {
			return nil, errForwardedChainMissing
		}
		state.Admission.Forwarded = sanitized
		r.Header.Set("Forwarded", sanitized)
		canonical = chain
	} else {
		r.Header.Del("Forwarded")
	}

	if state.Admission.ForwardedFor != "" {
		chain, err := parseForwardedChain(state.Admission.ForwardedFor)
		if err != nil {
			return nil, errForwardedChainInvalid
		}
		if len(chain) == 0 {
			return nil, errForwardedChainMissing
		}
		sanitized := joinForwardedAddrs(chain)
		state.Admission.ForwardedFor = sanitized
		r.Header.Set("X-Forwarded-For", sanitized)
		if len(canonical) == 0 {
			canonical = chain
		} else if !forwardedChainsEqual(canonical, chain) {
			return nil, errForwardedMetadata
		}
	} else {
		r.Header.Del("X-Forwarded-For")
	}

	if len(canonical) == 0 {
		return nil, errForwardedChainMissing
	}
	return canonical, nil
}

func (a *Agent) forwardedChainTrusted(chain []netip.Addr) bool {
	if len(chain) <= 1 {
		return true
	}
	for _, hop := range chain[1:] {
		if !a.isTrusted(hop) {
			return false
		}
	}
	return true
}

func (a *Agent) isTrusted(addr netip.Addr) bool {
	if len(a.trustedNetworks) == 0 {
		return false
	}
	for _, network := range a.trustedNetworks {
		if network.Contains(addr) {
			return true
		}
	}
	return false
}

func stripForwardedHeaders(r *http.Request) {
	for name := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			r.Header.Del(name)
		}
	}
}

func sanitizeConfig(cfg Config) Config {
	out := cfg
	out.Allow.Authorization = sanitizeAuthorizationList(cfg.Allow.Authorization)
	out.Allow.Header = sanitizeList(cfg.Allow.Header)
	out.Allow.Query = sanitizeList(cfg.Allow.Query)
	out.Challenge.Type = strings.ToLower(strings.TrimSpace(cfg.Challenge.Type))
	out.Challenge.Realm = strings.TrimSpace(cfg.Challenge.Realm)
	out.Challenge.Charset = strings.TrimSpace(cfg.Challenge.Charset)
	return out
}

func sanitizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func sanitizeAuthorizationList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (a *Agent) collectCredentials(r *http.Request) []pipeline.AdmissionCredential {
	var matches []pipeline.AdmissionCredential

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, param := parseAuthorization(authHeader)

	if allowsAuthorization(a.cfg.Allow.Authorization, "basic") && strings.EqualFold(scheme, "basic") {
		if user, pass, ok := decodeBasicCredential(param); ok {
			matches = append(matches, pipeline.AdmissionCredential{
				Type:     "basic",
				Username: user,
				Password: pass,
				Source:   "authorization",
			})
		}
	}

	if allowsAuthorization(a.cfg.Allow.Authorization, "bearer") && strings.EqualFold(scheme, "bearer") {
		token := strings.TrimSpace(param)
		if token != "" {
			matches = append(matches, pipeline.AdmissionCredential{
				Type:   "bearer",
				Token:  token,
				Source: "authorization",
			})
		}
	}

	if len(a.cfg.Allow.Header) > 0 {
		for _, name := range a.cfg.Allow.Header {
			if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
				matches = append(matches, pipeline.AdmissionCredential{
					Type:   "header",
					Name:   name,
					Value:  value,
					Source: fmt.Sprintf("header:%s", name),
				})
			}
		}
	}

	if len(a.cfg.Allow.Query) > 0 && r.URL != nil {
		values := r.URL.Query()
		for _, name := range a.cfg.Allow.Query {
			if value := strings.TrimSpace(values.Get(name)); value != "" {
				matches = append(matches, pipeline.AdmissionCredential{
					Type:   "query",
					Name:   name,
					Value:  value,
					Source: fmt.Sprintf("query:%s", name),
				})
			}
		}
	}

	if a.cfg.Allow.None {
		matches = append(matches, pipeline.AdmissionCredential{
			Type:   "none",
			Source: "anonymous",
		})
	}

	return matches
}

func parseAuthorization(header string) (string, string) {
	if header == "" {
		return "", ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func decodeBasicCredential(payload string) (string, string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return "", "", false
	}
	creds := string(decoded)
	if creds == "" {
		return "", "", false
	}
	parts := strings.SplitN(creds, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (a *Agent) challengeHeader() string {
	if a.cfg.Challenge.Type == "" || a.cfg.Challenge.Realm == "" {
		return ""
	}
	switch a.cfg.Challenge.Type {
	case "basic":
		header := fmt.Sprintf(`Basic realm="%s"`, escapeChallengeValue(a.cfg.Challenge.Realm))
		if a.cfg.Challenge.Charset != "" {
			header = fmt.Sprintf(`%s, charset="%s"`, header, escapeChallengeValue(a.cfg.Challenge.Charset))
		}
		return header
	case "bearer":
		return fmt.Sprintf(`Bearer realm="%s"`, escapeChallengeValue(a.cfg.Challenge.Realm))
	default:
		return ""
	}
}

func escapeChallengeValue(in string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(in)
}

func admissionAllowSnapshot(cfg AllowConfig) pipeline.AdmissionAllow {
	return pipeline.AdmissionAllow{
		Authorization: append([]string{}, cfg.Authorization...),
		Header:        append([]string{}, cfg.Header...),
		Query:         append([]string{}, cfg.Query...),
		None:          cfg.None,
	}
}

func cloneAdmissionCredentials(in []pipeline.AdmissionCredential) []pipeline.AdmissionCredential {
	if len(in) == 0 {
		return nil
	}
	out := make([]pipeline.AdmissionCredential, len(in))
	copy(out, in)
	return out
}

func allowsAuthorization(list []string, kind string) bool {
	for _, value := range list {
		if value == kind {
			return true
		}
	}
	return false
}

var (
	errForwardedChainMissing   = errors.New("forwarded chain missing client hop")
	errForwardedChainInvalid   = errors.New("invalid forwarded chain")
	errForwardedMetadata       = errors.New("forwarded metadata mismatch between headers")
	errForwardedDirectiveEmpty = errors.New("forwarded metadata missing for directive")
)

func parseRFC7239Forwarded(header string) ([]netip.Addr, string, error) {
	parts := strings.Split(header, ",")
	addrs := make([]netip.Addr, 0, len(parts))
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		params := strings.Split(trimmed, ";")
		sanitized := make([]string, 0, len(params))
		var foundFor bool
		for _, param := range params {
			fragment := strings.TrimSpace(param)
			if fragment == "" {
				continue
			}
			kv := strings.SplitN(fragment, "=", 2)
			if len(kv) != 2 {
				sanitized = append(sanitized, fragment)
				continue
			}
			key := strings.ToLower(strings.TrimSpace(kv[0]))
			value := strings.TrimSpace(kv[1])
			if key != "for" {
				sanitized = append(sanitized, key+"="+value)
				continue
			}
			addr, normalized, err := parseRFC7239ForValue(value)
			if err != nil {
				return nil, "", err
			}
			addrs = append(addrs, addr)
			sanitized = append(sanitized, "for="+normalized)
			foundFor = true
		}
		if !foundFor {
			return nil, "", errForwardedDirectiveEmpty
		}
		segments = append(segments, strings.Join(sanitized, "; "))
	}
	if len(addrs) == 0 {
		return nil, "", errForwardedChainMissing
	}
	return addrs, strings.Join(segments, ", "), nil
}

func parseRFC7239ForValue(raw string) (netip.Addr, string, error) {
	trimmed := strings.TrimSpace(raw)
	quoted := false
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
		quoted = true
	}
	if trimmed == "" {
		return netip.Addr{}, "", errForwardedChainMissing
	}
	if strings.HasPrefix(trimmed, "_") || strings.EqualFold(trimmed, "unknown") {
		return netip.Addr{}, "", errForwardedDirectiveEmpty
	}
	addr, err := parseForwardedEntry(trimmed)
	if err != nil {
		return netip.Addr{}, "", err
	}
	needsQuotes := quoted || strings.ContainsAny(trimmed, ":[]; ")
	if needsQuotes {
		return addr, "\"" + trimmed + "\"", nil
	}
	return addr, trimmed, nil
}

func parseForwardedChain(header string) ([]netip.Addr, error) {
	parts := strings.Split(header, ",")
	addrs := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		addr, err := parseForwardedEntry(trimmed)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func parseForwardedEntry(value string) (netip.Addr, error) {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr, nil
	}
	if strings.Contains(value, ":") {
		if addrPort, err := netip.ParseAddrPort(value); err == nil {
			return addrPort.Addr(), nil
		}
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return netip.ParseAddr(host)
	}
	return netip.Addr{}, net.InvalidAddrError("invalid forwarded entry")
}

func joinForwardedAddrs(addrs []netip.Addr) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		parts = append(parts, addr.String())
	}
	return strings.Join(parts, ", ")
}

func forwardedChainsEqual(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func annotateReason(base, note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return base
	}
	return base + " (" + note + ")"
}

func remoteHost(addr string) string {
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func parseRemoteIP(addr string) (netip.Addr, error) {
	host := remoteHost(addr)
	if host == "" {
		return netip.Addr{}, net.InvalidAddrError("empty remote address")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return ip, nil
}

func ParseCIDRs(cidrs []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}
