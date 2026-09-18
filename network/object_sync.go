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
	// maxObjectManifestReplyBytes bounds a sealed manifest reply. An ML-DSA-65
	// public key plus signature plus a small descriptor is well under 8 KiB;
	// 16 KiB leaves room without approaching a symbol payload.
	maxObjectManifestReplyBytes = 16 << 10
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
// sealed channel: verify the manifest against the peer's pinned node identity,
// then authenticate-before-decode every symbol. Default snapshot/block sync
// does not call this; it is the opt-in live path behind --object-codec.
func (f *BlockFetcher) FetchCodedObject(peerAddr string, limits objects.Limits) ([]byte, error) {
	if f.cfg.Identities == nil {
		return nil, fmt.Errorf("coded object from %s: no sealed transport configured", peerAddr)
	}
	doc, err := f.cfg.Identities.Get(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("coded object from %s: peer identity: %w", peerAddr, err)
	}
	raw, err := f.cfg.Identities.QuerySealedTimeout(peerAddr, KindGetObjectManifest, []byte(`{}`),
		sealedReplyBound(maxObjectManifestReplyBytes), objectFetchTimeout)
	if err != nil {
		return nil, fmt.Errorf("fetch object manifest from %s: %w", peerAddr, err)
	}
	manifest, err := objects.DecodeManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("decode object manifest from %s: %w", peerAddr, err)
	}
	recv, err := objects.NewReceiver(manifest, limits, objects.AllowSignerBytes(doc.NodePubKey))
	if err != nil {
		return nil, fmt.Errorf("object manifest from %s: %w", peerAddr, err)
	}
	// One client for every symbol so the loop reuses a connection instead of
	// building a transport per request, under one deadline for the whole loop.
	client := PeerHTTPClient(objectFetchTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), objectFetchDeadline(manifest.CodebookSize))
	defer cancel()
	descID := manifest.DescriptorID()
	for i := uint32(0); i < manifest.CodebookSize && !recv.Ready(); i++ {
		reqBody, err := json.Marshal(GetObjectSymbolRequest{DescriptorID: descID[:], Index: i})
		if err != nil {
			return nil, err
		}
		proofRaw, err := f.cfg.Identities.querySealed(ctx, client, peerAddr, KindGetObjectSymbol, reqBody,
			sealedReplyBound(maxObjectSymbolReplyBytes))
		if err != nil {
			return nil, fmt.Errorf("fetch object symbol %d from %s: %w", i, peerAddr, err)
		}
		proof, err := objects.DecodeSymbolProof(proofRaw)
		if err != nil {
			return nil, fmt.Errorf("decode object symbol %d from %s: %w", i, peerAddr, err)
		}
		if _, err := recv.AbsorbProof(proof); err != nil {
			return nil, fmt.Errorf("object symbol %d from %s: %w", i, peerAddr, err)
		}
	}
	got, err := recv.Decode()
	if err != nil {
		return nil, fmt.Errorf("decode coded object from %s: %w", peerAddr, err)
	}
	return got, nil
}
