package throttle

import (
	"testing"
	"time"
)

func TestThrottle(t *testing.T) {
	now := time.Now()
	slept := time.Duration(0)
	timeSleep = func(d time.Duration) { slept = d }
	timeNow = func() time.Time { return now }
	defer func() {
		timeSleep = time.Sleep
		timeNow = time.Now
	}()

	tr := Throttle{}
	if tr.Throttle("foo") || slept != 0 {
		// interal map not yet initialized
		t.Errorf("empty throttle is throttling: %v", slept)
		slept = 0
	}

	tr.Add("foo")
	if tr.Throttle("foo") || slept != 0 {
		t.Errorf("throttling inside initial buffer: %v", slept)
		slept = 0
	}
	for i := 0; i < 10; i++ {
		tr.Add("foo")
	}
	if !tr.Throttle("foo") || slept != 3*time.Second {
		t.Errorf("want throttling, got: %v", slept)
	}
	slept = 0
	now = now.Add(4 * time.Second)
	if tr.Throttle("foo") || slept != 0 {
		t.Errorf("throttling after sufficient wait: %v", slept)
	}
	slept = 0

	now = now.Add(61 * time.Second)

	if tr.Throttle("foo") || slept != 0 {
		t.Errorf("throttling after cleaning window: %v", slept)
		slept = 0
	}
}
