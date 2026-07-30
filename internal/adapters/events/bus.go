// Package events is an in-process EventBus. Subscribers run synchronously in the order
// registered (dev). A durable/webhook fan-out lives behind the same port later (§07/§03).
package events

import (
	"sync"

	"github.com/proseria-research/lineage/internal/domain"
)

type Bus struct {
	mu   sync.RWMutex
	subs []func(domain.Event)
}

func New() *Bus { return &Bus{} }

var _ domain.EventBus = (*Bus)(nil)

func (b *Bus) Subscribe(fn func(domain.Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, fn)
}

func (b *Bus) Publish(e domain.Event) {
	b.mu.RLock()
	subs := append([]func(domain.Event){}, b.subs...)
	b.mu.RUnlock()
	for _, fn := range subs {
		fn(e)
	}
}
