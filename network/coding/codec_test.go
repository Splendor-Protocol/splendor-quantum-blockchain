package coding

import "testing"

// Resolve is the negotiation primitive: it must map every registered codec's
// Name() back to a fresh instance, and fail closed on anything else — a
// receiver never infers a codec from an unknown name.
func TestResolve(t *testing.T) {
	for _, want := range []string{"uncoded-v1", "erasure-rs-v1", "rlnc-gf256-v1"} {
		c, err := Resolve(want)
		if err != nil {
			t.Fatalf("Resolve(%q) = %v, want a codec", want, err)
		}
		if c.Name() != want {
			t.Fatalf("Resolve(%q) returned codec named %q", want, c.Name())
		}
	}
	for _, unknown := range []string{"", "rlnc-future-v99", "RLNC-GF256-V1", " arbitrary "} {
		if _, err := Resolve(unknown); err == nil {
			t.Errorf("Resolve(%q) accepted an unknown codec name", unknown)
		}
	}
}
