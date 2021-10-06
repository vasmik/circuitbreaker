package circuitbreaker

import (
	"sync/atomic"
)

// TriggerConseqFailure implements simple circuit breaker trigger.
// It counts consecutive failures and triggers when counter reaches the threshold.
type TriggerConseqFailure struct {
	// Threshold specifies how many consecutive failures should pass to trigger the state
	Threshold int32
	failures  int32
}

// Test returns True when number of consecutive failures reached the threshold
func (cf *TriggerConseqFailure) Test(failure bool) bool {
	if failure {
		atomic.AddInt32(&cf.failures, 1)
		return cf.failures >= cf.Threshold
	}
	cf.Reset()
	return false
}

// Reset resets the failure counter
func (cf *TriggerConseqFailure) Reset() {
	atomic.StoreInt32(&cf.failures, 0)
}
