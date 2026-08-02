// Package network implements the P2P layer for Splendor Quantum Chain.
// In Phase 1 (single validator), full P2P is minimal — the validator broadcasts
// blocks to all connected full-node peers via HTTP POST.
// Peers validate the Dilithium block signature before accepting a block.
package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/splendor-protocol/splendor-quantum-blockchain/core/types"
)

// Peer represents a connected full-node peer.
type Peer struct {
	Addr string // e.g. "http://192.168.1.10:9001"
}

// Broadcaster sends newly produced blocks to all known peers.
type Broadcaster struct {
	mu    sync.RWMutex
	peers []Peer

	// validatorPubKey is the known validator Dilithium public key.
	// All incoming blocks must have a valid signature from this key.
	validatorPubKey []byte

	client *http.Client
}

// NewBroadcaster creates a new P2P broadcaster.
func NewBroadcaster(validatorPubKey []byte) *Broadcaster {
	return &Broadcaster{
		validatorPubKey: validatorPubKey,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// AddPeer registers a new peer address.
func (b *Broadcaster) AddPeer(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.peers {
		if p.Addr == addr {
			return // already known
		}
	}
	b.peers = append(b.peers, Peer{Addr: addr})
	log.Info().Str("peer", addr).Msg("peer added")
}

// RemovePeer removes a peer by address.
func (b *Broadcaster) RemovePeer(addr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.peers {
		if p.Addr == addr {
			b.peers = append(b.peers[:i], b.peers[i+1:]...)
			log.Info().Str("peer", addr).Msg("peer removed")
			return
		}
	}
}

// PeerCount returns the number of known peers.
func (b *Broadcaster) PeerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.peers)
}

// Broadcast sends a block to all peers concurrently.
// Each peer receives the block via POST /block.
// Errors are logged but do not stop broadcast to other peers.
func (b *Broadcaster) Broadcast(block *types.Block) {
	b.mu.RLock()
	peers := make([]Peer, len(b.peers))
	copy(peers, b.peers)
	b.mu.RUnlock()

	if len(peers) == 0 {
		return
	}

	data, err := json.Marshal(block)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal block for broadcast")
		return
	}

	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			url := p.Addr + "/block"
			resp, err := b.client.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				log.Warn().Err(err).Str("peer", p.Addr).Msg("block broadcast failed")
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				log.Warn().
					Str("peer", p.Addr).
					Int("status", resp.StatusCode).
					Msg("peer rejected block")
			}
		}(peer)
	}
	wg.Wait()
}

// RunBroadcastLoop reads blocks from the channel and broadcasts them to peers.
// Call this in a goroutine.
func (b *Broadcaster) RunBroadcastLoop(blockCh <-chan *types.Block) {
	for block := range blockCh {
		b.Broadcast(block)
	}
}

// HandleIncomingBlock validates and processes a block received from a peer.
// Returns an error if the block signature is invalid.
// onNewBlock is called with the validated block for further processing (e.g. chain sync).
func HandleIncomingBlock(data []byte, validatorPubKey []byte, onNewBlock func(*types.Block) error) error {
	var b types.Block
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("failed to parse incoming block: %w", err)
	}

	// Verify structure
	if err := b.Validate(); err != nil {
		return fmt.Errorf("invalid block structure: %w", err)
	}

	// Verify Dilithium signature from the known validator
	if err := b.VerifySignature(validatorPubKey); err != nil {
		return fmt.Errorf("block signature verification failed: %w", err)
	}

	// Hand off to chain
	return onNewBlock(&b)
}
