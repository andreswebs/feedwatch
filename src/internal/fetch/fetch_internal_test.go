package fetch

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func transportOf(t *testing.T, f *Client) *http.Transport {
	t.Helper()
	tr, ok := f.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", f.client.Transport)
	}
	return tr
}

func TestMinTLSDefaultsToTLS12(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := transportOf(t, f).TLSClientConfig.MinVersion; got != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want %#x (TLS 1.2)", got, tls.VersionTLS12)
	}
}

func TestWithMinTLSSetsTransport(t *testing.T) {
	f, err := New(WithMinTLS(tls.VersionTLS13))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := transportOf(t, f).TLSClientConfig.MinVersion; got != tls.VersionTLS13 {
		t.Errorf("MinVersion = %#x, want %#x (TLS 1.3)", got, tls.VersionTLS13)
	}
}

func TestWithConnectTimeoutSetsDialer(t *testing.T) {
	f, err := New(WithConnectTimeout(2 * time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f.connectTimeout != 2*time.Second {
		t.Errorf("connectTimeout = %s, want 2s", f.connectTimeout)
	}
	if transportOf(t, f).DialContext == nil {
		t.Error("DialContext not configured")
	}
}

func TestWithCheckRedirectIsWired(t *testing.T) {
	called := false
	hook := func(*http.Request, []*http.Request) error {
		called = true
		return nil
	}
	f, err := New(WithCheckRedirect(hook))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f.client.CheckRedirect == nil {
		t.Fatal("CheckRedirect not wired into client")
	}
	_ = f.client.CheckRedirect(nil, nil)
	if !called {
		t.Error("CheckRedirect hook not the one provided")
	}
}
