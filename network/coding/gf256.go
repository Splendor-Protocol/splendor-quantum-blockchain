// Package coding implements RLNC (Random Linear Network Coding) as an
// optional, isolated large-object codec, per the Sept 2026 improvement plan
// Section 8. It is NOT wired into consensus, block propagation, or any live
// P2P path: the plan's own stance is "test it, measure it, keep it optional,
// only enable if benchmarks prove >15% improvement" — this package exists to
// make that measurement possible, not to assume its outcome.
//
// Plain RLNC is not encryption: an observer with enough independent symbols
// and their coefficients can decode. This package never replaces Splendor's
// ML-KEM-768/AES-256-GCM peer channel or ML-DSA-65 content authentication —
// see network/objects for how a manifest is authenticated before any symbol
// is trusted.
package coding

import "errors"

// GF(256) arithmetic using the AES/Rijndael reduction polynomial
// x^8 + x^4 + x^3 + x + 1 (0x11B), the same field construction used across
// standard erasure-coding and network-coding literature. Implemented with
// table-driven log/antilog multiplication for speed: a generation's encode
// pass touches every (source symbol, output symbol) coefficient pair, so
// naive per-byte polynomial multiplication would dominate the coding cost
// the whole feature is trying to measure.
const gfSize = 256

var (
	gfExp [512]byte // antilog table, doubled to avoid a modulo on lookup
	gfLog [256]byte
)

func init() {
	// Standard GF(2^8) log/antilog table construction with generator 0x03.
	//
	// 0x03 is used deliberately, not 0x02: doubling (x <<= 1, i.e.
	// multiplying by 2) only has multiplicative order 51 under the 0x11B
	// reduction polynomial, so it cycles through 51 of the 255 nonzero
	// elements and repeats — most of gfLog would silently stay at its zero
	// value, i.e. every un-reached element would be misread as equal to 1.
	// 3 is a primitive element (order 255) for this polynomial, so the loop
	// below visits every nonzero field element exactly once.
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		// x = x * 3 (mod 0x11B) = (x*2) XOR x, since GF(2^8) addition is XOR.
		double := x << 1
		if double&0x100 != 0 {
			double ^= 0x11B
		}
		x = double ^ x
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMul multiplies two GF(256) elements. 0 * anything = 0 is handled
// explicitly because log(0) is undefined.
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// gfInv returns the multiplicative inverse of a nonzero GF(256) element.
func gfInv(a byte) (byte, error) {
	if a == 0 {
		return 0, errors.New("coding: no inverse for zero in GF(256)")
	}
	return gfExp[255-int(gfLog[a])], nil
}

// gfInvNonzero is gfInv for a caller that has already proven a != 0: the
// Cauchy parity encode loop computes the inverse of a denominator that is
// nonzero by construction, so it wants the fast, error-free form rather than
// an error branch it can never take.
func gfInvNonzero(a byte) byte {
	return gfExp[255-int(gfLog[a])]
}

// gfDiv computes a/b in GF(256); b must be nonzero.
func gfDiv(a, b byte) (byte, error) {
	inv, err := gfInv(b)
	if err != nil {
		return 0, err
	}
	return gfMul(a, inv), nil
}

// gfAddVec XORs (GF(256) addition) src into dst in place. dst and src must be
// the same length.
func gfAddVec(dst, src []byte) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}

// gfScaleVec multiplies every byte of v by the scalar s, in place.
func gfScaleVec(v []byte, s byte) {
	if s == 0 {
		for i := range v {
			v[i] = 0
		}
		return
	}
	if s == 1 {
		return
	}
	row := gfExp[int(gfLog[s]) : int(gfLog[s])+256]
	for i, b := range v {
		if b != 0 {
			v[i] = row[gfLog[b]]
		}
	}
}

// gfAddScaledVec computes dst += s*src (GF(256) multiply-accumulate), the
// core inner-loop operation of both RLNC encoding (linear combination of
// source symbols) and Gaussian-elimination decoding (row reduction).
func gfAddScaledVec(dst []byte, s byte, src []byte) {
	if s == 0 {
		return
	}
	if s == 1 {
		gfAddVec(dst, src)
		return
	}
	row := gfExp[int(gfLog[s]) : int(gfLog[s])+256]
	for i, b := range src {
		if b != 0 {
			dst[i] ^= row[gfLog[b]]
		}
	}
}
