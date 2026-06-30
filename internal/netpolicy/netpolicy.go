package netpolicy

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
)

type Policy struct {
	AllowedDomains []string
	AllowedCIDRs   []string
}

type NormalizedPolicy struct {
	Domains []string
	CIDRs   []netip.Prefix
}

func NormalizePolicy(p Policy) (NormalizedPolicy, error) {
	out := NormalizedPolicy{}
	seen := map[string]struct{}{}
	for _, raw := range p.AllowedDomains {
		d := NormalizeDomain(raw)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			out.Domains = append(out.Domains, d)
		}
	}
	for _, raw := range p.AllowedCIDRs {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			addr, addrErr := netip.ParseAddr(s)
			if addrErr != nil {
				return out, err
			}
			bits := 128
			if addr.Is4() {
				bits = 32
			}
			prefix = netip.PrefixFrom(addr, bits)
		}
		out.CIDRs = append(out.CIDRs, prefix.Masked())
	}
	return out, nil
}

func NormalizeDomain(raw string) string {
	s := strings.TrimSpace(strings.ToLower(StripURLUserinfo(raw)))
	if s == "" || s == "*" || strings.HasPrefix(s, "*.") {
		return s
	}
	if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
		s = u.Hostname()
	} else if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.TrimSuffix(strings.Trim(s, "[]"), ".")
}

func StripURLUserinfo(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value
	}
	u.User = nil
	return u.String()
}

func RedactSensitiveURL(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactUserinfoText(value)
	}
	u.User = nil
	q := u.Query()
	for key := range q {
		if sensitiveQueryName(key) {
			q.Set(key, "REDACTED")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func redactUserinfoText(value string) string {
	if i := strings.Index(value, "://"); i >= 0 {
		rest := value[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			return value[:i+3] + rest[at+1:]
		}
	}
	return value
}

func sensitiveQueryName(name string) bool {
	n := strings.ToLower(name)
	for _, token := range []string{"token", "secret", "key", "password", "passwd", "credential", "auth", "signature", "sig"} {
		if strings.Contains(n, token) {
			return true
		}
	}
	return false
}

func IsBlockedIP(raw string) bool {
	addr, ok := parseAddr(raw)
	if !ok {
		return false
	}
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || isBogon(addr)
}

func parseAddr(raw string) (netip.Addr, bool) {
	s := strings.Trim(strings.TrimSpace(raw), "[]")
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isBogon(addr netip.Addr) bool {
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "2001:db8::/32"} {
		if netip.MustParsePrefix(cidr).Contains(addr) {
			return true
		}
	}
	return false
}

func (p NormalizedPolicy) Allows(hostOrIP string) bool {
	host := NormalizeDomain(hostOrIP)
	if addr, ok := parseAddr(host); ok {
		if IsBlockedIP(addr.String()) {
			return false
		}
		for _, cidr := range p.CIDRs {
			if cidr.Contains(addr) {
				return true
			}
		}
		return false
	}
	for _, d := range p.Domains {
		if host == d || (strings.HasPrefix(d, "*.") && strings.HasSuffix(host, strings.TrimPrefix(d, "*"))) {
			return true
		}
	}
	return false
}
