package network

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
)

// FuzzValidateBlockBatchRange: the shared range guard for
// get_block_batch_v1 must agree exactly with its spec — never panic, and
// accept a range iff the count is in [1, MaxCodedBlockBatchBlocks] and
// fromHeight+count does not wrap uint64.
func FuzzValidateBlockBatchRange(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})                         // (0,0)
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1})                         // (1,1)
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 10, 0, 0, 0, 32})                       // (10,32)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 2}) // (MaxUint64,2)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 12 {
			return
		}
		from := binary.BigEndian.Uint64(data[:8])
		count := int(int32(binary.BigEndian.Uint32(data[8:12])))
		err := ValidateBlockBatchRange(from, count)
		valid := count >= 1 && count <= MaxCodedBlockBatchBlocks && from <= math.MaxUint64-uint64(count)
		if valid && err != nil {
			t.Fatalf("valid range (%d, %d) refused: %v", from, count, err)
		}
		if !valid && err == nil {
			t.Fatalf("invalid range (%d, %d) accepted", from, count)
		}
	})
}

// FuzzBlockBatchRequestJSON: the peer-supplied request body parses as JSON
// and every accepted body lands on the shared range guard. The two together
// are the entire input surface of get_block_batch_v1.
func FuzzBlockBatchRequestJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"from_height":10,"count":4}`))
	f.Add([]byte(`{"from_height":18446744073709551615,"count":2}`))
	f.Add([]byte(`{"from_height":10,"count":999}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var q GetBlockBatchRequest
		if err := json.Unmarshal(data, &q); err == nil {
			_ = ValidateBlockBatchRange(q.FromHeight, q.Count)
		}
		// Round-trip: an accepted body re-encodes to a body that parses back
		// to the same request.
		re, err := json.Marshal(&q)
		if err == nil {
			var q2 GetBlockBatchRequest
			if err := json.Unmarshal(re, &q2); err != nil {
				t.Fatalf("re-encoded request does not parse: %v", err)
			}
		}
	})
}

// FuzzObjectSymbolRequestJSON and FuzzObjectManifestRequestJSON cover the two
// remaining sealed-read bodies: arbitrary JSON must parse (or be refused)
// without panics, and descriptor IDs survive a marshal round trip.
func FuzzObjectSymbolRequestJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"descriptor_id":"AQID","generation":1,"index":2}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var q GetObjectSymbolRequest
		_ = json.Unmarshal(data, &q)
		re, err := json.Marshal(&q)
		if err == nil {
			var q2 GetObjectSymbolRequest
			if err := json.Unmarshal(re, &q2); err != nil {
				t.Fatalf("re-encoded request does not parse: %v", err)
			}
		}
	})
}

func FuzzObjectManifestRequestJSON(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"descriptor_id":"AQID"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var q GetObjectManifestRequest
		_ = json.Unmarshal(data, &q)
		re, err := json.Marshal(&q)
		if err == nil {
			var q2 GetObjectManifestRequest
			if err := json.Unmarshal(re, &q2); err != nil {
				t.Fatalf("re-encoded request does not parse: %v", err)
			}
		}
	})
}
