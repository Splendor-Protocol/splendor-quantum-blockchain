package vm

// u64be is v as 8 big-endian bytes. Storage-slot derivations append it to a
// label so slots keyed by an index or an id are fixed-width and distinct.
func u64be(v uint64) []byte {
	return []byte{byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}
