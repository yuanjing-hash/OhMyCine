package pan115

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

type downloadIPResolver func(context.Context, string) ([]net.IPAddr, error)

func validatePublicDownloadURL(ctx context.Context, target *url.URL, resolve downloadIPResolver) error {
	if target == nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		return errors.New("115 download target is invalid")
	}
	if port := target.Port(); port != "" && port != "443" {
		return errors.New("115 download target port is invalid")
	}
	host := strings.TrimSpace(strings.TrimSuffix(target.Hostname(), "."))
	if host == "" {
		return errors.New("115 download target host is invalid")
	}
	addresses, err := resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		return errors.New("115 download target could not be resolved")
	}
	for _, address := range addresses {
		if !isPublicDownloadIP(address.IP) {
			return errors.New("115 download target resolved to a private address")
		}
	}
	return nil
}

func publicDownloadDialContext(resolve downloadIPResolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("115 download dial target is invalid")
		}
		addresses, err := resolve(ctx, strings.TrimSuffix(host, "."))
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("115 download dial target could not be resolved")
		}
		var last error
		for _, candidate := range addresses {
			if !isPublicDownloadIP(candidate.IP) {
				return nil, errors.New("115 download dial target resolved to a private address")
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			last = dialErr
		}
		if last == nil {
			last = errors.New("115 download dial target is unavailable")
		}
		return nil, last
	}
}

func isPublicDownloadIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, denied := range nonPublicDownloadPrefixes {
		if denied.Contains(address) {
			return false
		}
	}
	return true
}

var nonPublicDownloadPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // shared carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),   // documentation
	netip.MustParsePrefix("192.88.99.0/24"), // deprecated relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),      // discard-only
	netip.MustParsePrefix("2001:2::/48"),   // benchmarking
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}
