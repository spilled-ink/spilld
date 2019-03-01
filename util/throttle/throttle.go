package throttle

import (
	"sync"
	"time"
)

type Throttle struct {
	mu      sync.Mutex
	keys    map[string]time.Time
	cleaned time.Time
}

func (t *Throttle) Throttle(val string) {
	t.mu.Lock()

	const delay = 3 * time.Second

	if time.Since(t.cleaned) > 60*time.Second {
		// Cleanup old keys.
		for key, tm := range t.keys {
			if time.Since(tm) > delay {
				delete(t.keys, key)
			}
		}
	}
	d := time.Since(t.keys[val])
	t.mu.Unlock()

	if d < delay {
		sleepFn(delay)
	}
}

func (t *Throttle) Add(val string) {
	t.mu.Lock()
	if t.keys == nil {
		t.keys = make(map[string]time.Time)
	}
	t.keys[val] = time.Now()
	t.mu.Unlock()
}

var sleepFn = time.Sleep
