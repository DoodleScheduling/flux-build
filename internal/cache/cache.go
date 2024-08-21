// Copyright (c) 2012-2019 Patrick Mylund Nielsen and the go-cache contributors
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Copyright 2022 The FluxCD contributors. All rights reserved.
// This package provides an in-memory cache
// derived from the https://github.com/patrickmn/go-cache
// package
// It has been modified in order to keep a small set of functions
// and to add a maxItems parameter in order to limit the number of,
// and thus the size of, items in the cache.

package cache

import (
	"fmt"
	"sync"
)

type Cache[K comparable] struct {
	// Items holds the elements in the cache.
	items map[K]any
	mu    sync.RWMutex
}

// ItemCount returns the number of items in the cache.
// This may include items that have expired, but have not yet been cleaned up.
func (c *Cache[K]) ItemCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Set adds an item to the cache, replacing any existing item.
func (c *Cache[K]) Set(key K, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = value
}

// Add an item to the cache, existing items will not be overwritten.
// To overwrite existing items, use Set.
func (c *Cache[K]) Add(key K, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, found := c.items[key]
	if found {
		return fmt.Errorf("Item %v already exists", key)
	}
	c.items[key] = value
	return nil
}

// Get an item from the cache. Returns the item or nil, and a bool indicating
// whether the key was found.
func (c *Cache[K]) Get(key K) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, found := c.items[key]
	if !found {
		return nil, false
	}
	return item, true
}

type valueLock chan struct{}

// GetOrLock returns an item from the cache or creats lock for the first requestor of specific key
// and locks others until the item will be set.
func (c *Cache[K]) GetOrLock(key K) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, found := c.items[key]
	if !found {
		// Create lock, return to the first caller.
		vl := make(valueLock)
		c.items[key] = vl
		return nil, false
	}
	if vl, ok := item.(valueLock); ok {
		// No value yet, unlock and block until ready.
		c.mu.Unlock()
		<-vl
		// Done waiting, re-locking.
		c.mu.Lock()
		item, found = c.items[key]
		if _, ok := item.(valueLock); !found || ok {
			// Can happen only if the cache was cleared while waiting or the cache is over capacity.
			return nil, false
		}
	}

	return item, true
}

// SetUnlock sets value for the key, if there was a lock for the key, unlocks it.
func (c *Cache[K]) SetUnlock(key K, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if v, found := c.items[key]; found {
		if vl, ok := v.(valueLock); ok {
			close(vl)
		}
	}

	c.items[key] = value
}

// Delete an item from the cache. Does nothing if the key is not in the cache.
func (c *Cache[K]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Clear all items from the cache.
// This reallocate the inderlying array holding the items,
// so that the memory used by the items is reclaimed.
func (c *Cache[K]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[K]any)
}

// New creates a new cache with the given configuration.
func New[K comparable]() *Cache[K] {
	c := &Cache[K]{
		items: make(map[K]any),
	}

	return c
}
