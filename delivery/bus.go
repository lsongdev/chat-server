package delivery

import (
	"context"
	"sync"
)

type BusMessage struct {
	Message Message
}

type Bus interface {
	Publish(context.Context, BusMessage) error
	Subscribe(func(BusMessage)) (func(), error)
	Close() error
}

type MemoryBus struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]func(BusMessage)
	closed      bool
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{subscribers: make(map[uint64]func(BusMessage))}
}

func (b *MemoryBus) Publish(_ context.Context, message BusMessage) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrNotFound
	}
	for _, subscriber := range b.subscribers {
		subscriber(message)
	}
	return nil
}

func (b *MemoryBus) Subscribe(subscriber func(BusMessage)) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrNotFound
	}
	b.nextID++
	id := b.nextID
	b.subscribers[id] = subscriber
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, id)
			b.mu.Unlock()
		})
	}, nil
}

func (b *MemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	clear(b.subscribers)
	return nil
}
