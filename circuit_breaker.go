package circuitbreaker

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// State type to indicate CircuitBreaker state
type State int

// State values
var (
	StateClosed   State = 0
	StateOpen     State = 1
	StateHalfOpen State = 2
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "OPEN"
	case StateClosed:
		return "CLOSED"
	case StateHalfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

// ErrOpenState issued then circuit breaker is in the  open state
var ErrOpenState = errors.New("circuit breaker open")

// DefaultTimeout is the default value for the Open state time
var DefaultTimeout = 10 * time.Second

// DefaultTimeoutJitter is default value for the jitter
// to distribute the time for cliens get back to full consumption power
var DefaultTimeoutJitter = 300 * time.Millisecond

// DefaultHalfOpenPassRate specifies how many requests should be skipped before the passing one
// when circuit breaker is in Half-Open state.
//
// Default is 0, which means no requests are going to be skipped.
var DefaultHalfOpenPassRate int32 = 5

// FailureDetector defines the failure detector interface.
// Detector should decide if request result failed.
type FailureDetector interface {
	IsFailure(res *http.Response, err error) bool
}

// DefaultFailureDetector implements default failure detector behavior.
type DefaultFailureDetector struct{}

// IsFailure returns True in case of 5xx response status code or any error except context cancelation.
func (d *DefaultFailureDetector) IsFailure(res *http.Response, err error) bool {
	return err != nil && err != context.Canceled || res != nil && res.StatusCode >= 500
}

// Trigger describes the interface to trigger the opening of the circuit breaker.
//
// Circuit breaker in Closed state will test every request result with trigger and will switch to Open state
// if trigger Test method returns true
type Trigger interface {
	// Test receives the result of the request (failed or not)
	// and returns True if trigger decides that circuit breaker should open
	Test(failure bool) bool

	// Reset called when circuit breaker switched back to Closed state
	Reset()
}

// Recoverer describes the interface to recover the circuit breaker
//
// Circuir breaker in Half-Open state will test every request result with recoverer
// and will switch to Closed state it recoverer Test method returns true
type Recoverer interface {
	// Test receives the result of the request (failed or not)
	// and returns True if recoverer decides that circuit breaker should close
	Test(failure bool) bool

	// Reset called when circuit breaker switched to Half-Open state
	Reset()
}

// DefaultTrigger used if no trigger specified for circuit breaker instance
var DefaultTrigger = &TriggerConseqFailure{Threshold: 5}

// DefaultRecoverer used if no recoverer spacified for circuit breaker instance
var DefaultRecoverer = &RecovererConseqSuccess{Threshold: 5}

// CircuitBreaker is a wrapper on HTTP transport, which implements Circuit Breaker pattern
type CircuitBreaker struct {
	// Timeout specifies the time to keep circuit breaker in Open state
	// before switch to Half-Open
	Timeout time.Duration

	// Jitter specifies the time fluctuation to extend the timeout
	// jitter helps to distribute the multiple circuit breakers closing moments
	// and avoid the instant overload for target service
	Jitter time.Duration

	// HalfOpenPassRate specifies the ratio of requiests to pass in Half-Open state
	// to minimize the load when circuit breaker checks the target service for availablity
	// If specified, circuit breaker will skip N incoming requests
	// the next one will pass.
	// For example if HalfOpenPassRate=5, five requests will be skipped, 6th will pass.
	// If HalfOpenPassRate=0 (default) all requests will pass
	HalfOpenPassRate int32

	// OnStateChange calls on each status chsnge and passes the news state value
	OnStateChange func(State)

	// Trigger makes the decision when to open circuit breaker based on requests results .
	// If nil, DefaultTrigger is used
	Trigger Trigger

	// Recoverer makes decision when to close cirsiot breacker absed on requests results.
	// If nil, DefaultRecoverer is used
	Recoverer Recoverer

	// FailureDetector makes decision if request is filed or not.
	// If nil, DefaultFailureDetector is used
	FailureDetector FailureDetector

	// The transport used to perform proxy requests.
	// If nil, http.DefaultTransport is used
	Transport http.RoundTripper

	skipped    int32
	state      State
	mutex      sync.Mutex
	timeoutExp time.Time
}

func (cb *CircuitBreaker) String() string {
	return cb.state.String()
}

// Close allows to manually close the cirquit breaker
// failure counters will be resetted
func (cb *CircuitBreaker) Close() {
	cb.setState(StateClosed)
}

func (cb *CircuitBreaker) detector() FailureDetector {
	if cb.FailureDetector == nil {
		cb.FailureDetector = &DefaultFailureDetector{}
	}
	return cb.FailureDetector
}

func (cb *CircuitBreaker) tranport() http.RoundTripper {
	if cb.Transport == nil {
		cb.Transport = http.DefaultTransport
	}
	return cb.Transport
}

// RoundTrip executes a single HTTP transaction, returning
// a Response for the provided Request.
// It wraps the standard http tranport method with circuit breaker
func (cb *CircuitBreaker) RoundTrip(req *http.Request) (*http.Response, error) {
	switch cb.GetState() {
	case StateOpen:
		return nil, ErrOpenState
	case StateHalfOpen:
		if !cb.pass() {
			return nil, ErrOpenState
		}
		res, err := cb.tranport().RoundTrip(req)
		failure := cb.detector().IsFailure(res, err)
		if failure {
			cb.setState(StateOpen)
			return res, err
		}
		if cb.recoverer().Test(failure) {
			cb.setState(StateClosed)
		}
		return res, err
	default:
		res, err := cb.tranport().RoundTrip(req)
		failure := cb.detector().IsFailure(res, err)
		if cb.trigger().Test(failure) {
			cb.setState(StateOpen)
		}
		return res, err
	}
}

// GetState returns current state
func (cb *CircuitBreaker) GetState() State {
	if cb.state == StateOpen && !cb.timeoutExp.IsZero() && time.Now().After(cb.timeoutExp) {
		cb.setState(StateHalfOpen)
	}
	return cb.state
}

// Get wraps the http.Get with circuit breaker
func (cb *CircuitBreaker) Get(url string) (resp *http.Response, err error) {
	return cb.GetWithContext(context.Background(), url)
}

// GetWithContext wraps the http.Get with circuit breaker and context
func (cb *CircuitBreaker) GetWithContext(ctx context.Context, url string) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return cb.RoundTrip(req)
}

// Post wraps the http.Post with circuit breaker
func (cb *CircuitBreaker) Post(url, contentType string, body io.Reader) (resp *http.Response, err error) {
	return cb.PostWithContext(context.Background(), url, contentType, body)
}

// PostWithContext wraps the http.Get with circuit breaker and context
func (cb *CircuitBreaker) PostWithContext(ctx context.Context, url, contentType string, body io.Reader) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return cb.RoundTrip(req)
}

// pass checks if incoming request should be passed or rejected
// when circuit breaker is in the half-open state
func (cb *CircuitBreaker) pass() bool {
	atomic.AddInt32(&cb.skipped, 1)
	if cb.skipped > cb.HalfOpenPassRate {
		atomic.StoreInt32(&cb.skipped, 0)
		return true
	}
	return false
}

func (cb *CircuitBreaker) trigger() Trigger {
	if cb.Trigger == nil {
		cb.Trigger = DefaultTrigger
	}
	return cb.Trigger
}

func (cb *CircuitBreaker) recoverer() Recoverer {
	if cb.Recoverer == nil {
		cb.Recoverer = DefaultRecoverer
	}
	return cb.Recoverer
}

func (cb *CircuitBreaker) setState(s State) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.state == s {
		return
	}
	cb.state = s

	switch cb.state {
	case StateOpen:
		if cb.Timeout == 0 {
			cb.Timeout = DefaultTimeout
		}
		rand.Seed(time.Now().UnixNano())
		var shift time.Duration
		if cb.Jitter != 0 {
			shift = time.Duration(rand.Int63n(int64(cb.Jitter)))
		}
		cb.timeoutExp = time.Now().Add(cb.Timeout + shift)
	case StateClosed:
		cb.trigger().Reset()
		cb.timeoutExp = time.Time{}
	case StateHalfOpen:
		cb.recoverer().Reset()
		cb.timeoutExp = time.Time{}
	}

	if cb.OnStateChange != nil {
		cb.OnStateChange(cb.state)
	}
}
