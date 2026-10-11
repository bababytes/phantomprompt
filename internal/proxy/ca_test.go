package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"
)

// The app keeps running in the background, so its MITM chain must stay valid past one day.
func TestCAStaysValidForLongUptime(t *testing.T) {
	ca, err := NewAuthority("test")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("api.anthropic.com")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate.Leaf)
	for _, later := range []time.Duration{25 * time.Hour, 30 * 24 * time.Hour} {
		if _, err := parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "api.anthropic.com", CurrentTime: time.Now().Add(later)}); err != nil {
			t.Fatalf("chain invalid after %v: %v", later, err)
		}
	}
}

func TestCertificateCacheRenewsExpiringCertificates(t *testing.T) {
	cache := &certificateCache{entries: make(map[string]*tls.Certificate)}
	issued := 0
	generate := func(validFor time.Duration) func() (*tls.Certificate, error) {
		return func() (*tls.Certificate, error) {
			issued++
			return &tls.Certificate{Leaf: &x509.Certificate{NotAfter: time.Now().Add(validFor)}}, nil
		}
	}
	if _, err := cache.Fetch("api.anthropic.com", generate(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Fetch("api.anthropic.com", generate(24*time.Hour)); err != nil || issued != 2 {
		t.Fatalf("certificate close to expiry was reused: issued %d", issued)
	}
	if _, err := cache.Fetch("api.anthropic.com", generate(24*time.Hour)); err != nil || issued != 2 {
		t.Fatalf("valid certificate was not reused: issued %d", issued)
	}
}
