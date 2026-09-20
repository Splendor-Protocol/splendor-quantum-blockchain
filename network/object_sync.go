package network

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

const (
	// maxObjectManifestReplyBytes bounds a sealed manifest reply. A
	// multi-generation object manifest is at most MaxGenerations × 40 bytes of
	// generation entries plus the descriptor, key and signature: under 170 KiB
	// at the cap. 256 KiB leaves headroom for the envelope without approaching
	// a symbol payload.
	maxObjectManifestReplyBytes = 256 << 10
	// maxObjectSymbolReplyBytes bounds one proof: MaxSymbolSize payload plus
	// coefficients, Merkle siblings, and SQCO headers.
	maxObjectSymbolReplyBytes = coding.MaxSymbolSize + coding.MaxSourceSymbols + (objects.MaxMerkleSiblings * 32) + 1024
	// objectFetchTimeout bounds one sealed request (manifest or symbol).
	objectFetchTimeout = 1 * time.Minute
	// The symbol phase also gets one aggregate budget: a floor so a small
	// object is not starved by one slow symbol, per-symbol time above that,
	// and a cap so a peer answering each symbol just inside
	// objectFetchTimeout cannot hold a fetch for CodebookSize minutes.
	objectFetchFloor     = 2 * time.Minute
	objectFetchPerSymbol = 2 * time.Second
	objectFetchCap       = 10 * time.Minute
)

// objectFetchDeadline is the aggregate budget for fetching n committed symbols.
// sealedReplyBound is the wire size a sealed reply may reach when its
// plaintext is at most plain bytes. The reply travels as a JSON envelope:
// the AES-GCM ciphertext (plaintext plus a 16-byte tag) and the 1,088-byte
// ML-KEM-768 ciphertext are both base64, beside a nonce, a timestamp and the
// field names. Reading the wire with the plaintext cap truncated every proof
// above roughly 48 KiB before it could be opened.
func sealedReplyBound(plain int) int64 {
	return int64((plain+16+2)/3*4 + 2048)
}

func objectFetchDeadline(n uint32) time.Duration {
	if n >= uint32(objectFetchCap/objectFetchPerSymbol) {
		return objectFetchCap
	}
	if d := time.Duration(n) * objectFetchPerSymbol; d > objectFetchFloor {
		return d
	}
	return objectFetchFloor
}

// FetchCodedObject pulls a peer's currently published coded object over the
// sealed channel: verify the object manifest against the peer's pinned node
// identity, then authenticate-before-decode every symbol of every generation.
// The reconstructed bytes are then validated by the caller against the
// chain-level commitments of whatever the object claims to be (a block or a
// state snapshot) — this method only authenticates the transfer.
func (f *BlockFetcher) FetchCodedObject(peerAddr string, limits objects.Limits) ([]byte, error) {
	if f.cfg.Identities == nil {
		return nil, fmt.Errorf("coded object from %s: no sealed transport configured", peerAddr)
	}
	doc, err := f.cfg.Identities.Get(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("coded object from %s: peer identity: %w", peerAddr, err)
	}
	if !doc.ServesCodedObjects() {
		return nil, fmt.Errorf("coded object from %s: peer declares no coded-object service", peerAddr)
	}
	// A peer whose document predates the capability block gets probed; if it
	// refused recently, skip the wasted sealed request this time.
	if doc.CodedObjects == nil && f.cfg.Identities.CodedRecentlyRefused(peerAddr, CodedSnapshots) {
		return nil, fmt.Errorf("coded object from %s: peer recently refused coded-object service", peerAddr)
	}
	raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetObjectManifest, []byte(`{}`),
		sealedReplyBound(maxObjectManifestReplyBytes), objectFetchTimeout)
	if err != nil {
		if doc.CodedObjects == nil {
			f.cfg.Identities.NoteCodedRefusal(peerAddr, CodedSnapshots)
		}
		return nil, fmt.Errorf("fetch object manifest from %s: %w", peerAddr, err)
	}
	f.cfg.Identities.ForgetCodedRefusal(peerAddr, CodedSnapshots)
	manifest, err := objects.DecodeObjectManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("decode object manifest from %s: %w", peerAddr, err)
	}
	return f.fetchDecodedObject(peerAddr, manifest, limits)
}

// fetchDecodedObject reconstructs the object committed by manifest: symbols
// are fetched over the sealed channel, every proof authenticated against its
// generation's codebook root before it reaches a decoder, under one deadline
// sized by the total codebook.
func (f *BlockFetcher) fetchDecodedObject(peerAddr string, manifest *objects.ObjectManifest, limits objects.Limits) ([]byte, error) {
	doc, err := f.cfg.Identities.Get(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("coded object from %s: peer identity: %w", peerAddr, err)
	}
	recv, err := objects.NewObjectReceiver(manifest, limits, objects.AllowObjectSignerBytes(doc.NodePubKey))
	if err != nil {
		return nil, fmt.Errorf("object manifest from %s: %w", peerAddr, err)
	}
	// One client for every symbol so the loop reuses a connection instead of
	// building a transport per request, under one deadline for the whole loop,
	// sized by the total codebook across all generations.
	var total uint32
	for _, g := range manifest.Generations {
		total += g.CodebookSize
	}
	client := PeerHTTPClient(objectFetchTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), objectFetchDeadline(total))
	defer cancel()
	descID := manifest.DescriptorID()
	for gen := 0; gen < recv.GenerationCount(); gen++ {
		for i := uint32(0); i < manifest.Generations[gen].CodebookSize && !recv.GenReady(gen); i++ {
			reqBody, err := json.Marshal(GetObjectSymbolRequest{DescriptorID: descID[:], Generation: uint32(gen), Index: i})
			if err != nil {
				return nil, err
			}
			proofRaw, err := f.cfg.Identities.querySealed(ctx, client, peerAddr, KindGetObjectSymbol, reqBody,
				sealedReplyBound(maxObjectSymbolReplyBytes))
			if err != nil {
				return nil, fmt.Errorf("fetch object symbol %d/%d from %s: %w", gen, i, peerAddr, err)
			}
			proof, err := objects.DecodeSymbolProof(proofRaw)
			if err != nil {
				return nil, fmt.Errorf("decode object symbol %d/%d from %s: %w", gen, i, peerAddr, err)
			}
			if _, err := recv.AbsorbProof(gen, proof); err != nil {
				return nil, fmt.Errorf("object symbol %d/%d from %s: %w", gen, i, peerAddr, err)
			}
		}
	}
	got, err := recv.Decode()
	if err != nil {
		return nil, fmt.Errorf("decode coded object from %s: %w", peerAddr, err)
	}
	return got, nil
}
