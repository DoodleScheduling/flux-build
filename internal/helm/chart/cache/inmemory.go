package cache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"

	memcache "github.com/doodlescheduling/flux-build/internal/cache"
	"github.com/doodlescheduling/flux-build/internal/helm/chart"
)

type InMemory struct {
	dir   string
	cache *memcache.Cache[CacheKey]
	// pathMap stores the mapping from cache keys to their file paths
	// This ensures we use the same path that was initially locked
	pathMap map[CacheKey]string
}

func (c *InMemory) filepath(basename string) string {
	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	return filepath.Join(c.dir, basename+"-"+hex.EncodeToString(randBytes)+".tgz")
}

// GetOrLock returns path of Helm chart to store to or read from and a key to unlock.
// If the key is nil, the file is InMemory already and can be used.
func (c *InMemory) GetOrLock(repo string, ref chart.RemoteReference) (string, any, error) {
	fn := basename(repo, ref)

	key := CacheKey{RemoteReference: ref, Repo: repo}
	p, ok := c.cache.GetOrLock(key)
	if ok {
		// Chart is already cached, return the stored path
		return p.(string), nil, nil
	}

	// Generate a new path for this cache entry
	path := c.filepath(fn)

	// Store the path mapping so SetUnlock can use the same path
	if c.pathMap == nil {
		c.pathMap = make(map[CacheKey]string)
	}
	c.pathMap[key] = path

	return path, key, nil
}

// SetUnlock unlocks Helm chart by the key.
// It's safe to pass a nil.
func (c *InMemory) SetUnlock(a any) error {
	if a == nil {
		return nil
	}

	key, ok := a.(CacheKey)
	if !ok {
		return fmt.Errorf("unlock failed, can't convert to InMemoryKey, type is %t", a)
	}

	// Use the same path that was generated in GetOrLock
	path, exists := c.pathMap[key]
	if !exists {
		// Fallback to generating a new path if mapping doesn't exist
		// This shouldn't happen in normal operation
		path = c.filepath(basename(key.Repo, key.RemoteReference))
	}

	// Set the cache entry with the consistent path
	c.cache.SetUnlock(key, path)

	// Clean up the path mapping since it's no longer needed
	if c.pathMap != nil {
		delete(c.pathMap, key)
	}

	return nil
}
