package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
)

func TestIsPrivateClassification(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},             // loopback
		{"::1", true},                   // loopback IPv6
		{"10.0.0.1", true},              // RFC1918
		{"172.16.5.4", true},            // RFC1918
		{"192.168.1.1", true},           // RFC1918
		{"169.254.10.1", true},          // link-local
		{"fe80::1", true},               // link-local IPv6
		{"fc00::1", true},               // unique-local IPv6
		{"100.64.0.1", true},            // CGNAT
		{"100.127.255.1", true},         // CGNAT upper
		{"0.0.0.0", true},               // unspecified
		{"::", true},                    // unspecified IPv6
		{"8.8.8.8", false},              // public
		{"203.0.113.10", false},         // public (TEST-NET-3)
		{"99.64.0.1", false},            // just below CGNAT
		{"100.128.0.1", false},          // just above CGNAT
		{"2606:4700:4700::1111", false}, // public IPv6
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", tc.ip)
		}
		if got := isPrivate(ip); got != tc.want {
			t.Errorf("isPrivate(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// fakeResolver maps hostnames to fixed IPs so the guard can be exercised without
// real DNS. An IP literal host resolves to itself.
func fakeResolver(m map[string][]net.IP) func(context.Context, string) ([]net.IP, error) {
	return func(_ context.Context, host string) ([]net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return []net.IP{ip}, nil
		}
		if ips, ok := m[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
}

func redirectRequest(t *testing.T, originURL, targetURL string, status int) (*http.Request, []*http.Request) {
	t.Helper()
	origin, err := http.NewRequest(http.MethodGet, originURL, nil)
	if err != nil {
		t.Fatalf("origin request: %v", err)
	}
	target, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatalf("target request: %v", err)
	}
	ctx := context.WithValue(context.Background(), redirectStateKey, &redirectState{})
	target = target.WithContext(ctx)
	target.Response = &http.Response{StatusCode: status}
	return target, []*http.Request{origin}
}

func TestCheckRedirectBlocksPublicToPrivate(t *testing.T) {
	g := &ssrfGuard{
		allowPrivate: false,
		resolve: fakeResolver(map[string][]net.IP{
			"public.example":   {net.ParseIP("203.0.113.10")},
			"internal.example": {net.ParseIP("127.0.0.1")},
		}),
	}
	req, via := redirectRequest(t, "http://public.example/", "http://internal.example/", http.StatusFound)

	err := g.checkRedirect(req, via)
	if err == nil {
		t.Fatal("checkRedirect: expected block, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatNetwork {
		t.Errorf("category = %q, want %q", fe.Category, core.CatNetwork)
	}
}

func TestCheckRedirectAllowPrivateOpensRedirect(t *testing.T) {
	g := &ssrfGuard{
		allowPrivate: true,
		resolve: fakeResolver(map[string][]net.IP{
			"public.example":   {net.ParseIP("203.0.113.10")},
			"internal.example": {net.ParseIP("127.0.0.1")},
		}),
	}
	req, via := redirectRequest(t, "http://public.example/", "http://internal.example/", http.StatusFound)

	if err := g.checkRedirect(req, via); err != nil {
		t.Fatalf("checkRedirect with allowPrivate: %v, want nil", err)
	}
}

func TestCheckRedirectPrivateOriginAllowsPrivateTarget(t *testing.T) {
	g := &ssrfGuard{
		allowPrivate: false,
		resolve: fakeResolver(map[string][]net.IP{
			"reader.lan": {net.ParseIP("192.168.1.10")},
			"other.lan":  {net.ParseIP("192.168.1.20")},
		}),
	}
	req, via := redirectRequest(t, "http://reader.lan/", "http://other.lan/", http.StatusFound)

	if err := g.checkRedirect(req, via); err != nil {
		t.Fatalf("private-origin redirect blocked: %v, want allowed", err)
	}
}

func TestCheckRedirectRecordsPermanent(t *testing.T) {
	g := &ssrfGuard{allowPrivate: true, resolve: fakeResolver(nil)}
	req, via := redirectRequest(t, "http://a.example/", "http://b.example/", http.StatusMovedPermanently)

	if err := g.checkRedirect(req, via); err != nil {
		t.Fatalf("checkRedirect: %v", err)
	}
	rs, _ := req.Context().Value(redirectStateKey).(*redirectState)
	if rs == nil || !rs.permanent {
		t.Errorf("permanent = %v, want true after 301 hop", rs)
	}
}

func TestCheckRedirectStopsAfterLimit(t *testing.T) {
	g := &ssrfGuard{allowPrivate: true, resolve: fakeResolver(nil)}
	target, err := http.NewRequest(http.MethodGet, "http://a.example/", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	via := make([]*http.Request, maxRedirects)
	if err := g.checkRedirect(target, via); err == nil {
		t.Error("checkRedirect: expected error after redirect limit, got nil")
	}
}
