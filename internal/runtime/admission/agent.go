package admission

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

type Agent struct {
	trustedNetworks []netip.Prefix
	developmentMode bool
}

func New(trusted []netip.Prefix, development bool) *Agent {
	return &Agent{trustedNetworks: trusted, developmentMode: development}
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

	if token := r.Header.Get("Authorization"); token != "" {
		state.Admission.Authenticated = true
		state.Admission.Reason = annotateReason("authorization header accepted", state.Admission.ProxyNote)
	} else {
		state.Admission.Authenticated = false
		state.Admission.Reason = annotateReason("authorization header missing", state.Admission.ProxyNote)
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
