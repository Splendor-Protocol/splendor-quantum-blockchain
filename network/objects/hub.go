package objects

import (
	"errors"
	"fmt"
	"sync"
)

// Hub holds at most one current ObjectSender for sealed serving. Replacing the
// current object drops the previous one so memory stays bounded to a single
// (possibly multi-generation) coded object.
type Hub struct {
	mu      sync.RWMutex
	current *ObjectSender
	byID    map[[32]byte]*ObjectSender
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{byID: make(map[[32]byte]*ObjectSender)}
}

// Put publishes s as the current object, replacing any previous one.
func (h *Hub) Put(s *ObjectSender) {
	if s == nil || s.manifest == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		delete(h.byID, h.current.Manifest().DescriptorID())
	}
	h.current = s
	h.byID[s.Manifest().DescriptorID()] = s
}

// Current is the object last Put, or nil.
func (h *Hub) Current() *ObjectSender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.current
}

func (h *Hub) get(id [32]byte) *ObjectSender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if id == ([32]byte{}) {
		return h.current
	}
	return h.byID[id]
}

// ServeManifest returns the SQCO encoding of the requested (or current)
// object manifest. Empty descID means the current object.
func (h *Hub) ServeManifest(descID []byte) ([]byte, error) {
	s, err := h.lookup(descID)
	if err != nil {
		return nil, err
	}
	return EncodeObjectManifest(s.Manifest())
}

// ServeSymbol returns the SQCO encoding of one codebook proof from
// generation gen of the requested object.
func (h *Hub) ServeSymbol(descID []byte, generation uint32, index uint32) ([]byte, error) {
	s, err := h.lookup(descID)
	if err != nil {
		return nil, err
	}
	gen := int(generation)
	if gen < 0 || gen >= s.GenerationCount() {
		return nil, fmt.Errorf("objects: generation %d out of range [0,%d)", gen, s.GenerationCount())
	}
	if uint64(index) >= uint64(s.SymbolCount(gen)) {
		return nil, fmt.Errorf("objects: symbol index %d out of range [0,%d) in generation %d", index, s.SymbolCount(gen), gen)
	}
	proof, err := s.Proof(gen, int(index))
	if err != nil {
		return nil, err
	}
	return EncodeSymbolProof(proof)
}

func (h *Hub) lookup(descID []byte) (*ObjectSender, error) {
	var id [32]byte
	if len(descID) != 0 {
		if len(descID) != 32 {
			return nil, errors.New("objects: descriptor id must be 32 bytes")
		}
		copy(id[:], descID)
	}
	s := h.get(id)
	if s == nil {
		return nil, errors.New("objects: no coded object published")
	}
	return s, nil
}
