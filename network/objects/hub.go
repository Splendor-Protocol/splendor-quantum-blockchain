package objects

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// maxRetiredObjects is how many superseded generations stay addressable
	// by their own descriptor id after a refresh, so a fetch already under
	// way can finish against the manifest it verified.
	//
	// One generation is at most SourceCount*SymbolSize of payload plus its
	// codebook, and the node publishes at most 256 x 16 KiB = 4 MiB, so the
	// ceiling here is the current object plus two retired ones: about 12 MiB
	// of payload. Two rather than one because a publisher that refreshes
	// twice in quick succession would otherwise tear the very transfer this
	// exists to protect.
	maxRetiredObjects = 2

	// RetiredTTL bounds how long a superseded generation is kept when nothing
	// supersedes it again.
	//
	// It has to outlive the fetcher's own aggregate deadline, or this whole
	// mechanism does not do the thing it exists for. That deadline scales with
	// the symbol count and reaches ten minutes for a full generation, while
	// this was two — so a transfer that started shortly before a refresh lost
	// its generation eight minutes before it was due to give up, which is
	// exactly the tear the retirement was added to prevent. A refresh fires
	// every 128 blocks, about four minutes at the default interval, so the
	// window was reachable in ordinary operation rather than only under load.
	//
	// Exported so the invariant can be pinned from the package that owns the
	// deadline: see TestRetiredObjectsOutliveTheFetchDeadline. Kept equal to
	// that cap rather than larger, because past it the fetcher has genuinely
	// stopped asking.
	RetiredTTL = 10 * time.Minute
)

// Hub holds at most one current ObjectSender for sealed serving. Replacing the
// current object drops the previous one so memory stays bounded to a single
// (possibly multi-generation) coded object.
type Hub struct {
	mu      sync.RWMutex
	current *ObjectSender
	byID    map[[32]byte]*ObjectSender
	// retired is oldest-first, so eviction is from the front. It never holds
	// more than maxRetiredObjects entries.
	retired []retiredObject
	// now is the clock, injectable so a test can age an entry without
	// sleeping. nil means time.Now.
	now func() time.Time
}

type retiredObject struct {
	id      [32]byte
	expires time.Time
	sender  *ObjectSender
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{byID: make(map[[32]byte]*ObjectSender)}
}

// Put publishes s as the current object. The object it replaces stays
// reachable by its own descriptor id until it is evicted by age or by newer
// refreshes; only the CURRENT object is served to a caller that does not name
// an id, so a joiner is always offered the freshest state.
//
// It used to delete the previous object outright, which made a refresh tear
// any transfer in progress: a fetcher reads the manifest, then asks for each
// symbol BY THE DESCRIPTOR ID it read there, so the moment the publisher
// rotated, every remaining symbol failed with "no coded object published" --
// discarding a verified manifest and every symbol already absorbed under it,
// and saying something untrue while doing it. The bound still exists, it is
// just not one.
func (h *Hub) Put(s *ObjectSender) {
	if s == nil || s.manifest == nil {
		return
	}
	id := s.Manifest().DescriptorID()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		prev := h.current
		prevID := prev.Manifest().DescriptorID()
		// Republishing the same object is a no-op, not a self-retirement.
		if prevID != id {
			h.retired = append(h.retired, retiredObject{
				id:      prevID,
				expires: h.clock().Add(RetiredTTL),
				sender:  prev,
			})
		}
	}
	h.current = s
	h.byID[id] = s
	h.evictLocked()
}

func (h *Hub) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// evictLocked drops retired objects that expired or fell out of the bound, and
// unmaps them so the codebook can be collected. The current object is never
// evicted here.
func (h *Hub) evictLocked() {
	now := h.clock()
	keep := h.retired[:0]
	for _, r := range h.retired {
		if now.Before(r.expires) {
			keep = append(keep, r)
			continue
		}
		h.dropLocked(r.id)
	}
	h.retired = keep
	for len(h.retired) > maxRetiredObjects {
		h.dropLocked(h.retired[0].id)
		h.retired = h.retired[1:]
	}
}

// dropLocked unmaps a retired id, unless the same object has meanwhile become
// current again.
func (h *Hub) dropLocked(id [32]byte) {
	if h.current != nil && h.current.Manifest().DescriptorID() == id {
		return
	}
	delete(h.byID, id)
}

// Current is the object last Put, or nil.
func (h *Hub) Current() *ObjectSender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.current
}

// Clear drops the current object. Used when a node wants to republish at a
// newer head; until the replacement is Put, ServeManifest/ServeSymbol report
// "no coded object published" and callers fall back to the plain paths.
func (h *Hub) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current = nil
	h.byID = make(map[[32]byte]*ObjectSender)
}

func (h *Hub) get(id [32]byte) *ObjectSender {
	// A node that publishes once and then goes quiet would otherwise hold its
	// retired generations for the life of the process; Put is the only other
	// place eviction runs, and by definition it is not running.
	h.mu.Lock()
	h.evictLocked()
	h.mu.Unlock()

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
