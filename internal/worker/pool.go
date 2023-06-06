package worker

import (
	"context"
	"sync"
)

type Pusher interface {
	Push(task Task)
}

type Waiter interface {
	Wait() error
}

type PoolOptions struct {
	Workers int
}

type pool struct {
	wgTasks sync.WaitGroup
	wgErr   sync.WaitGroup
	tasks   chan Task
	err     chan error
	workers chan struct{}
	ctx     context.Context
	lastErr error
	cancel  context.CancelFunc
}

func New(ctx context.Context, opts PoolOptions) *pool {
	ctx, cancel := context.WithCancel(ctx)

	p := &pool{
		cancel:  cancel,
		workers: make(chan struct{}, opts.Workers),
		tasks:   make(chan Task),
		err:     make(chan error, opts.Workers),
		ctx:     ctx,
	}

	return p.start()
}

type Task func(ctx context.Context) error

func (p *pool) Push(task Task) {
	p.wgTasks.Add(1)

	if p.ctx.Err() != nil {
		return
	}

	p.tasks <- task
}

// Cap returns the concurrent workers capacity, see New().
func (p *pool) Cap() int {
	return cap(p.workers)
}

// Len returns the count of concurrent workers currently running.
func (p *pool) Len() int {
	return len(p.workers)
}

// Close closes all channels
func (p *pool) Close() {
	p.cancel()
	close(p.tasks)
	p.wgTasks.Wait()
	close(p.err)
	p.wgErr.Wait()
}

func (p *pool) start() *pool {
	go func() {
		for {
			select {
			case <-p.ctx.Done():
				for task := range p.tasks {
					if task == nil {
						return
					}

					p.runTask(task)
				}

				return

			case task, ok := <-p.tasks:
				if !ok {
					return
				}

				if p.ctx.Err() != nil {
					return
				}

				p.runTask(task)
			}
		}
	}()

	p.wgErr.Add(1)
	go func() {
		defer p.wgErr.Done()

		for {
			select {
			case <-p.ctx.Done():
				return

			case err, ok := <-p.err:
				if !ok {
					return
				}

				if err != nil {
					p.lastErr = err
				}
			}
		}
	}()

	return p
}

func (p *pool) runTask(task Task) {
	p.workers <- struct{}{}

	go func() {
		defer p.wgTasks.Done()
		p.err <- task(p.ctx)
		<-p.workers
	}()
}

// Wait blocks until the task queue is drained
func (p *pool) Wait() error {
	close(p.tasks)
	p.wgTasks.Wait()
	close(p.err)
	p.wgErr.Wait()
	return p.lastErr
}
