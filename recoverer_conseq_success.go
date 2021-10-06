package circuitbreaker

import (
	"sync/atomic"
)

// RecovererConseqSuccess implements simple circuit breaker recoverer.
// It counts consecutive successfil results and triggers when counter reaches the threshold.
type RecovererConseqSuccess struct {
	// Threshold specifies how many consecutive successes should pass to trigger the state
	Threshold int32
	sucesses  int32
}

// Test returns True when number of consecutive successes reached the threshold
func (cs *RecovererConseqSuccess) Test(failure bool) bool {
	if failure {
		cs.Reset()
		return false
	}
	atomic.AddInt32(&cs.sucesses, 1)
	return cs.sucesses >= cs.Threshold
}

// Reset resets the success counter
func (cs *RecovererConseqSuccess) Reset() {
	atomic.StoreInt32(&cs.sucesses, 0)
}
