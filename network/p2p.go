// Package network — P2P block broadcasting for Splendor Quantum Chain.
//
// Architecture: Push-based (like geth), not poll-based.
//
// When a validator produces a block:
//   1. It calls Broadcaster.Broadcast(block) immediately
//   2. The broadcaster sends POST /block to every known peer
//   3. Each receiving peer calls ExecuteBlock() immediately
//   4. The RPC node is always within milliseconds of the validator
//
// This is true realtime sync — no polling, no intervals.
// The RPC node's 1s polling loop is a fallback for missed blocks only.
//
// Peer announcement:
//   When a node starts and connects to a peer, it sends POST /peers/announce
//   with its own address. This makes bidirectional peer tracking work.
//   Validator can then also push blocks to the RPC node.
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

// Broadcaster pushes newly produced blocks to all known peers immediately.
// This provides true realtime sync — peers receive blocks within milliseconds
// of the validator producing them, without any polling interval.
type Broadcaster struct {
	mu           sync.RWMutex
	peers        map[string]struct{} // peer RPC URLs
	validatorPK  []byte
	client       *http.Client
	applyBlock   func(*types.Block) error // local apply callback (for loopback, if needed)
}

// NewBroadcaster creates a new block broadcaster.
func NewBroadcaster(validatorPK []byte) *Broadcaster {
	return &Broadcaster{
		peers:       make(map[string]struct{}),
		validatorPK: validatorPK,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// AddPeer registers a peer URL to receive broadcast blocks.
func (b *Broadcaster) AddPeer(url string) {
	if url == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.peers[url] = struct{}{}
	log.Debug().Str("peer", url).Msg("broadcaster: peer added")
}

// RemovePeer removes a peer from the broadcast list.
func (b *Broadcaster) RemovePeer(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.peers, url)
}

// PeerCount returns the number of broadcast targets.
func (b *Broadcaster) PeerCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.peers)
}

// Broadcast sends a block to all known peers concurrently.
// Each peer receives the block via POST /block.
// Failed peers are logged but do not cause errors.
func (b *Broadcaster) Broadcast(block *types.Block) {
	data, err := json.Marshal(block)
	if err != nil {
		log.Error().Err(err).Msg("broadcaster: failed to marshal block")
		return
	}

	b.mu.RLock()
	peers := make([]string, 0, len(b.peers))
	for peer := range b.peers {
		peers = append(peers, peer)
	}
	b.mu.RUnlock()

	if len(peers) == 0 {
		return
	}

	log.Debug().
		Uint64("height", block.Header.Height).
		Int("peers", len(peers)).
		Msg("broadcaster: pushing block to peers")

	for _, peer := range peers {
		go func(peerURL string) {
			if err := b.sendBlock(peerURL, data); err != nil {
				log.Warn().Err(err).Str("peer", peerURL).Msg("broadcaster: block push failed")
			} else {
				log.Debug().Str("peer", peerURL).Uint64("height", block.Header.Height).
					Msg("broadcaster: block pushed")
			}
		}(peer)
	}
}

// sendBlock sends a serialised block to a single peer.
func (b *Broadcaster) sendBlock(peerURL string, blockData []byte) error {
	url := peerURL + "/block"
	resp, err := b.client.Post(url, "application/json", bytes.NewReader(blockData))
	if err != nil {
		return fmt.Errorf("POST /block failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned %d", resp.StatusCode)
	}
	return nil
}

// RunBroadcastLoop reads blocks from blockCh and broadcasts each one.
// Run in a goroutine alongside the validator consensus loop.
func (b *Broadcaster) RunBroadcastLoop(blockCh <-chan *types.Block) {
	for block := range blockCh {
		b.Broadcast(block)
	}
}

// AnnounceSelf announces this node's own address to all known peers.
// Call this after adding bootstrap peers so the peers can add us back
// (enabling bidirectional block propagation).
func (b *Broadcaster) AnnounceSelf(selfURL string, selfHeight uint64) {
	b.mu.RLock()
	peers := make([]string, 0, len(b.peers))
	for peer := range b.peers {
		peers = append(peers, peer)
	}
	b.mu.RUnlock()

	announcement := &PeerInfo{
		Addr:     selfURL,
		LastSeen: time.Now().Unix(),
		Height:   selfHeight,
		Version:  "1.0.0-quantum",
	}
	data, _ := json.Marshal(announcement)

	for _, peer := range peers {
		go func(peerURL string) {
			url := peerURL + "/peers/announce"
			resp, err := b.client.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				log.Debug().Err(err).Str("peer", peerURL).Msg("announce: failed")
				return
			}
			defer resp.Body.Close()
			log.Debug().Str("peer", peerURL).Str("self", selfURL).Msg("announce: sent")
		}(peer)
	}
}

// HandleIncomingBlock processes a raw block JSON received from a peer.
// It validates the block's Dilithium signature and calls applyBlock.
func HandleIncomingBlock(body []byte, validatorPK []byte, applyBlock func(*types.Block) error) error {
	var block types.Block
	if err := json.Unmarshal(body, &block); err != nil {
		return fmt.Errorf("invalid block JSON: %w", err)
	}

	// Structural validation
	if err := block.Validate(); err != nil {
		return fmt.Errorf("block validation failed: %w", err)
	}

	// Dilithium signature verification (if pubkey is known)
	if len(validatorPK) > 0 {
		if err := block.VerifySignature(validatorPK); err != nil {
			return fmt.Errorf("invalid validator signature: %w", err)
		}
	}

	// Apply to local chain
	if err := applyBlock(&block); err != nil {
		return fmt.Errorf("block application failed: %w", err)
	}

	log.Debug().
		Uint64("height", block.Header.Height).
		Str("hash", fmt.Sprintf("%x", block.Hash[:8])).
		Msg("p2p: block received and applied")

	return nil
}
