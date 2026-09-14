package coding

import (
	"math/rand"
	"testing"
)

// The log/antilog table generator must be a primitive element: gfExp[0..254]
// must cover all 255 nonzero field elements exactly once. A generator with a
// shorter multiplicative order (e.g. 2, under this reduction polynomial)
// would leave gfLog permanently zero for every element it never reaches,
// which silently misreads those elements as equal to 1 in every
// multiplication and division — exactly the bug this test is guarding
// against ever recurring.
func TestGFGeneratorIsPrimitive(t *testing.T) {
	seen := make(map[byte]bool, 255)
	for i := 0; i < 255; i++ {
		v := gfExp[i]
		if v == 0 {
			t.Fatalf("gfExp[%d] = 0, the generator must never produce zero", i)
		}
		if seen[v] {
			t.Fatalf("gfExp[%d] = %d was already produced earlier — generator order < 255", i, v)
		}
		seen[v] = true
	}
	if len(seen) != 255 {
		t.Fatalf("generator produced %d distinct nonzero elements, want 255", len(seen))
	}
}

// Every nonzero element must round-trip through inversion: a * inv(a) == 1.
func TestGFInverseRoundTrip(t *testing.T) {
	for a := 1; a < gfSize; a++ {
		inv, err := gfInv(byte(a))
		if err != nil {
			t.Fatalf("gfInv(%d): %v", a, err)
		}
		if got := gfMul(byte(a), inv); got != 1 {
			t.Fatalf("gfMul(%d, inv=%d) = %d, want 1", a, inv, got)
		}
	}
}

func TestGFInverseOfZeroErrors(t *testing.T) {
	if _, err := gfInv(0); err == nil {
		t.Fatal("gfInv(0) should error: zero has no multiplicative inverse")
	}
}

// Multiplication must be commutative and 1 must be the identity, over the
// whole field — a full 256x256 sweep is cheap and catches any table bug.
func TestGFMulCommutativeAndIdentity(t *testing.T) {
	for a := 0; a < gfSize; a++ {
		for b := 0; b < gfSize; b++ {
			ab := gfMul(byte(a), byte(b))
			ba := gfMul(byte(b), byte(a))
			if ab != ba {
				t.Fatalf("gfMul(%d,%d)=%d != gfMul(%d,%d)=%d", a, b, ab, b, a, ba)
			}
		}
		if got := gfMul(byte(a), 1); got != byte(a) {
			t.Fatalf("gfMul(%d, 1) = %d, want %d", a, got, a)
		}
	}
}

// a/b then *b must recover a, for every nonzero b.
func TestGFDivRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		a := byte(rng.Intn(256))
		b := byte(1 + rng.Intn(255)) // nonzero
		q, err := gfDiv(a, b)
		if err != nil {
			t.Fatalf("gfDiv(%d,%d): %v", a, b, err)
		}
		if got := gfMul(q, b); got != a {
			t.Fatalf("gfDiv(%d,%d)=%d, but gfMul(%d,%d)=%d != %d", a, b, q, q, b, got, a)
		}
	}
	if _, err := gfDiv(5, 0); err == nil {
		t.Fatal("gfDiv by zero should error")
	}
}

// gfAddVec must be its own inverse (XOR): a ^= b; a ^= b == original a.
func TestGFAddVecIsSelfInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	a := randBytes(rng, 64)
	orig := append([]byte(nil), a...)
	b := randBytes(rng, 64)
	gfAddVec(a, b)
	gfAddVec(a, b)
	for i := range a {
		if a[i] != orig[i] {
			t.Fatalf("byte %d: %d != original %d after double add", i, a[i], orig[i])
		}
	}
}

// Scaling by 1 must be a no-op; scaling by 0 must zero the vector.
func TestGFScaleVecIdentityAndZero(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	v := randBytes(rng, 32)
	orig := append([]byte(nil), v...)
	gfScaleVec(v, 1)
	for i := range v {
		if v[i] != orig[i] {
			t.Fatalf("scale by 1 changed byte %d: %d -> %d", i, orig[i], v[i])
		}
	}
	gfScaleVec(v, 0)
	for i := range v {
		if v[i] != 0 {
			t.Fatalf("scale by 0 left nonzero byte %d = %d", i, v[i])
		}
	}
}

// dst += s*src must match a scalar-by-scalar reference computation.
func TestGFAddScaledVecMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 50; trial++ {
		dst := randBytes(rng, 40)
		src := randBytes(rng, 40)
		s := byte(rng.Intn(256))

		want := append([]byte(nil), dst...)
		for i := range want {
			want[i] ^= gfMul(s, src[i])
		}

		got := append([]byte(nil), dst...)
		gfAddScaledVec(got, s, src)

		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("trial %d byte %d: got %d, want %d (s=%d)", trial, i, got[i], want[i], s)
			}
		}
	}
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}
