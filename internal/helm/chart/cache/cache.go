package cache

import (
	"fmt"
	"hash/fnv"
	"os"

	memcache "github.com/doodlescheduling/flux-build/internal/cache"
	"github.com/doodlescheduling/flux-build/internal/helm/chart"
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

type Interface interface {
	GetOrLock(repo string, ref chart.RemoteReference) (string, any, error)
	SetUnlock(a any) error
}

func New(cacheType, cacheDir string) (Interface, error) {
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
		return &InMemory{
			dir:     dir,
			cache:   memcache.New[CacheKey](),
			pathMap: make(map[CacheKey]string),
		}, nil
	case CacheTypeFS:
		err := os.MkdirAll(cacheDir, os.ModePerm)
		if err != nil {
			return nil, err
		}

		return &FS{dir: cacheDir}, nil
	}

	dir, err := os.MkdirTemp("", "helmcharts")
	if err != nil {
		return nil, err
	}
	return &Null{dir: dir}, nil
}

func basename(repo string, ref chart.RemoteReference) string {
	h := fnv.New32a()
	h.Write([]byte(repo))
	return fmt.Sprintf("%x%%%s", h.Sum32(), ref.String())
}
