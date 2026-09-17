package consensus

import (
	"encoding/json"
	"fmt"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// ProposalEnvelope is the bft_rounds_v1 wrapper a current-round leader signs.
// The proposed *value* is the block; the envelope leader may differ from
// block.ValidatorAddr when a later leader re-proposes a locked body.
type ProposalEnvelope struct {
	Height     uint64          `json:"height"`
	Round      uint32          `json:"round"`
	BlockHash  [32]byte        `json:"block_hash"`
	LeaderAddr qcrypto.Address `json:"leader_addr"`
	// POLRound is -1 when this proposal is not justified by a prior POL.
	POLRound  int32    `json:"pol_round"`
	POLHash   [32]byte `json:"pol_hash"`
	Timestamp int64    `json:"timestamp"`
	Signature []byte   `json:"signature,omitempty"`
}

type envelopeBody struct {
	Type       string          `json:"type"`
	Height     uint64          `json:"height"`
	Round      uint32          `json:"round"`
	BlockHash  [32]byte        `json:"block_hash"`
	LeaderAddr qcrypto.Address `json:"leader_addr"`
	POLRound   int32           `json:"pol_round"`
	POLHash    [32]byte        `json:"pol_hash"`
	Timestamp  int64           `json:"timestamp"`
}

func (e *ProposalEnvelope) digest() ([32]byte, error) {
	b, err := json.Marshal(envelopeBody{
		Type:       "proposal_envelope",
		Height:     e.Height,
		Round:      e.Round,
		BlockHash:  e.BlockHash,
		LeaderAddr: e.LeaderAddr,
		POLRound:   e.POLRound,
		POLHash:    e.POLHash,
		Timestamp:  e.Timestamp,
	})
	if err != nil {
		return [32]byte{}, err
	}
	return qcrypto.Hash32(b), nil
}

func (e *ProposalEnvelope) Sign(priv *qcrypto.PrivateKey) error {
	h, err := e.digest()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, h[:], qcrypto.CtxVote)
	if err != nil {
		return err
	}
	e.Signature = sig
	return nil
}

func (e *ProposalEnvelope) Verify(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("envelope pubkey: %w", err)
	}
	if qcrypto.DeriveAddress(pub) != e.LeaderAddr {
		return fmt.Errorf("envelope claims leader %s but key belongs to %s",
			e.LeaderAddr, qcrypto.DeriveAddress(pub))
	}
	h, err := e.digest()
	if err != nil {
		return err
	}
	if !qcrypto.Verify(pub, h[:], qcrypto.CtxVote, e.Signature) {
		return fmt.Errorf("invalid proposal envelope signature from %s", e.LeaderAddr)
	}
	return nil
}

// RoundChange is an authenticated vote to leave the previous round. The new
// leader needs a quorum of these for (height, newRound) before proposing.
type RoundChange struct {
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	LockedHash    [32]byte        `json:"locked_hash"`
	LockedRound   int32           `json:"locked_round"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Timestamp     int64           `json:"timestamp"`
	Signature     []byte          `json:"signature,omitempty"`
}

type roundChangeBody struct {
	Type          string          `json:"type"`
	Height        uint64          `json:"height"`
	Round         uint32          `json:"round"`
	LockedHash    [32]byte        `json:"locked_hash"`
	LockedRound   int32           `json:"locked_round"`
	ValidatorAddr qcrypto.Address `json:"validator_addr"`
	Timestamp     int64           `json:"timestamp"`
}

func (r *RoundChange) digest() ([32]byte, error) {
	b, err := json.Marshal(roundChangeBody{
		Type:          "round_change",
		Height:        r.Height,
		Round:         r.Round,
		LockedHash:    r.LockedHash,
		LockedRound:   r.LockedRound,
		ValidatorAddr: r.ValidatorAddr,
		Timestamp:     r.Timestamp,
	})
	if err != nil {
		return [32]byte{}, err
	}
	return qcrypto.Hash32(b), nil
}

func (r *RoundChange) Sign(priv *qcrypto.PrivateKey) error {
	h, err := r.digest()
	if err != nil {
		return err
	}
	sig, err := qcrypto.Sign(priv, h[:], qcrypto.CtxVote)
	if err != nil {
		return err
	}
	r.Signature = sig
	return nil
}

func (r *RoundChange) Verify(pubKeyBytes []byte) error {
	pub, err := qcrypto.PublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("round-change pubkey: %w", err)
	}
	if qcrypto.DeriveAddress(pub) != r.ValidatorAddr {
		return fmt.Errorf("round-change claims %s but key belongs to %s",
			r.ValidatorAddr, qcrypto.DeriveAddress(pub))
	}
	h, err := r.digest()
	if err != nil {
		return err
	}
	if !qcrypto.Verify(pub, h[:], qcrypto.CtxVote, r.Signature) {
		return fmt.Errorf("invalid round-change signature from %s", r.ValidatorAddr)
	}
	return nil
}
