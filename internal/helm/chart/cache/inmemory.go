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
}

func (c *InMemory) filepath(basename string) string {
	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	return filepath.Join(c.dir, basename+"-"+hex.EncodeToString(randBytes)+".tgz")
}

// GetOrLock returns path of Helm chart to store to or read from and a key to unlock.
// If the key is nil, the file is InMemoryd already and can be used.
func (c *InMemory) GetOrLock(repo string, ref chart.RemoteReference) (string, any, error) {
	fn := basename(repo, ref)

	key := CacheKey{RemoteReference: ref, Repo: repo}
	p, ok := c.cache.GetOrLock(key)
	if ok {
		return p.(string), nil, nil
	}
	return c.filepath(fn), key, nil
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
	c.cache.SetUnlock(key, c.filepath(basename(key.Repo, key.RemoteReference)))
	return nil
}
