package types

// StateLeaf is one committed state entry (an acc:/code:/store:/x402nonce:
// key and its raw value) as it travels in a state snapshot. Both fields are
// raw bytes and marshal as base64.
type StateLeaf struct {
	Key   []byte `json:"key"`
	Value []byte `json:"value"`
}

// StateSnapshot is the point-in-time state a node serves so a newcomer can
// join a chain whose history it cannot replay.
//
// Height is the block the leaves reflect and Block is that block, which is
// what authenticates the leaves: its header's state_root must equal the
// Merkle root over Leaves, and the block itself is verified by the receiver
// against the pinned genesis validator key before anything is imported.
type StateSnapshot struct {
	Height uint64      `json:"height"`
	Block  *Block      `json:"block"`
	Leaves []StateLeaf `json:"leaves"`
}

// StateSnapshotChunkResult is one page of a chunked state export, returned by
// the executor to whatever transport serves it (currently the sealed P2P
// read plane). Block is populated only for the first page of a transfer —
// see Executor.ExportStateSnapshotChunk.
type StateSnapshotChunkResult struct {
	// SessionID identifies the immutable serving session; empty only when the
	// node serves stateless pages. The client echoes it back on later pages.
	SessionID string      `json:"session_id,omitempty"`
	Height    uint64      `json:"height"`
	Block     *Block      `json:"block,omitempty"`
	Leaves    []StateLeaf `json:"leaves"`
	NextKey   []byte      `json:"next_key,omitempty"`
	Done      bool        `json:"done"`
}
