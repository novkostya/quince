package bus

import (
	"sync"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/wire"
)

func TestBusDeliversToSubscriber(t *testing.T) {
	b := New()
	s := b.Subscribe(4)
	defer b.Unsubscribe(s)
	b.PublishEvent("job.updated", nil)
	select {
	case env := <-s.C():
		if env.Type != "job.updated" {
			t.Fatalf("type = %q", env.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
}

func TestBusDropsSlowSubscriber(t *testing.T) {
	b := New()
	s := b.Subscribe(1)
	defer b.Unsubscribe(s)
	b.PublishEvent("a", nil) // fills the buffer
	b.PublishEvent("b", nil) // buffer full → dropped
	select {
	case <-s.Dropped():
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not dropped")
	}
}

// TestBusFanOutRaceStress is the story-5 gate: N publishers, M slow subscribers, under
// -race. Publishers must never block; nothing may panic or deadlock.
func TestBusFanOutRaceStress(t *testing.T) {
	b := New()
	const M = 8
	subs := make([]*Subscription, M)
	for i := range subs {
		subs[i] = b.Subscribe(1) // tiny buffers → guaranteed drops under load
	}

	stop := make(chan struct{})
	var drainWG sync.WaitGroup
	for _, s := range subs {
		drainWG.Add(1)
		go func(s *Subscription) {
			defer drainWG.Done()
			for {
				select {
				case <-stop:
					return
				case <-s.Dropped():
					return
				case <-s.C():
					time.Sleep(time.Millisecond) // deliberately slow consumer
				}
			}
		}(s)
	}

	const N = 8
	start := time.Now()
	var pubWG sync.WaitGroup
	for i := 0; i < N; i++ {
		pubWG.Add(1)
		go func() {
			defer pubWG.Done()
			for j := 0; j < 300; j++ {
				b.PublishEvent(wire.EventJobUpdated, nil)
			}
		}()
	}
	pubWG.Wait()
	// Publishers finishing at all proves they never blocked on a slow subscriber.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("publishers took %v — likely blocked on a slow subscriber", elapsed)
	}

	close(stop)
	drainWG.Wait()
	for _, s := range subs {
		b.Unsubscribe(s)
	}
}
