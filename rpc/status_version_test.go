package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/buildinfo"
)

// node_version was the literal "1.0.0-quantum" in both handlers, so every build
// of every commit reported the same thing. It is now derived from the identity
// stamped at image build — the tag, else the commit — and both /status and
// /healthz must show exactly that, next to the raw commit and image_tag fields
// they already exposed, which are unchanged.
func TestNodeVersionIsTheBuildIdentity(t *testing.T) {
	s := newTestServer(t, "")
	healthz := makeHealthzHandler(time.Now())

	decode := func(h http.Handler, path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}

	prevCommit, prevTag := buildinfo.Commit, buildinfo.ImageTag
	t.Cleanup(func() { buildinfo.Commit, buildinfo.ImageTag = prevCommit, prevTag })

	const sha = "3dc5a35e0b6d4a3f9c2e1d0f8a7b6c5d4e3f2a1b"
	cases := []struct {
		name, commit, tag, want string
	}{
		{"tagged image", sha, "v1.1.0-devnet", "v1.1.0-devnet"},
		{"commit only", sha, buildinfo.Unknown, sha[:12]},
		{"unstamped", buildinfo.Unknown, buildinfo.Unknown, buildinfo.Unknown},
	}
	for _, tc := range cases {
		buildinfo.Commit, buildinfo.ImageTag = tc.commit, tc.tag
		for path, got := range map[string]map[string]any{
			"/status":  decode(s.router, "/status"),
			"/healthz": decode(healthz, "/healthz"),
		} {
			if got["node_version"] != tc.want {
				t.Errorf("%s %s: node_version = %v, want %q", tc.name, path, got["node_version"], tc.want)
			}
			if got["node_version"] == "1.0.0-quantum" {
				t.Errorf("%s %s: node_version reports the retired literal", tc.name, path)
			}
			if got["commit"] != tc.commit || got["image_tag"] != tc.tag {
				t.Errorf("%s %s: commit=%v image_tag=%v, want %q and %q",
					tc.name, path, got["commit"], got["image_tag"], tc.commit, tc.tag)
			}
		}
	}
}
