package netutil

import (
	"net"
	"net/http"
	"strings"
)

var (
	carrierGradeNATRange = mustParseCIDR("100.64.0.0/10")
	benchmarkRange       = mustParseCIDR("198.18.0.0/15")
	documentationRanges  = []*net.IPNet{
		mustParseCIDR("192.0.2.0/24"),
		mustParseCIDR("198.51.100.0/24"),
		mustParseCIDR("203.0.113.0/24"),
	}
)

// ExtractClientIP resolves the most likely end-user IP across common proxy chains.
func ExtractClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}

	for _, header := range []string{"CF-Connecting-IP", "True-Client-IP"} {
		if ip := parseSingleIP(r.Header.Get(header)); ip != nil {
			return ip.String()
		}
	}

	if ip := parseForwardedHeaderIP(r.Header.Get("Forwarded")); ip != nil {
		return ip.String()
	}
	if ip := parseForwardedListIP(r.Header.Get("X-Forwarded-For")); ip != nil {
		return ip.String()
	}
	if ip := parseSingleIP(r.Header.Get("X-Real-IP")); ip != nil {
		return ip.String()
	}

	if ip := parseSingleIP(r.RemoteAddr); ip != nil {
		return ip.String()
	}
	return "unknown"
}

// NormalizeIP converts an IP-ish value to canonical string form.
func NormalizeIP(raw string) string {
	if ip := parseSingleIP(raw); ip != nil {
		return ip.String()
	}
	return ""
}

// IsPublicIP reports whether a string resolves to a publicly routable IP.
func IsPublicIP(raw string) bool {
	return isPublicRoutableIP(parseSingleIP(raw))
}

func parseForwardedListIP(raw string) net.IP {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	candidates := make([]net.IP, 0, 4)
	for _, part := range strings.Split(trimmed, ",") {
		if ip := parseSingleIP(part); ip != nil {
			candidates = append(candidates, ip)
		}
	}
	return selectBestIP(candidates)
}

func parseForwardedHeaderIP(raw string) net.IP {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	candidates := make([]net.IP, 0, 4)
	for _, entry := range strings.Split(trimmed, ",") {
		segments := strings.Split(entry, ";")
		for _, segment := range segments {
			field := strings.TrimSpace(segment)
			if len(field) < 4 {
				continue
			}
			if !strings.EqualFold(field[:4], "for=") {
				continue
			}
			value := strings.TrimSpace(field[4:])
			value = strings.Trim(value, `"`)
			if strings.HasPrefix(value, "_") || strings.EqualFold(value, "unknown") {
				continue
			}
			if ip := parseSingleIP(value); ip != nil {
				candidates = append(candidates, ip)
			}
		}
	}
	return selectBestIP(candidates)
}

func selectBestIP(candidates []net.IP) net.IP {
	if len(candidates) == 0 {
		return nil
	}
	var first net.IP
	for _, ip := range candidates {
		if ip == nil {
			continue
		}
		if first == nil {
			first = ip
		}
		if isPublicRoutableIP(ip) {
			return ip
		}
	}
	return first
}

func parseSingleIP(raw string) net.IP {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	trimmed = strings.Trim(trimmed, `"`)
	if strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]") {
		if host, _, err := net.SplitHostPort(trimmed); err == nil {
			trimmed = host
		}
	} else if host, _, err := net.SplitHostPort(trimmed); err == nil && strings.TrimSpace(host) != "" {
		trimmed = host
	}

	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	if zoneIndex := strings.Index(trimmed, "%"); zoneIndex > 0 {
		trimmed = trimmed[:zoneIndex]
	}

	ip := net.ParseIP(strings.TrimSpace(trimmed))
	if ip == nil {
		return nil
	}
	return ip
}

func isPublicRoutableIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsLinkLocalUnicast() {
		return false
	}
	if carrierGradeNATRange.Contains(ip) || benchmarkRange.Contains(ip) {
		return false
	}
	for _, network := range documentationRanges {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func mustParseCIDR(value string) *net.IPNet {
	_, network, err := net.ParseCIDR(strings.TrimSpace(value))
	if err != nil {
		panic("invalid CIDR: " + value)
	}
	return network
}
