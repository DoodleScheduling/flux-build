package cache

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"

	"github.com/doodlescheduling/flux-build/internal/helm/chart"
)

type Null struct {
	dir string
}

func (c *Null) filepath(basename string) string {
	randBytes := make([]byte, 16)
	_, _ = rand.Read(randBytes)
	return filepath.Join(c.dir, basename+"-"+hex.EncodeToString(randBytes)+".tgz")
}

// GetOrLock returns path of Helm chart to store to or read from and a key to unlock.
// If the key is nil, the file is Nulld already and can be used.
func (c *Null) GetOrLock(repo string, ref chart.RemoteReference) (string, any, error) {
	fn := basename(repo, ref)
	return c.filepath(fn), nil, nil
}

// SetUnlock unlocks Helm chart by the key.
// It's safe to pass a nil.
func (c *Null) SetUnlock(a any) error {
	return nil
}
