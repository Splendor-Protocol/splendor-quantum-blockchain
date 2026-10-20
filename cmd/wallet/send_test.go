package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func TestFetchNonceRequiresHTTP200AndNonceField(t *testing.T) {
	var addr qcrypto.Address
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nonce/" + addr.String():
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("error code: 1010"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_, err := fetchNonce(srv.Client(), srv.URL, addr)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("cloudflare-style 403 must fail, got %v", err)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"address":"x"}`))
	}))
	defer ok.Close()
	_, err = fetchNonce(ok.Client(), ok.URL, addr)
	if err == nil || !strings.Contains(err.Error(), "missing nonce") {
		t.Fatalf("body without nonce must fail, got %v", err)
	}

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"nonce":7}`))
	}))
	defer good.Close()
	n, err := fetchNonce(good.Client(), good.URL, addr)
	if err != nil || n != 7 {
		t.Fatalf("got nonce %d err %v", n, err)
	}
}

func TestSubmitErrorUsesHTTPStatus(t *testing.T) {
	if err := submitError(http.StatusForbidden, []byte("error code: 1010")); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("403 HTML must be a rejection, got %v", err)
	}
	if err := submitError(http.StatusBadRequest, []byte(`{"error":"insufficient balance"}`)); err == nil || !strings.Contains(err.Error(), "insufficient balance") {
		t.Fatalf("JSON error: %v", err)
	}
	if err := submitError(http.StatusAccepted, []byte(`{"status":"accepted"}`)); err != nil {
		t.Fatalf("202 is success: %v", err)
	}
}
