package worker

import (
	"context"
	"fmt"
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
	opts    PoolOptions
	wgTasks sync.WaitGroup
	wgErr   sync.WaitGroup
	tasks   chan Task
	err     chan error
	ctx     context.Context
	lastErr error
}

func NewPool(opts PoolOptions) *pool {
	return &pool{
		opts:  opts,
		tasks: make(chan Task, opts.Workers),
		err:   make(chan error, opts.Workers),
	}
}

type Task func(ctx context.Context) error

func (p *pool) Push(task Task) {
	if p.ctx.Err() != nil {
		return
	}

	p.tasks <- task
}

func (p *pool) Start(ctx context.Context) *pool {
	p.ctx = ctx
	for i := 0; i < p.opts.Workers; i++ {
		p.wgTasks.Add(1)
		go func() {
			defer p.wgTasks.Done()

			for {
				select {
				case <-ctx.Done():
					for task := range p.tasks {
						fmt.Printf("build task %#v\n", task)
						if task == nil {
							return
						}

						p.err <- task(ctx)
					}

					return

				case task, ok := <-p.tasks:
					if !ok {
						return
					}

					if ctx.Err() != nil {
						return
					}

					p.err <- task(ctx)
				}
			}
		}()
	}

	p.wgErr.Add(1)
	go func() {
		defer p.wgErr.Done()

		for {
			select {
			case <-ctx.Done():
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

func (p *pool) Wait() error {
	close(p.tasks)
	p.wgTasks.Wait()
	close(p.err)
	p.wgErr.Wait()
	return p.lastErr
}
