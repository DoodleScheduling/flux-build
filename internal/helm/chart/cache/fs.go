package cache

import (
	"fmt"
	"path/filepath"

	"github.com/doodlescheduling/flux-build/internal/helm/chart"
	"github.com/gofrs/flock"
)

const lockSuffix = ".lock"

type FS struct {
	dir string
}

// GetOrLock returns path of Helm chart to store to or read from and a key to unlock.
// If the key is nil, the file is FSd already and can be used.
func (c *FS) GetOrLock(repo string, ref chart.RemoteReference) (string, any, error) {
	fileName := basename(repo, ref)
	fileName += ".tgz"
	fileName = filepath.Join(c.dir, fileName)

	lockFileName := fileName + lockSuffix
	fileLock := flock.New(lockFileName)

	return fileName, fileLock, fileLock.Lock()
}

// SetUnlock unlocks Helm chart by the key.
// It's safe to pass a nil.
func (c *FS) SetUnlock(a any) error {
	fileLock, ok := a.(*flock.Flock)
	if !ok {
		return fmt.Errorf("unlock failed, can't convert to *flock.Flock, type is %t", a)
	}

	return fileLock.Unlock()
}
