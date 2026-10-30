// Package network — Peer discovery for Splendor Quantum Chain.
//
// Discovery works in two layers:
//  1. Bootstrap: on startup, connect to a hardcoded or config-provided set of bootstrap peers.
//  2. Peer Exchange (PEX): periodically request the peer list from known peers
//     and add newly discovered peers to the pool.
//
// Discovery itself is plain HTTP. Block and announce traffic is carried over the
// ML-KEM-768 sealed channel in pqchannel.go; see network/p2p.go.
// The P2P protocol is intentionally minimal — blocks are pushed via POST /block,
// and peer lists are exchanged via GET /peers and POST /peers/announce.
package network

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// PeerInfo contains the minimal information needed to connect to a peer.
type PeerInfo struct {
	// Addr is the peer's RPC base URL, e.g. "http://1.2.3.4:8080"
	Addr string `json:"addr"`

	// LastSeen is the unix timestamp of the last successful contact.
	LastSeen int64 `json:"last_seen"`

	// Height is the last known chain height of this peer.
	Height uint64 `json:"height"`

	// Version is the node software version reported by this peer.
	Version string `json:"version,omitempty"`
}

// PeerPool manages the set of known peers.
// It is goroutine-safe and supports automatic eviction of stale peers.
type PeerPool struct {
	mu      sync.RWMutex
	peers   map[string]*PeerInfo // addr → info
	maxSize int

	// self is this node's own announced address (excluded from outbound connections)
	self string

	client *http.Client
}

// NewPeerPool creates a new peer pool.
func NewPeerPool(self string, maxSize int) *PeerPool {
	if maxSize <= 0 {
		maxSize = 50
	}
	return &PeerPool{
		peers:   make(map[string]*PeerInfo),
		maxSize: maxSize,
		self:    self,
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: noRedirect,
			Transport:     SafeTransport(5 * time.Second),
		},
	}
}

// Add adds or updates a peer in the pool.
// Ignores our own address and peers exceeding maxSize.
func (p *PeerPool) Add(info *PeerInfo) {
	if info.Addr == p.self || info.Addr == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.peers) >= p.maxSize {
		// Evict the oldest peer to make room
		p.evictOldestLocked()
	}
	if existing, ok := p.peers[info.Addr]; ok {
		// Update existing peer info
		existing.LastSeen = info.LastSeen
		if info.Height > existing.Height {
			existing.Height = info.Height
		}
		if info.Version != "" {
			existing.Version = info.Version
		}
		return
	}
	cp := *info
	p.peers[info.Addr] = &cp
	log.Debug().Str("peer", info.Addr).Msg("peer added to pool")
}

// Remove removes a peer from the pool.
func (p *PeerPool) Remove(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.peers, addr)
}

// All returns a copy of all known peers.
func (p *PeerPool) All() []*PeerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*PeerInfo, 0, len(p.peers))
	for _, info := range p.peers {
		cp := *info
		result = append(result, &cp)
	}
	return result
}

// Addrs returns the list of all peer addresses.
func (p *PeerPool) Addrs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	addrs := make([]string, 0, len(p.peers))
	for addr := range p.peers {
		addrs = append(addrs, addr)
	}
	return addrs
}

// Count returns the number of known peers.
func (p *PeerPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.peers)
}

// evictOldestLocked removes the peer with the oldest LastSeen time.
// Must be called with p.mu held.
func (p *PeerPool) evictOldestLocked() {
	var oldest *PeerInfo
	var oldestAddr string
	for addr, info := range p.peers {
		if oldest == nil || info.LastSeen < oldest.LastSeen {
			oldest = info
			oldestAddr = addr
		}
	}
	if oldestAddr != "" {
		delete(p.peers, oldestAddr)
		log.Debug().Str("peer", oldestAddr).Msg("peer evicted (pool full)")
	}
}

// Discoverer handles bootstrap + periodic peer exchange.
type Discoverer struct {
	pool     *PeerPool
	selfAddr string
	interval time.Duration
	stopCh   chan struct{}

	// identity signs outbound announcements so peers can attribute them to this
	// node rather than to whoever holds the shared write-plane token.
	identity *NodeIdentity
}

// NewDiscoverer creates a new peer discoverer.
func NewDiscoverer(pool *PeerPool, selfAddr string, interval time.Duration) *Discoverer {
	if interval == 0 {
		interval = 60 * time.Second
	}
	return &Discoverer{
		pool:     pool,
		selfAddr: selfAddr,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// SetIdentity supplies the node key used to sign announcements.
func (d *Discoverer) SetIdentity(id *NodeIdentity) { d.identity = id }

// Bootstrap connects to the initial set of bootstrap peer addresses.
// For each bootstrap peer, it fetches the peer's peer list and adds them.
func (d *Discoverer) Bootstrap(bootstrapAddrs []string) {
	for _, addr := range bootstrapAddrs {
		d.pool.Add(&PeerInfo{
			Addr:     addr,
			LastSeen: time.Now().Unix(),
		})
		// Immediately fetch peers from this bootstrap node
		go d.fetchPeersFrom(addr)
	}
	log.Info().Int("bootstrap_peers", len(bootstrapAddrs)).Msg("discovery: bootstrapped")
}

// Start begins the periodic peer exchange loop.
func (d *Discoverer) Start() {
	go d.run()
}

// Stop halts the discovery loop.
func (d *Discoverer) Stop() {
	close(d.stopCh)
}

// run is the main discovery loop.
func (d *Discoverer) run() {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.exchangePeers()
		case <-d.stopCh:
			return
		}
	}
}

// exchangePeers queries all known peers for their peer lists.
func (d *Discoverer) exchangePeers() {
	peers := d.pool.Addrs()
	for _, addr := range peers {
		go d.fetchPeersFrom(addr)
	}
}

// fetchPeersFrom calls GET {addr}/peers and adds any new peers to the pool.
func (d *Discoverer) fetchPeersFrom(addr string) {
	url := addr + "/peers"
	resp, err := d.pool.client.Get(url)
	if err != nil {
		log.Debug().Err(err).Str("peer", addr).Msg("discovery: peer fetch failed")
		return
	}
	defer resp.Body.Close()

	var peerList []*PeerInfo
	if err := json.NewDecoder(resp.Body).Decode(&peerList); err != nil {
		return
	}

	now := time.Now().Unix()
	// Update this peer's LastSeen
	d.pool.Add(&PeerInfo{Addr: addr, LastSeen: now})

	// Add newly discovered peers. These URLs come from a remote (untrusted) peer's
	// /peers response, so they must pass the SSRF guard before we ever dial them —
	// otherwise PEX is an unguarded ingress that bypasses the /peers/announce check.
	for _, p := range peerList {
		if p.Addr == d.selfAddr || p.Addr == "" {
			continue
		}
		if err := IsSafePeerURL(p.Addr); err != nil {
			log.Debug().Str("peer", p.Addr).Err(err).Msg("discovery: rejected unsafe discovered peer")
			continue
		}
		d.pool.Add(&PeerInfo{
			Addr:     p.Addr,
			LastSeen: now,
			Height:   p.Height,
			Version:  p.Version,
		})
	}
}

// Announce announces this node's existence to all known peers.
// Called after startup so peers know about us.
func (d *Discoverer) Announce(selfInfo *PeerInfo) {
	// Sign once, not per peer: a fresh nonce for each recipient would be
	// pointless work, and every receiver keeps its own nonce cache, so one
	// announcement reaching all of them is not a replay to any of them.
	payload := any(selfInfo)
	if d.identity != nil {
		signed, err := SignAnnounce(d.identity, *selfInfo)
		if err != nil {
			log.Warn().Err(err).Msg("discovery: could not sign announcement, sending unsigned")
		} else {
			payload = signed
		}
	}

	peers := d.pool.Addrs()
	for _, addr := range peers {
		go func(peerAddr string) {
			url := peerAddr + "/peers/announce"
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			resp, err := d.pool.client.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				return
			}
			resp.Body.Close()
		}(addr)
	}
}

// EvictStale removes peers that haven't been seen in the given duration.
func (d *Discoverer) EvictStale(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge).Unix()
	d.pool.mu.Lock()
	defer d.pool.mu.Unlock()
	for addr, info := range d.pool.peers {
		if info.LastSeen < cutoff {
			delete(d.pool.peers, addr)
			log.Debug().Str("peer", addr).Msg("discovery: stale peer evicted")
		}
	}
}

// UpdatePeerHeight records a peer's chain height, but only ever moves it
// forward.
//
// Callers report two different kinds of evidence under this one method: a
// fresh /status poll (which really does know the peer's current height, up
// or down) and "I just applied block h that this peer served me" (which only
// proves the peer was AT LEAST h at some point — never that h is a ceiling).
// Letting the second kind lower a height recorded by the first would make a
// syncer that just pulled block 40 from a peer known to be at 100 report that
// peer as caught-up at 40, hiding 60 blocks of real work behind its own
// bookkeeping. A height can go stale (LastSeen still advances so eviction
// still works) but never regress.
func (p *PeerPool) UpdatePeerHeight(addr string, height uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if info, ok := p.peers[addr]; ok {
		if height > info.Height {
			info.Height = height
		}
		info.LastSeen = time.Now().Unix()
	}
}

// BestPeerHeight returns the maximum known chain height among all peers.
// Useful for detecting whether this node is behind.
func (p *PeerPool) BestPeerHeight() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var best uint64
	for _, info := range p.peers {
		if info.Height > best {
			best = info.Height
		}
	}
	return best
}

// PeersAboveHeight returns addresses of peers whose known height exceeds the given height.
// Used to identify peers to sync blocks from.
func (p *PeerPool) PeersAboveHeight(height uint64) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var result []string
	for addr, info := range p.peers {
		if info.Height > height {
			result = append(result, addr)
		}
	}
	return result
}

// FormatPeerList formats the peer pool as a JSON-serialisable slice for the /peers endpoint.
func (p *PeerPool) FormatPeerList(selfAddr string, selfHeight uint64) []*PeerInfo {
	all := p.All()
	// Include self
	all = append(all, &PeerInfo{
		Addr:     selfAddr,
		LastSeen: time.Now().Unix(),
		Height:   selfHeight,
	})
	return all
}

// SyncStatus describes whether this node is synced with its peers.
type SyncStatus struct {
	IsSyncing   bool   `json:"is_syncing"`
	LocalHeight uint64 `json:"local_height"`
	BestHeight  uint64 `json:"best_height"`
	PeerCount   int    `json:"peer_count"`
}

// GetSyncStatus returns the current sync status relative to known peers.
func (p *PeerPool) GetSyncStatus(localHeight uint64) SyncStatus {
	best := p.BestPeerHeight()
	return SyncStatus{
		IsSyncing:   best > localHeight+1,
		LocalHeight: localHeight,
		BestHeight:  best,
		PeerCount:   p.Count(),
	}
}

// dialClient is a shared client for peer /status probes. The full-node sync loop
// polls this once per second per peer; a fresh http.Client (and TCP connection)
// per call forfeits keep-alive and wastes sockets, so reuse one transport.
var dialClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: noRedirect,
	Transport:     SafeTransport(5 * time.Second),
}

// DialPeer checks if a peer is reachable by calling GET {addr}/status.
// Returns the peer's reported height, or an error if unreachable.
func DialPeer(addr string) (uint64, error) {
	return ProbePeer(addr, nil)
}

// ErrPeerOnAnotherChain means a peer's /status names a genesis other than this
// node's: it is on another network, or on a chain that has since been reset.
var ErrPeerOnAnotherChain = errors.New("peer is on another chain")

// ProbePeer is DialPeer that also refuses a peer that is not on this node's
// chain. The probe is the handshake: the block-sync loop probes every pooled
// peer each second, so a wrong-network peer is dropped within a second of
// arriving, however it arrived (bootstrap, peer exchange, announce). Its
// blocks fail validation regardless; this keeps the syncer off it.
//
// It compares genesis hashes, not chain ids: block 0's header carries the
// chain id, so a different id means a different genesis, while a reset chain
// keeps its id and mints a new genesis. Every node since v1.2 reports
// genesis_hash in /status, so this asks peers for nothing they do not already
// send. The check is skipped, not failed, when either side cannot name its
// genesis (this node has not adopted one yet; the peer holds no block 0):
// block validation still refuses another chain's blocks, this only saves
// syncing from it.
func ProbePeer(addr string, localGenesis func() ([32]byte, bool)) (uint64, error) {
	resp, err := dialClient.Get(addr + "/status")
	if err != nil {
		return 0, fmt.Errorf("peer %s unreachable: %w", addr, err)
	}
	defer resp.Body.Close()

	var status struct {
		Height      uint64 `json:"height"`
		GenesisHash string `json:"genesis_hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return 0, fmt.Errorf("peer %s returned invalid status: %w", addr, err)
	}
	if localGenesis == nil || status.GenesisHash == "" {
		return status.Height, nil
	}
	ours, known := localGenesis()
	if !known {
		return status.Height, nil
	}
	theirs, err := hex.DecodeString(status.GenesisHash)
	if err != nil || len(theirs) != len(ours) {
		return 0, fmt.Errorf("peer %s reported an unreadable genesis_hash %q", addr, status.GenesisHash)
	}
	if !bytes.Equal(theirs, ours[:]) {
		return 0, fmt.Errorf("%w: %s reports genesis %x, this node is on %x",
			ErrPeerOnAnotherChain, addr, theirs[:8], ours[:8])
	}
	return status.Height, nil
}
