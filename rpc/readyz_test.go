package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadyzReportsFieldsAndStatus(t *testing.T) {
	h := makeReadyzHandler(func() ReadyStatus {
		return ReadyStatus{Ready: false, Reason: "recovery in progress", AppliedHeight: 3, Recovering: true}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
	var st ReadyStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Ready || st.Reason != "recovery in progress" || st.AppliedHeight != 3 {
		t.Fatalf("body = %+v", st)
	}

	ok := makeReadyzHandler(func() ReadyStatus {
		return ReadyStatus{Ready: true, AppliedHeight: 4, IndexHeight: 4, IndexKnown: true}
	})
	rec = httptest.NewRecorder()
	ok.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready code = %d", rec.Code)
	}
}

func TestHealthzStaysLiveness(t *testing.T) {
	h := makeHealthzHandler(time.Now())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" {
		t.Fatalf("healthz = %v", out)
	}
	if _, ok := out["applied_height"]; ok {
		t.Fatal("liveness must not require applied height")
	}
}
