package worker

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolCap(t *testing.T) {
	wpOne := New(context.Background(), PoolOptions{
		Workers: 1,
	})

	defer wpOne.Close()
	if c := wpOne.Cap(); c != 1 {
		t.Errorf("got %d; want %d", c, 1)
	}

	wpCPU := New(context.Background(), PoolOptions{
		Workers: runtime.NumCPU(),
	})

	defer wpCPU.Close()
	if c := wpCPU.Cap(); c != runtime.NumCPU() {
		t.Errorf("got %d; want %d", c, 1)
	}
}

func TestWorkerPoolLen(t *testing.T) {
	wpOne := New(context.Background(), PoolOptions{
		Workers: 1,
	})

	defer wpOne.Close()
	if c := wpOne.Len(); c != 0 {
		t.Errorf("got %d; want %d", c, 0)
	}
}

func TestPool(t *testing.T) {
	var count atomic.Int64

	wp := New(context.Background(), PoolOptions{
		Workers: 5,
	})

	for i := 0; i <= 10; i++ {
		wp.Push(Task(func(ctx context.Context) error {
			time.Sleep(time.Millisecond * 100)
			count.Add(1)
			return nil
		}))
	}

	err := wp.Wait()
	if err != nil {
		t.Errorf("got %s; want nil", err.Error())
	}

	if count.Load() != 11 {
		t.Errorf("got %d; want %d", count.Load(), 11)
	}
}

func TestPoolWithError(t *testing.T) {
	wp := New(context.Background(), PoolOptions{
		Workers: 5,
	})

	wp.Push(Task(func(ctx context.Context) error {
		return errors.New("error")
	}))

	wp.Push(Task(func(ctx context.Context) error {
		return nil
	}))

	err := wp.Wait()
	if err == nil {
		t.Errorf("got nil; want error")
	}
}
