package main

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

func TestFetchBalanceUnitsParsesNodeResponse(t *testing.T) {
	var addr qcrypto.Address
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/balance/" + addr.String():
			_, _ = w.Write([]byte(`{"balance":"123456789"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := fetchBalanceUnits(srv.URL, addr)
	if err != nil {
		t.Fatalf("fetchBalanceUnits: %v", err)
	}
	if got.Cmp(big.NewInt(123456789)) != 0 {
		t.Fatalf("balance = %s, want 123456789", got)
	}
}

func TestFetchBalanceUnitsPropagatesNodeError(t *testing.T) {
	var addr qcrypto.Address
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"account not found"}`))
	}))
	defer srv.Close()

	if _, err := fetchBalanceUnits(srv.URL, addr); err == nil || !strings.Contains(err.Error(), "account not found") {
		t.Fatalf("expected node error to propagate, got %v", err)
	}
}

func TestFetchBalanceUnitsRejectsMalformedBalance(t *testing.T) {
	var addr qcrypto.Address
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"balance":"not-a-number"}`))
	}))
	defer srv.Close()

	if _, err := fetchBalanceUnits(srv.URL, addr); err == nil {
		t.Fatal("malformed balance accepted")
	}
}

// doMigrateTransfer must move exactly (balance - fee), reject when the
// balance cannot cover the fee, and use the OLD wallet's key to sign a
// transfer addressed to the NEW wallet — never the reverse, which would move
// funds nowhere useful.
func TestDoMigrateTransferMovesBalanceMinusFee(t *testing.T) {
	oldPub, oldPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := qcrypto.DeriveAddress(oldPub)
	newPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	newAddr := qcrypto.DeriveAddress(newPub)

	var submitted types.Transaction
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/balance/"+oldAddr.String():
			_, _ = w.Write([]byte(`{"balance":"1000000"}`))
		case r.URL.Path == "/nonce/"+oldAddr.String():
			_, _ = w.Write([]byte(`{"nonce":0}`))
		case r.URL.Path == "/tx/submit":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Fatalf("decode submitted tx: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"accepted"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := doMigrateTransfer(srv.URL, oldAddr, oldPriv, newAddr, 1000); err != nil {
		t.Fatalf("doMigrateTransfer: %v", err)
	}
	if submitted.From != oldAddr {
		t.Fatalf("transfer From = %v, want the OLD address", submitted.From)
	}
	if submitted.To != newAddr {
		t.Fatalf("transfer To = %v, want the NEW address", submitted.To)
	}
	if submitted.Amount == nil || submitted.Amount.Cmp(big.NewInt(1000000-1000)) != 0 {
		t.Fatalf("transfer amount = %v, want balance-fee = 999000", submitted.Amount)
	}
}

// A balance that cannot even cover the fee must refuse before ever building
// or submitting a transaction.
func TestDoMigrateTransferRefusesWhenBalanceBelowFee(t *testing.T) {
	oldPub, oldPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := qcrypto.DeriveAddress(oldPub)
	newPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	newAddr := qcrypto.DeriveAddress(newPub)

	submitted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/balance/"+oldAddr.String():
			_, _ = w.Write([]byte(`{"balance":"500"}`)) // less than the fee
		case r.URL.Path == "/tx/submit":
			submitted = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := doMigrateTransfer(srv.URL, oldAddr, oldPriv, newAddr, 1000); err == nil {
		t.Fatal("expected refusal when balance cannot cover the fee")
	}
	if submitted {
		t.Fatal("a transaction was submitted despite an insufficient balance")
	}
}

// A rejected submission (e.g. node-side validation failure) must surface as
// an error, not be reported as a successful migration.
func TestDoMigrateTransferPropagatesSubmitRejection(t *testing.T) {
	oldPub, oldPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := qcrypto.DeriveAddress(oldPub)
	newPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	newAddr := qcrypto.DeriveAddress(newPub)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/balance/"+oldAddr.String():
			_, _ = w.Write([]byte(`{"balance":"1000000"}`))
		case r.URL.Path == "/nonce/"+oldAddr.String():
			_, _ = w.Write([]byte(`{"nonce":0}`))
		case r.URL.Path == "/tx/submit":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"nonce too low"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	err = doMigrateTransfer(srv.URL, oldAddr, oldPriv, newAddr, 1000)
	if err == nil || !strings.Contains(err.Error(), "nonce too low") {
		t.Fatalf("expected the node rejection to surface, got %v", err)
	}
}
