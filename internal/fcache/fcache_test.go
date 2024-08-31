package fcache

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestCache(t *testing.T) {
	t.Parallel()
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for r := 0; r < 100; r++ {
		r := r // Remove when govet [loopclosure] will be removed.
		t.Run(fmt.Sprintf("run-%d", r), func(t *testing.T) {
			t.Parallel()
			file := fmt.Sprintf("test-%d.tgz", r)
			g := new(errgroup.Group)
			for n := 0; n < 20; n++ {
				n := n // Remove when govet [loopclosure] will be removed.
				g.Go(func() error {
					//time.Sleep(time.Millisecond)
					fl, err := c.GetOrLock(file)
					if err != nil {
						return err
					}
					if fl != nil {
						// Writer, should be only one.
						f, err := os.Create(c.Filename(file))
						if err != nil {
							return err
						}
						//time.Sleep(10 * time.Millisecond)
						_, err = f.Write([]byte("test\n"))
						if err != nil {
							return err
						}
						f.Close()
						t.Logf("%d, Wrote test\n", n)
						err = c.SetUnlock(fl)
						if err != nil {
							return err
						}
					} else {
						// Reader
						f, err := os.Open(c.Filename(file))
						if err != nil {
							return err
						}
						b, err := io.ReadAll(f)
						if err != nil {
							return err
						}
						f.Close()
						if !reflect.DeepEqual(b, []byte("test\n")) {
							return fmt.Errorf("read incorrect data from file: %v", string(b))
						}
						t.Logf("%2d, %s", n, string(b))
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
