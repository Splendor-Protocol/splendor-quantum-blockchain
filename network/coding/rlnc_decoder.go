package coding

import "fmt"

// rlncDecoder performs incremental Gaussian elimination: each Absorb call
// reduces the incoming row against rows already held, and either inserts it
// (rank increased) or discards it (linearly dependent — not useful, not an
// error). This is the standard RLNC "on-the-fly" decoding approach: no
// symbol is held un-reduced, so decode cost is spread across reception
// rather than paid all at once at the end.
type rlncDecoder struct {
	sourceCount int
	symbolSize  int
	rows        [][]byte // reduced coefficient rows, row-echelon form
	payloads    [][]byte // payload accumulator matching each row
	pivotCol    []int    // pivotCol[i] = the column rows[i] is reduced to a 1 in
}

func (d *rlncDecoder) SourceCount() int { return d.sourceCount }
func (d *rlncDecoder) Rank() int        { return len(d.rows) }

func (d *rlncDecoder) Absorb(s Symbol) (bool, error) {
	if len(s.Payload) != d.symbolSize {
		return false, fmt.Errorf("%w: got %d bytes, want %d", ErrSymbolSizeMismatch, len(s.Payload), d.symbolSize)
	}
	var coeffs []byte
	if len(s.Coefficients) == 0 {
		// Uncoded systematic symbol carried with no coefficient vector:
		// treat it as the identity row at s.Index. Bounds-check Index
		// before use — an out-of-range Index here is exactly the kind of
		// malformed input Absorb must reject before any matrix work.
		if s.Index < 0 || s.Index >= d.sourceCount {
			return false, fmt.Errorf("coding: symbol index %d out of range [0,%d)", s.Index, d.sourceCount)
		}
		coeffs = make([]byte, d.sourceCount)
		coeffs[s.Index] = 1
	} else {
		if len(s.Coefficients) != d.sourceCount {
			return false, fmt.Errorf("coding: symbol carries %d coefficients, want %d", len(s.Coefficients), d.sourceCount)
		}
		coeffs = append([]byte(nil), s.Coefficients...)
	}
	payload := append([]byte(nil), s.Payload...)

	// Reduce the incoming row against every pivot already held.
	for i, row := range d.rows {
		col := d.pivotCol[i]
		if coeffs[col] == 0 {
			continue
		}
		scale := coeffs[col]
		gfAddScaledVec(coeffs, scale, row)
		gfAddScaledVec(payload, scale, d.payloads[i])
	}

	// Find the new row's pivot: its first nonzero column.
	pivot := -1
	for col, c := range coeffs {
		if c != 0 {
			pivot = col
			break
		}
	}
	if pivot == -1 {
		// Reduced to all-zero: this symbol was a linear combination of rows
		// already held. Not useful, but not malformed — normal RLNC
		// behaviour once a generation is close to full rank.
		return false, nil
	}

	// Normalize the pivot to 1 so later reductions are pure XOR-add, not
	// scale-then-add.
	inv, err := gfInv(coeffs[pivot])
	if err != nil {
		// coeffs[pivot] is nonzero by construction above; this can only
		// fail if gfInv itself is broken, not on any valid input.
		return false, fmt.Errorf("coding: internal: %w", err)
	}
	gfScaleVec(coeffs, inv)
	gfScaleVec(payload, inv)

	d.rows = append(d.rows, coeffs)
	d.payloads = append(d.payloads, payload)
	d.pivotCol = append(d.pivotCol, pivot)
	return true, nil
}

func (d *rlncDecoder) Decode() ([][]byte, error) {
	if len(d.rows) < d.sourceCount {
		return nil, fmt.Errorf("%w: have rank %d, need %d", ErrNotEnoughSymbols, len(d.rows), d.sourceCount)
	}
	// Back-substitute to full reduced row-echelon form: eliminate every
	// pivot column from every OTHER row, so each row directly reads out one
	// source symbol with no further computation.
	for i := range d.rows {
		pi := d.pivotCol[i]
		for j := range d.rows {
			if j == i {
				continue
			}
			c := d.rows[j][pi]
			if c == 0 {
				continue
			}
			gfAddScaledVec(d.rows[j], c, d.rows[i])
			gfAddScaledVec(d.payloads[j], c, d.payloads[i])
		}
	}
	out := make([][]byte, d.sourceCount)
	for i := range d.rows {
		out[d.pivotCol[i]] = d.payloads[i]
	}
	for i, p := range out {
		if p == nil {
			// Every pivotCol must, by full rank, cover every column exactly
			// once; a nil entry here means the rank/pivot bookkeeping is
			// broken, not that the input was bad (already checked above).
			return nil, fmt.Errorf("coding: internal: source symbol %d never reconstructed despite full rank", i)
		}
	}
	return out, nil
}
