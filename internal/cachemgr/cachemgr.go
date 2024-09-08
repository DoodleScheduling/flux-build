// cachemgr provides single way to use different caches.
package cachemgr

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"

	"github.com/doodlescheduling/flux-build/internal/cache"
	"github.com/doodlescheduling/flux-build/internal/fcache"
	"github.com/doodlescheduling/flux-build/internal/helm/chart"
	"github.com/doodlescheduling/flux-build/internal/helm/repository"
)

// CacheType is enum of supported cache types.
type CacheType int

const (
	CacheTypeNone CacheType = iota
	CacheTypeInmemory
	CacheTypeFS
)

var cacheTypeConvertion = map[string]CacheType{
	"none":     CacheTypeNone,
	"inmemory": CacheTypeInmemory,
	"fs":       CacheTypeFS,
}

// StringToCacheType converts a string into corresponding CacheType.
func StringToCacheType(s string) (CacheType, error) {
	ct, ok := cacheTypeConvertion[s]
	if !ok {
		return CacheTypeNone, fmt.Errorf("CacheType %q isn't supported", s)
	}
	return ct, nil
}

type CacheKey struct {
	chart.RemoteReference
	Repo string
}

type Cache struct {
	dir      string
	inmemory *cache.Cache[CacheKey]
	fs       *fcache.Cache
}

func (c *Cache) filepath(basename string) string {
	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	return filepath.Join(c.dir, basename+"-"+hex.EncodeToString(randBytes)+".tgz")
}

func basename(repo string, ref chart.RemoteReference) string {
	h := fnv.New32a()
	h.Write([]byte(repo))
	return fmt.Sprintf("%x%%%s", h.Sum32(), ref.String())
}

// GetOrLock returns path of Helm chart to store to or read from and a key to unlock.
// If the key is nil, the file is cached already and can be used.
func (c *Cache) GetOrLock(repo string, ref chart.RemoteReference) (string, any, error) {
	fn := basename(repo, ref)
	if c.fs != nil {
		fn += ".tgz"
		path := c.fs.Filename(fn)
		flock, err := c.fs.GetOrLock(fn)
		if err != nil {
			return "", nil, err
		}
		if flock != nil {
			return path, flock, nil
		}
		return path, nil, nil
	}

	if c.inmemory != nil {
		key := CacheKey{RemoteReference: ref, Repo: repo}
		p, ok := c.inmemory.GetOrLock(key)
		if ok {
			return p.(string), nil, nil
		}
		return c.filepath(fn), key, nil
	}

	return c.filepath(fn), nil, nil
}

// SetUnlock unlocks Helm chart by the key.
// It's safe to pass a nil.
func (c *Cache) SetUnlock(a any) error {
	if a == nil {
		return nil
	}

	if c.fs != nil {
		fl, ok := a.(*os.File)
		if !ok {
			return fmt.Errorf("unlock failed, can't convert to *os.File, type is %t", a)
		}
		if fl == nil {
			// Nothing to unlock
			return nil
		}
		err := c.fs.SetUnlock(fl)
		if err != nil {
			return err
		}
		return nil
	}

	if c.inmemory != nil {
		key, ok := a.(CacheKey)
		if !ok {
			return fmt.Errorf("unlock failed, can't convert to CacheKey, type is %t", a)
		}
		c.inmemory.SetUnlock(key, c.filepath(basename(key.Repo, key.RemoteReference)))
		return nil
	}

	return nil
}

func (c *Cache) RepoGetOrLock(url string) repository.Downloader {
	if c.inmemory == nil {
		return nil
	}

	key := CacheKey{Repo: url}
	r, ok := c.inmemory.GetOrLock(key)
	if ok {
		return r.(repository.Downloader)
	}
	return nil
}

func (c *Cache) RepoSetUnlock(url string, repo repository.Downloader) {
	if repo == nil || c.inmemory == nil {
		return
	}

	key := CacheKey{Repo: url}
	c.inmemory.SetUnlock(key, repo)
}

func New(cacheType, cacheDir string) (*Cache, error) {
	ct, err := StringToCacheType(cacheType)
	if err != nil {
		return nil, err
	}

	switch ct {
	case CacheTypeInmemory:
		dir, err := os.MkdirTemp("", "helmcharts")
		if err != nil {
			return nil, err
		}
		return &Cache{dir: dir, inmemory: cache.New[CacheKey]()}, nil
	case CacheTypeFS:
		fc, err := fcache.New(cacheDir)
		if err != nil {
			return nil, err
		}
		return &Cache{dir: cacheDir, fs: fc, inmemory: cache.New[CacheKey]()}, nil
	}

	dir, err := os.MkdirTemp("", "helmcharts")
	if err != nil {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}
