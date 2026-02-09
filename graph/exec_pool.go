package graph

import "runtime"

type execTask func()

type execWorker struct {
	ch chan execTask
}

type execWorkerPool struct {
	idle chan *execWorker
}

func newExecWorkerPool() *execWorkerPool {
	// Keep a bounded number of idle workers to avoid unbounded goroutine
	// retention after traffic spikes while still amortizing goroutine creation
	// costs under sustained load.
	maxIdle := runtime.GOMAXPROCS(0) * 64
	if maxIdle < 64 {
		maxIdle = 64
	} else if maxIdle > 4096 {
		maxIdle = 4096
	}
	return &execWorkerPool{idle: make(chan *execWorker, maxIdle)}
}

func (p *execWorkerPool) Go(fn execTask) {
	if fn == nil {
		return
	}
	w := p.get()
	w.ch <- fn
}

func (p *execWorkerPool) get() *execWorker {
	select {
	case w := <-p.idle:
		return w
	default:
		w := &execWorker{ch: make(chan execTask, 1)}
		go w.run(p)
		return w
	}
}

func (p *execWorkerPool) put(w *execWorker) bool {
	select {
	case p.idle <- w:
		return true
	default:
		close(w.ch)
		return false
	}
}

func (w *execWorker) run(p *execWorkerPool) {
	for fn := range w.ch {
		fn()
		if !p.put(w) {
			return
		}
	}
}

var executorWorkPool = newExecWorkerPool()

