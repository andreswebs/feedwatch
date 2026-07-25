package fetch

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/andreswebs/feedwatch/internal/core"
)

// maxRedirects bounds a single fetch's redirect chain. Installing a custom
// CheckRedirect replaces net/http's default 10-hop limit, so the guard enforces
// its own.
const maxRedirects = 10

// cgnatNet is the carrier-grade NAT range (RFC 6598), which net.IP.IsPrivate
// does not cover.
var cgnatNet = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// isPrivate reports whether an IP belongs to a private, loopback, link-local,
// unique-local, unspecified, or carrier-grade-NAT range. These are the
// addresses an SSRF guard must keep a public feed from redirecting into.
func isPrivate(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && cgnatNet.Contains(v4) {
		return true
	}
	return false
}

// redirectState is per-fetch mutable state carried through the request context
// so the concurrently-reused Client never shares it across calls. checkRedirect
// records whether the final hop was reached via a permanent redirect.
type redirectState struct {
	permanent bool
}

type ctxKey int

const redirectStateKey ctxKey = iota

// resolver maps a host (an IP literal or a name) to its IP addresses. It is a
// seam so tests can classify hosts without real DNS.
type resolver func(ctx context.Context, host string) ([]net.IP, error)

// defaultResolve resolves IP literals to themselves and names via the system
// resolver. It is the production resolver used by every Client.
func defaultResolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

// ssrfGuard implements the redirect policy: a directly-supplied URL is dialed
// as given (so a self-hosted LAN reader is reachable), but a public URL is never
// allowed to redirect into private address space, and every hop is re-resolved
// and re-checked. allowPrivate lifts the redirect restriction entirely.
type ssrfGuard struct {
	allowPrivate bool
	resolve      resolver
}

// checkRedirect is the net/http CheckRedirect hook. It records the permanent
// flag for the result, enforces the redirect limit, and blocks a
// public-to-private redirect unless allowPrivate is set.
func (g *ssrfGuard) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return core.NetworkErr(originURL(via), fmt.Errorf("stopped after %d redirects", maxRedirects))
	}

	if rs, ok := req.Context().Value(redirectStateKey).(*redirectState); ok && req.Response != nil {
		switch req.Response.StatusCode {
		case http.StatusMovedPermanently, http.StatusPermanentRedirect:
			rs.permanent = true
		default:
			rs.permanent = false
		}
	}

	if g.allowPrivate {
		return nil
	}

	originPrivate, err := g.hostPrivate(req.Context(), via[0].URL.Hostname())
	if err != nil {
		return core.NetworkErr(via[0].URL.String(), fmt.Errorf("resolving redirect origin: %w", err))
	}
	if originPrivate {
		return nil
	}

	targetPrivate, err := g.hostPrivate(req.Context(), req.URL.Hostname())
	if err != nil {
		return core.NetworkErr(req.URL.String(), fmt.Errorf("resolving redirect target: %w", err))
	}
	if targetPrivate {
		return &core.FeedError{
			FeedURL:  via[0].URL.String(),
			Category: core.CatNetwork,
			Message:  "blocked redirect from public host into private address space: " + req.URL.Host,
		}
	}
	return nil
}

// hostPrivate resolves host and reports whether any of its addresses is in a
// private range. Treating a host as private when any single address is private
// is the conservative choice against DNS that mixes public and private answers.
func (g *ssrfGuard) hostPrivate(ctx context.Context, host string) (bool, error) {
	ips, err := g.resolve(ctx, host)
	if err != nil {
		return false, err
	}
	for _, ip := range ips {
		if isPrivate(ip) {
			return true, nil
		}
	}
	return false, nil
}

// originURL returns the original request's URL for error reporting, tolerating
// an empty chain.
func originURL(via []*http.Request) string {
	if len(via) > 0 && via[0] != nil && via[0].URL != nil {
		return via[0].URL.String()
	}
	return ""
}
