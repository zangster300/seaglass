package broadcaster

import "sync"

type Broadcaster struct {
	mu sync.Mutex
	ch chan struct{}
}

func New() *Broadcaster {
	return &Broadcaster{ch: make(chan struct{})}
}

func (b *Broadcaster) Listen() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

func (b *Broadcaster) Broadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}
