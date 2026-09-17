package coding

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
)

func genSourceSymbols(rng *rand.Rand, count, size int) [][]byte {
	out := make([][]byte, count)
	for i := range out {
		out[i] = randBytes(rng, size)
	}
	return out
}

// The healthy case: absorbing exactly the systematic symbols (no loss, no
// repair needed) must decode back to the exact original source symbols.
func TestRLNCSystematicOnlyDecodesExactly(t *testing.T) {
	rng := rand.New(rand.NewSource(10))
	source := genSourceSymbols(rng, 16, 64)
	codec := NewRLNCCodec()

	enc, err := codec.NewEncoder(source, 64)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := codec.NewDecoder(16, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range enc.Systematic() {
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb systematic %d: %v", s.Index, err)
		}
	}
	if dec.Rank() != 16 {
		t.Fatalf("rank = %d, want 16", dec.Rank())
	}
	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range source {
		if !bytes.Equal(got[i], source[i]) {
			t.Fatalf("symbol %d mismatch after systematic-only decode", i)
		}
	}
}

// Losing some systematic symbols but making up the rank with repair symbols
// must still decode exactly — this is the entire point of RLNC over
// uncoded transfer: ANY sufficient independent set works, not a specific one.
func TestRLNCRecoversFromLossUsingRepairSymbols(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const k = 20
	source := genSourceSymbols(rng, k, 128)
	codec := NewRLNCCodec()

	enc, err := codec.NewEncoder(source, 128)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := codec.NewDecoder(k, 128)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate losing every 3rd systematic symbol.
	systematic := enc.Systematic()
	lost := 0
	for i, s := range systematic {
		if i%3 == 0 {
			lost++
			continue
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb systematic %d: %v", i, err)
		}
	}
	if dec.Rank() >= k {
		t.Fatal("test setup: no symbols were actually lost")
	}

	// Repair symbols make up the rank. Ask for more than strictly needed
	// (the plan's "finite repair set" — some repair symbols may be
	// dependent on what's already held, so request a small overhead).
	for _, s := range mustRepair(t, enc, lost+4) {
		if dec.Rank() == k {
			break
		}
		if _, err := dec.Absorb(s); err != nil {
			t.Fatalf("absorb repair %d: %v", s.Index, err)
		}
	}
	if dec.Rank() != k {
		t.Fatalf("rank = %d after repair, want %d (repair set exhausted without reaching full rank)", dec.Rank(), k)
	}

	got, err := dec.Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := range source {
		if !bytes.Equal(got[i], source[i]) {
			t.Fatalf("symbol %d mismatch after loss+repair decode", i)
		}
	}
}

// Decode before full rank must fail loudly (ErrNotEnoughSymbols), never
// silently return partial or wrong data. Collecting a certain NUMBER of
// symbols is not sufficient — this test also plants literal duplicates,
// which must not be mistaken for distinct useful symbols.
func TestRLNCDecodeBeforeFullRankFails(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	const k = 8
	source := genSourceSymbols(rng, k, 32)
	codec := NewRLNCCodec()
	enc, _ := codec.NewEncoder(source, 32)
	dec, _ := codec.NewDecoder(k, 32)

	systematic := enc.Systematic()
	for i := 0; i < 3; i++ {
		increased, err := dec.Absorb(systematic[0])
		if err != nil {
			t.Fatalf("absorb duplicate: %v", err)
		}
		if i > 0 && increased {
			t.Fatal("a duplicate symbol increased decoder rank")
		}
	}
	if dec.Rank() != 1 {
		t.Fatalf("rank = %d after 3x the same symbol, want 1", dec.Rank())
	}
	if _, err := dec.Decode(); !errors.Is(err, ErrNotEnoughSymbols) {
		t.Fatalf("decode with rank 1/%d = %v, want ErrNotEnoughSymbols", k, err)
	}
}

// A symbol with the wrong payload length must be rejected before any matrix
// work — the plan's "corrupt payload: rejection before expensive decoding"
// benchmark scenario.
func TestRLNCAbsorbRejectsMalformedSymbols(t *testing.T) {
	codec := NewRLNCCodec()
	dec, _ := codec.NewDecoder(4, 16)

	if _, err := dec.Absorb(Symbol{Coefficients: make([]byte, 4), Payload: make([]byte, 8)}); !errors.Is(err, ErrSymbolSizeMismatch) {
		t.Fatalf("wrong payload size = %v, want ErrSymbolSizeMismatch", err)
	}
	if _, err := dec.Absorb(Symbol{Coefficients: make([]byte, 2), Payload: make([]byte, 16)}); err == nil {
		t.Fatal("wrong coefficient vector length accepted")
	}
	if _, err := dec.Absorb(Symbol{Index: 99, Payload: make([]byte, 16)}); err == nil {
		t.Fatal("out-of-range uncoded index accepted")
	}
}

func TestRLNCEmptyGenerationRejected(t *testing.T) {
	codec := NewRLNCCodec()
	if _, err := codec.NewEncoder(nil, 16); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("empty encoder = %v, want ErrEmptyGeneration", err)
	}
	if _, err := codec.NewDecoder(0, 16); !errors.Is(err, ErrEmptyGeneration) {
		t.Fatalf("zero-source decoder = %v, want ErrEmptyGeneration", err)
	}
}

func TestRLNCEncoderRejectsWrongSizeSourceSymbol(t *testing.T) {
	codec := NewRLNCCodec()
	bad := [][]byte{make([]byte, 16), make([]byte, 8)}
	if _, err := codec.NewEncoder(bad, 16); !errors.Is(err, ErrSymbolSizeMismatch) {
		t.Fatalf("mismatched source symbol size = %v, want ErrSymbolSizeMismatch", err)
	}
}

// Decoding must work at generation sizes spanning the plan's initial
// candidates (16, 32, 64 source symbols).
func TestRLNCDecodesAcrossCandidateGenerationSizes(t *testing.T) {
	for _, k := range []int{1, 2, 16, 32, 64} {
		rng := rand.New(rand.NewSource(int64(100 + k)))
		source := genSourceSymbols(rng, k, 64)
		codec := NewRLNCCodec()
		enc, err := codec.NewEncoder(source, 64)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		dec, err := codec.NewDecoder(k, 64)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		half := k / 2
		systematic := enc.Systematic()
		for _, s := range systematic[:half] {
			if _, err := dec.Absorb(s); err != nil {
				t.Fatalf("k=%d absorb systematic: %v", k, err)
			}
		}
		for _, s := range mustRepair(t, enc, k) {
			if dec.Rank() == k {
				break
			}
			if _, err := dec.Absorb(s); err != nil {
				t.Fatalf("k=%d absorb repair: %v", k, err)
			}
		}
		if dec.Rank() != k {
			t.Fatalf("k=%d: rank %d, want %d", k, dec.Rank(), k)
		}
		got, err := dec.Decode()
		if err != nil {
			t.Fatalf("k=%d decode: %v", k, err)
		}
		for i := range source {
			if !bytes.Equal(got[i], source[i]) {
				t.Fatalf("k=%d: symbol %d mismatch", k, i)
			}
		}
	}
}

func mustRepair(t *testing.T, enc Encoder, n int) []Symbol {
	t.Helper()
	out, err := enc.Repair(n)
	if err != nil {
		t.Fatalf("Repair(%d): %v", n, err)
	}
	return out
}

func TestRLNCRejectsNonPositiveAndOversizedDimensions(t *testing.T) {
	codec := NewRLNCCodec()
	if _, err := codec.NewEncoder([][]byte{[]byte("x")}, 0); !errors.Is(err, ErrInvalidDimension) {
		t.Fatalf("zero symbol size encoder = %v, want ErrInvalidDimension", err)
	}
	if _, err := codec.NewDecoder(4, -1); !errors.Is(err, ErrInvalidDimension) {
		t.Fatalf("negative symbol size decoder = %v, want ErrInvalidDimension", err)
	}

	tooMany := make([][]byte, MaxSourceSymbols+1)
	for i := range tooMany {
		tooMany[i] = []byte{1}
	}
	if _, err := codec.NewEncoder(tooMany, 1); !errors.Is(err, ErrGenerationTooLarge) {
		t.Fatalf("oversized encoder = %v, want ErrGenerationTooLarge", err)
	}
	if _, err := codec.NewDecoder(MaxSourceSymbols+1, 16); !errors.Is(err, ErrGenerationTooLarge) {
		t.Fatalf("oversized decoder = %v, want ErrGenerationTooLarge", err)
	}

	huge := [][]byte{make([]byte, MaxSymbolSize+1)}
	if _, err := codec.NewEncoder(huge, MaxSymbolSize+1); !errors.Is(err, ErrGenerationTooLarge) {
		t.Fatalf("oversized symbol encoder = %v, want ErrGenerationTooLarge", err)
	}
	if _, err := codec.NewDecoder(1, MaxSymbolSize+1); !errors.Is(err, ErrGenerationTooLarge) {
		t.Fatalf("oversized symbol decoder = %v, want ErrGenerationTooLarge", err)
	}
}

func TestRLNCRepairRejectsNonPositiveAndHugeN(t *testing.T) {
	codec := NewRLNCCodec()
	enc, err := codec.NewEncoder([][]byte{make([]byte, 8), make([]byte, 8)}, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, -1, -1 << 20} {
		got, err := enc.Repair(n)
		if err != nil {
			t.Fatalf("Repair(%d) error = %v, want nil", n, err)
		}
		if len(got) != 0 {
			t.Fatalf("Repair(%d) returned %d symbols, want 0", n, len(got))
		}
	}
	if _, err := enc.Repair(1 << 25); !errors.Is(err, ErrRepairCountTooLarge) {
		t.Fatalf("Repair(1<<25) = %v, want ErrRepairCountTooLarge", err)
	}
}

func TestRLNCRepairStableSymbolIDsAcrossCalls(t *testing.T) {
	codec := NewRLNCCodec()
	const k = 4
	source := [][]byte{make([]byte, 8), make([]byte, 8), make([]byte, 8), make([]byte, 8)}
	enc, err := codec.NewEncoder(source, 8)
	if err != nil {
		t.Fatal(err)
	}
	first := mustRepair(t, enc, 2)
	second := mustRepair(t, enc, 2)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got %d + %d repair symbols, want 2 + 2", len(first), len(second))
	}
	if first[0].Index != k || first[1].Index != k+1 {
		t.Fatalf("first batch indices = %d,%d want %d,%d", first[0].Index, first[1].Index, k, k+1)
	}
	if second[0].Index != k+2 || second[1].Index != k+3 {
		t.Fatalf("second batch indices = %d,%d want %d,%d (IDs must not restart at k)", second[0].Index, second[1].Index, k+2, k+3)
	}
}

func TestRLNCRepairErrorsOnRandomnessFailureAndUsesCompleteReads(t *testing.T) {
	source := [][]byte{make([]byte, 8), make([]byte, 8), make([]byte, 8)}
	failing := NewRLNCCodecWithReader(failingReader{})
	enc, err := failing.NewEncoder(source, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Repair(1); !errors.Is(err, ErrRepairRandomness) {
		t.Fatalf("failed rand = %v, want ErrRepairRandomness", err)
	}

	// A reader that only yields one byte per Read must still fill the
	// coefficient vector (io.ReadFull), not leave a short/zero-padded row.
	slow := NewRLNCCodecWithReader(byteAtATime{src: bytes.NewReader(bytes.Repeat([]byte{3}, 64))})
	enc, err = slow.NewEncoder(source, 8)
	if err != nil {
		t.Fatal(err)
	}
	got := mustRepair(t, enc, 1)
	if len(got) != 1 {
		t.Fatalf("slow reader returned %d symbols, want 1", len(got))
	}
	for i, c := range got[0].Coefficients {
		if c != 3 {
			t.Fatalf("coeff[%d] = %d, want 3 (incomplete read left zeros)", i, c)
		}
	}
}

func TestRLNCRepairDoesNotAdvanceIndexOnFailure(t *testing.T) {
	const k = 3
	src := &scriptedReader{remain: k, fail: io.ErrUnexpectedEOF}
	enc, err := NewRLNCCodecWithReader(src).NewEncoder([][]byte{
		make([]byte, 8), make([]byte, 8), make([]byte, 8),
	}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Repair(2); !errors.Is(err, ErrRepairRandomness) {
		t.Fatalf("partial-batch Repair = %v, want ErrRepairRandomness", err)
	}
	src.remain = 64
	src.fail = nil
	got := mustRepair(t, enc, 1)
	if got[0].Index != k {
		t.Fatalf("index after failed Repair = %d, want %d (failed batch must not skip IDs)", got[0].Index, k)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

type scriptedReader struct {
	remain int
	fail   error
}

func (s *scriptedReader) Read(p []byte) (int, error) {
	if s.remain <= 0 {
		if s.fail != nil {
			return 0, s.fail
		}
		return 0, io.EOF
	}
	n := len(p)
	if n > s.remain {
		n = s.remain
	}
	for i := 0; i < n; i++ {
		p[i] = 1
	}
	s.remain -= n
	return n, nil
}

type byteAtATime struct{ src *bytes.Reader }

func (b byteAtATime) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return b.src.Read(p[:1])
}
