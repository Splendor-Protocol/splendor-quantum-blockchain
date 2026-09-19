package objects

import (
	"errors"
	"fmt"
)

// Cache holds a bounded set of coded objects for sealed serving, keyed by
// descriptor ID. It serves on-demand objects (e.g. block batches, one object
// per requested range) alongside the Hub's single current snapshot object;
// the oldest entry is evicted once the cache is full. A client that loses its
// object to eviction mid-transfer simply retries or falls back — cache
// eviction never corrupts, it only refuses.
type Cache struct {
	max     int
	entries map[[32]byte]*ObjectSender
	order   [][32]byte // FIFO, oldest first
}

// NewCache returns a cache holding up to max objects. max < 1 means 8.
func NewCache(max int) *Cache {
	if max < 1 {
		max = 8
	}
	return &Cache{max: max, entries: make(map[[32]byte]*ObjectSender, max)}
}

// Put stores s, replacing any object with the same descriptor ID and evicting
// the oldest entry when the cache is full.
func (c *Cache) Put(s *ObjectSender) {
	if s == nil || s.manifest == nil {
		return
	}
	id := s.Manifest().DescriptorID()
	if _, exists := c.entries[id]; exists {
		c.entries[id] = s
		return
	}
	for len(c.order) >= c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.order = append(c.order, id)
	c.entries[id] = s
}

// Get returns the object for descID. Empty descID is refused — the cache has
// no notion of a current object; that is the Hub's.
func (c *Cache) Get(descID []byte) (*ObjectSender, error) {
	if len(descID) != 32 {
		return nil, errors.New("objects: descriptor id must be 32 bytes")
	}
	var id [32]byte
	copy(id[:], descID)
	if s, ok := c.entries[id]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("objects: no coded object published for %x", descID[:8])
}
