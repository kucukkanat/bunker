package core

import "sync"

// Bus is an in-process publish/subscribe hub used for live updates
// (database changes, presence, deploy notifications).
type Bus struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[string]map[chan []byte]struct{}{}} }

// Subscribe returns a channel receiving messages on topic and a cancel func.
func (b *Bus) Subscribe(topic string) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = map[chan []byte]struct{}{}
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[topic], ch)
			if len(b.subs[topic]) == 0 {
				delete(b.subs, topic)
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish delivers msg to all subscribers; slow subscribers drop messages.
func (b *Bus) Publish(topic string, msg []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[topic] {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Count returns the number of subscribers on topic.
func (b *Bus) Count(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[topic])
}
