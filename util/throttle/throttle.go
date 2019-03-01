package throttle

import (
	"sync"
	"time"
)

type Throttle struct {
	mu       sync.Mutex
	attempts map[string]state
	cleaned  time.Time
}

type state struct {
	last     time.Time
	failures int
}

func (tr *Throttle) Throttle(val string) {
	const delay = 3 * time.Second
	const window = 60 * time.Second
	const buffer = 10

	now := timeNow()

	tr.mu.Lock()
	if now.Sub(tr.cleaned) > window {
		// Cleanup old keys.
		for key, tm := range tr.attempts {
			if now.Sub(tm.last) > delay {
				delete(tr.attempts, key)
			}
		}
		tr.cleaned = now
	}
	state := tr.attempts[val]
	tr.mu.Unlock()

	if state.failures >= buffer && now.Sub(state.last) < delay {
		timeSleep(delay)
	}
}

func (tr *Throttle) Add(val string) {
	tr.mu.Lock()
	if tr.attempts == nil {
		tr.attempts = make(map[string]state)
	}
	state := tr.attempts[val]
	state.last = timeNow()
	state.failures++
	tr.attempts[val] = state
	tr.mu.Unlock()
}

var timeSleep = time.Sleep
var timeNow = time.Now
