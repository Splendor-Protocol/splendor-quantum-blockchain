package network

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
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
	// objectSymbolRetries bounds per-symbol transport/decode retries before the
	// fetcher moves on to the next codebook index: the repair symbols that
	// follow the systematic ones in a generation's codebook cover a lost
	// systematic symbol, so a bounded retry + advance is enough to tolerate
	// loss without ever relaxing the fail-closed authentication check.
	objectSymbolRetries = 2
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

// objectProgress retains partially-decoded coded-object receivers so a retry
// against the SAME peer resumes from already-authenticated symbols instead of
// restarting from rank zero. It is bounded in both entry count and committed
// payload bytes: a receiver holds one decoder per generation, so the worst case
// is the object's committed payload (capped by objects.DefaultMaxObjectSize).
// The key is (peer, descriptor ID): an object's manifest is signed by the
// serving peer's node key, so a receiver is only ever resumed against the peer
// it was built for — never across signers. Failure modes stay fail-closed: only
// lossy/liveness failures (a generation that does not reach full rank) are
// retained; a proof that fails authentication aborts and is never cached.
type objectProgress struct {
	mu      sync.Mutex
	entries map[string]*objectProgressEntry
	order   []string // FIFO, oldest first
	bytes   uint64
	resumes uint64 // metric: transfers that resumed from retained progress
}

type objectProgressEntry struct {
	recv *objects.ObjectReceiver
	size uint64
}

const (
	maxObjectProgressReceivers = 4
	maxObjectProgressBytes     = 256 << 20 // 256 MiB aggregate decoder state
)

func newObjectProgress() *objectProgress {
	return &objectProgress{entries: make(map[string]*objectProgressEntry)}
}

func objectProgressKey(peerAddr string, descID [32]byte) string {
	return peerAddr + "|" + hex.EncodeToString(descID[:])
}

// take removes and returns the retained receiver for (peer, descID), or nil.
func (p *objectProgress) take(peerAddr string, descID [32]byte) *objects.ObjectReceiver {
	if p == nil {
		return nil
	}
	key := objectProgressKey(peerAddr, descID)
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[key]
	if !ok {
		return nil
	}
	delete(p.entries, key)
	for i, k := range p.order {
		if k == key {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.bytes -= e.size
	p.resumes++
	return e.recv
}

// keep retains a partially-decoded receiver for a later retry, evicting the
// oldest entry when a bound would be exceeded. An object larger than the whole
// byte budget is not retained.
func (p *objectProgress) keep(peerAddr string, descID [32]byte, recv *objects.ObjectReceiver, size uint64) {
	if p == nil || recv == nil || size == 0 || size > maxObjectProgressBytes {
		return
	}
	key := objectProgressKey(peerAddr, descID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[key]; exists {
		return
	}
	for (len(p.entries) >= maxObjectProgressReceivers || p.bytes+size > maxObjectProgressBytes) && len(p.order) > 0 {
		oldest := p.order[0]
		p.order = p.order[1:]
		if e, ok := p.entries[oldest]; ok {
			p.bytes -= e.size
			delete(p.entries, oldest)
		}
	}
	if len(p.entries) >= maxObjectProgressReceivers || p.bytes+size > maxObjectProgressBytes {
		return
	}
	p.order = append(p.order, key)
	p.entries[key] = &objectProgressEntry{recv: recv, size: size}
	p.bytes += size
}

// Resumes reports how many transfers resumed from retained progress.
func (p *objectProgress) Resumes() uint64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resumes
}

// CodedProgressResumes reports how many coded-object transfers resumed from a
// partially-decoded receiver retained by an earlier attempt, for observability.
func (f *BlockFetcher) CodedProgressResumes() uint64 {
	return f.progress.Resumes()
}

// FetchCodedObject pulls a peer's currently published coded object over the
// sealed channel: verify the object manifest against the peer's pinned node
// identity, then authenticate-before-decode every symbol of every generation.
// The reconstructed bytes are then validated by the caller against the
// chain-level commitments of whatever the object claims to be (a block or a
// state snapshot) — this method only authenticates the transfer.
func (f *BlockFetcher) FetchCodedObject(ctx context.Context, peerAddr string, limits objects.Limits) ([]byte, error) {
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
	if doc.CodedObjects == nil && f.cfg.Identities.CodedRecentlyRefused(peerAddr) {
		return nil, fmt.Errorf("coded object from %s: peer recently refused coded-object service", peerAddr)
	}
	raw, err := f.cfg.Identities.QuerySealedContext(ctx, peerAddr, KindGetObjectManifest, []byte(`{}`),
		sealedReplyBound(maxObjectManifestReplyBytes))
	if err != nil {
		if doc.CodedObjects == nil {
			f.cfg.Identities.NoteCodedRefusal(peerAddr)
		}
		return nil, fmt.Errorf("fetch object manifest from %s: %w", peerAddr, err)
	}
	f.cfg.Identities.ForgetCodedRefusal(peerAddr)
	manifest, err := objects.DecodeObjectManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("decode object manifest from %s: %w", peerAddr, err)
	}
	return f.fetchDecodedObject(ctx, peerAddr, manifest, limits)
}

// fetchDecodedObject reconstructs the object committed by manifest: symbols
// are fetched over the sealed channel, every proof authenticated against its
// generation's codebook root before it reaches a decoder, under one deadline
// sized by the total codebook and bounded by the caller's context.
//
// The symbol phase is repair-aware: a symbol whose transport or wire decode
// fails is retried a bounded number of times and then skipped, because the
// repair symbols that follow the systematic ones in a generation's codebook
// can cover the loss. A proof that decodes but fails authentication against
// the committed codebook root is fail-closed immediately — that is a peer
// lying about a symbol, not a lost packet, and it is never fed to the decoder.
func (f *BlockFetcher) fetchDecodedObject(ctx context.Context, peerAddr string, manifest *objects.ObjectManifest, limits objects.Limits) ([]byte, error) {
	doc, err := f.cfg.Identities.Get(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("coded object from %s: peer identity: %w", peerAddr, err)
	}
	descID := manifest.DescriptorID()
	// Resume a partially-decoded receiver from a previous attempt against the
	// same peer (same descriptor, same signer) rather than restarting from
	// rank zero.
	recv := f.progress.take(peerAddr, descID)
	if recv == nil {
		recv, err = objects.NewObjectReceiver(manifest, limits, objects.AllowObjectSignerBytes(doc.NodePubKey))
		if err != nil {
			return nil, fmt.Errorf("object manifest from %s: %w", peerAddr, err)
		}
	}
	// One client for every symbol so the loop reuses a connection instead of
	// building a transport per request, under one deadline for the whole loop,
	// sized by the total codebook across all generations and derived from the
	// caller's context so cancelling the enclosing sync stops this transfer.
	var total uint32
	for _, g := range manifest.Generations {
		total += g.CodebookSize
	}
	client := PeerHTTPClient(objectFetchTimeout)
	ctx, cancel := context.WithTimeout(ctx, objectFetchDeadline(total))
	defer cancel()
	symbols := 0
	for gen := 0; gen < recv.GenerationCount(); gen++ {
		cb := manifest.Generations[gen].CodebookSize
		for i := uint32(0); i < cb && !recv.GenReady(gen); i++ {
			proof, ok := f.fetchObjectSymbol(ctx, client, peerAddr, descID, gen, i)
			if !ok {
				// Lost after retries; the loop advances to the next codebook
				// index, where the repair symbols cover the gap.
				continue
			}
			if _, err := recv.AbsorbProof(gen, proof); err != nil {
				if errors.Is(err, objects.ErrUnauthenticatedSymbol) {
					return nil, fmt.Errorf("object symbol %d/%d from %s failed authentication: %w", gen, i, peerAddr, err)
				}
				// Duplicate or out-of-range index is a no-op, not a failure.
				continue
			}
			symbols++
		}
		if !recv.GenReady(gen) {
			f.progress.keep(peerAddr, descID, recv, manifest.ObjectSize)
			return nil, fmt.Errorf("object generation %d from %s did not reach full rank (%d symbols absorbed)", gen, peerAddr, symbols)
		}
	}
	got, err := recv.Decode()
	if err != nil {
		f.progress.keep(peerAddr, descID, recv, manifest.ObjectSize)
		return nil, fmt.Errorf("decode coded object from %s: %w", peerAddr, err)
	}
	return got, nil
}

// fetchObjectSymbol retrieves one symbol proof, retrying transient transport
// and wire-decode failures a bounded number of times. It returns ok=false only
// when the symbol could not be obtained after retries (the caller then moves
// on to a repair symbol). A proof that verifies against the codebook root is
// returned with ok=true; authentication happens at absorb time, so this method
// never has to decide trust.
func (f *BlockFetcher) fetchObjectSymbol(ctx context.Context, client *http.Client, peerAddr string, descID [32]byte, gen int, index uint32) (objects.SymbolProof, bool) {
	reqBody, err := json.Marshal(GetObjectSymbolRequest{DescriptorID: descID[:], Generation: uint32(gen), Index: index})
	if err != nil {
		return objects.SymbolProof{}, false
	}
	for attempt := 0; attempt <= objectSymbolRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return objects.SymbolProof{}, false
		}
		proofRaw, err := f.cfg.Identities.querySealed(ctx, client, peerAddr, KindGetObjectSymbol, reqBody,
			sealedReplyBound(maxObjectSymbolReplyBytes))
		if err != nil {
			continue
		}
		proof, err := objects.DecodeSymbolProof(proofRaw)
		if err != nil {
			continue
		}
		return proof, true
	}
	return objects.SymbolProof{}, false
}
