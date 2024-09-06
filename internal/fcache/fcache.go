package fcache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const lockSuffix = ".lock"

// ready is a random constant to write into lock file when data is ready.
const ready byte = 254

type Cache struct {
	dir string
}

func New(dir string) (*Cache, error) {
	ds, err := os.Stat(dir)
	if err != nil || !ds.IsDir() {
		if err := os.MkdirAll(dir, 0775); err != nil {
			return nil, err
		}
	}

	return &Cache{dir}, nil
}

func isReady(f *os.File) (bool, error) {
	b, err := io.ReadAll(f)
	if err != nil {
		return false, err
	}
	if len(b) > 0 && b[0] == ready {
		return true, nil
	}
	return false, nil
}

func openLock(filename string, flag int) (*os.File, error) {
	f, err := os.OpenFile(filename, flag, 0664)
	if err != nil {
		return nil, fmt.Errorf("Can't open lock file %s: %v", filename, err)
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("Can't lock file %s: %v", filename, err)
	}

	b, err := isReady(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("Can't check if file %s is ready: %v", filename, err)
	}
	if b {
		// The data is there and already.
		f.Close()
		return nil, nil
	}

	return f, nil
}

// Filename generates full file name.
func (c *Cache) Filename(filename string) string {
	return filepath.Join(c.dir, filename)
}

// GetOrLock returns not nil file handler if lock is taken and caller should create data file
// or returns nil if the data file is ready to be read.
func (c *Cache) GetOrLock(filename string) (*os.File, error) {
	filename = c.Filename(filename) + lockSuffix
	fs, err := os.Stat(filename)
	if err != nil {
		// The file doesn't exist. Create and try to lock.
		return openLock(filename, os.O_CREATE|os.O_RDWR)
	} else if fs.Size() == 0 {
		// The file is there, but data isn't ready. Try to lock.
		return openLock(filename, os.O_RDWR)
	}
	// File should be ready to be used.
	f, err := os.OpenFile(filename, os.O_RDWR, 0664)
	if err != nil {
		return nil, fmt.Errorf("Can't reopen lock file %s: %v", filename, err)
	}
	defer f.Close()
	b, err := isReady(f)
	if err != nil {
		return nil, fmt.Errorf("Can't check if file is ready: %v", err)
	}
	if b {
		// The data is there and already.
		return nil, nil
	}
	return nil, fmt.Errorf("The lock %s is there and non empty but has wrong data", filename)
}

// SetUnlock writes constant to mark that data is ready and releases the lock.
func (c *Cache) SetUnlock(file *os.File) error {
	defer file.Close()
	_, err := file.Write([]byte{ready})
	if err != nil {
		return fmt.Errorf("Can't write into lock file: %v", err)
	}
	return nil
}
