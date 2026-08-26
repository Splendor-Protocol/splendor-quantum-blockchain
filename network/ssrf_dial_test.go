package network

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSafeTransportRefusesHostResolvingToInternal(t *testing.T) {
	// Point a hostname at loopback through the transport's own resolution path
	// by using a name the system resolver will fail on, then assert we fail
	// closed rather than fall through to a dial.
	tr := SafeTransport(2 * time.Second)
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}

	_, err := client.Get("http://metadata.google.internal./")
	if err == nil {
		t.Fatal("expected dial to be refused or fail, got success")
	}
}

func TestSafeTransportAllowsIPLiteralLoopback(t *testing.T) {
	// IP literals cannot rebind: IsSafePeerURL already vetted the exact address.
	// Operator clusters on internal literals and loopback test servers depend on
	// this passing through untouched.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: SafeTransport(2 * time.Second), Timeout: 3 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("IP-literal dial should pass through, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSafeTransportHonoursAllowPrivatePeers(t *testing.T) {
	prev := AllowPrivatePeers
	AllowPrivatePeers = true
	defer func() { AllowPrivatePeers = prev }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split test server addr: %v", err)
	}

	client := &http.Client{Transport: SafeTransport(2 * time.Second), Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:" + port + "/")
	if err != nil {
		t.Fatalf("private peers allowed, hostname dial should succeed: %v", err)
	}
	defer resp.Body.Close()
}

func TestSafeTransportBlocksHostnameToLoopbackByDefault(t *testing.T) {
	prev := AllowPrivatePeers
	AllowPrivatePeers = false
	defer func() { AllowPrivatePeers = prev }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split test server addr: %v", err)
	}

	client := &http.Client{Transport: SafeTransport(2 * time.Second), Timeout: 3 * time.Second}
	_, err = client.Get("http://localhost:" + port + "/")
	if err == nil {
		t.Fatal("hostname resolving to loopback must be refused at dial time")
	}
	if !strings.Contains(err.Error(), "internal address") {
		t.Fatalf("want internal-address refusal, got %v", err)
	}
}
