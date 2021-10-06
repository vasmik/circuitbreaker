package circuitbreaker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func srvOk() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("OK"))
	}))
}

func srvSlow(t time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		time.Sleep(t)
	}))
}

func srvErr(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(status)
		rw.Write([]byte(http.StatusText(status)))
	}))
}

func immediateCB() *CircuitBreaker {
	return &CircuitBreaker{
		Trigger:   &TriggerConseqFailure{Threshold: 1},
		Recoverer: &RecovererConseqSuccess{Threshold: 1},
	}
}

type triggerMock struct {
	reset bool
	test  bool
}

func (t *triggerMock) Reset()         { t.reset = true }
func (t *triggerMock) Test(bool) bool { return t.test }

type recovererMock struct {
	reset bool
	test  bool
}

func (t *recovererMock) Reset()         { t.reset = true }
func (t *recovererMock) Test(bool) bool { return t.test }

func TestCircuitBreaker_Close(t *testing.T) {
	cb := immediateCB()
	cb.setState(StateOpen)
	assert.Equal(t, StateOpen, cb.state)
	cb.Close()
	assert.Equal(t, StateClosed, cb.state)
}

func TestCircuitBreaker_getState(t *testing.T) {
	type fields struct {
		state      State
		timeoutExp time.Time
	}
	tests := []struct {
		name   string
		fields fields
		want   State
	}{
		{name: "open", fields: fields{state: StateOpen}, want: StateOpen},
		{name: "closed", fields: fields{state: StateClosed}, want: StateClosed},
		{name: "half-open", fields: fields{state: StateHalfOpen}, want: StateHalfOpen},
		{name: "open to half-open", fields: fields{state: StateOpen, timeoutExp: time.Now().Add(-1 * time.Second)}, want: StateHalfOpen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &CircuitBreaker{
				state:      tt.fields.state,
				timeoutExp: tt.fields.timeoutExp,
			}
			assert.Equal(t, tt.want, cb.GetState())
		})
	}
}

func TestCircuitBreaker_setState(t *testing.T) {
	type want struct {
		timeout        func(time.Time) bool
		failures       int32
		successes      int32
		skipped        int32
		fnCalled       bool
		resetTrigger   bool
		resetRecoverer bool
	}
	type fields struct {
		state      State
		Timeout    time.Duration
		timeoutExp time.Time
		failures   int32
		successes  int32
		skipped    int32
		Jitter     time.Duration
	}
	tests := []struct {
		name string
		fields
		state State
		want  want
	}{
		{
			name:  "closed to open",
			state: StateOpen,
			fields: fields{
				state:  StateClosed,
				Jitter: 3 * time.Second,
			},
			want: want{
				timeout: func(t time.Time) bool {
					return time.Now().Before(t) && t.After(time.Now().Add(DefaultTimeout))
				},
				fnCalled:       true,
				resetTrigger:   false,
				resetRecoverer: false,
			},
		},
		{
			name:  "open to closed",
			state: StateClosed,
			fields: fields{
				state:      StateOpen,
				failures:   3,
				timeoutExp: time.Now().Add(time.Hour),
			},
			want: want{
				failures:       0,
				timeout:        func(t time.Time) bool { return t.IsZero() },
				fnCalled:       true,
				resetTrigger:   true,
				resetRecoverer: false,
			},
		},
		{
			name:  "closed to closed no changes",
			state: StateClosed,
			fields: fields{
				state:      StateClosed,
				failures:   3,
				timeoutExp: time.Now().Add(time.Hour),
			},
			want: want{
				failures:       3,
				timeout:        func(t time.Time) bool { return !t.IsZero() },
				fnCalled:       false,
				resetTrigger:   false,
				resetRecoverer: false,
			},
		},
		{
			name:  "open to half-open",
			state: StateHalfOpen,
			fields: fields{
				state:      StateOpen,
				successes:  3,
				skipped:    5,
				timeoutExp: time.Now().Add(time.Hour),
			},
			want: want{
				successes:      0,
				skipped:        0,
				timeout:        func(t time.Time) bool { return t.IsZero() },
				fnCalled:       true,
				resetTrigger:   false,
				resetRecoverer: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			tg := &triggerMock{reset: false}
			rc := &recovererMock{reset: false}
			cb := &CircuitBreaker{
				Trigger:    tg,
				Recoverer:  rc,
				state:      tt.fields.state,
				timeoutExp: tt.fields.timeoutExp,
				Jitter:     tt.fields.Jitter,
				Timeout:    tt.fields.Timeout,
				OnStateChange: func(s State) {
					called = true
				},
			}
			cb.setState(tt.state)
			if tt.want.timeout != nil {
				assert.True(t, tt.want.timeout(cb.timeoutExp))
			}
			assert.Equal(t, tt.state, cb.state)
			assert.Equal(t, tt.want.fnCalled, called)
			assert.Equal(t, tt.want.resetTrigger, tg.reset)
			assert.Equal(t, tt.want.resetRecoverer, rc.reset)
		})
	}
}

func TestCircuitBreaker_pass(t *testing.T) {
	tests := []struct {
		name        string
		rate        int32
		skipped     int32
		nextSkipped int32
		want        bool
	}{
		{name: "skip", rate: 3, skipped: 2, nextSkipped: 3, want: false},
		{name: "pass", rate: 3, skipped: 3, nextSkipped: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &CircuitBreaker{
				HalfOpenPassRate: tt.rate,
				skipped:          tt.skipped,
			}
			assert.Equal(t, tt.want, cs.pass())
			assert.Equal(t, tt.nextSkipped, cs.skipped)
		})
	}
}

func TestCircuitBreaker_RoundTrip(t *testing.T) {
	t.Run("Open state", func(t *testing.T) {
		srv := srvOk()
		defer srv.Close()

		cb := &CircuitBreaker{state: StateOpen}
		req := httptest.NewRequest("GET", srv.URL, nil)
		res, err := cb.RoundTrip(req)
		require.Nil(t, res)
		assert.Equal(t, ErrOpenState, err)
	})

	t.Run("Closed state", func(t *testing.T) {
		srv := srvOk()
		defer srv.Close()
		srv500 := srvErr(500)
		defer srv500.Close()
		srvTO := srvSlow(30 * time.Millisecond)
		defer srvTO.Close()

		cb := &CircuitBreaker{
			Trigger: &TriggerConseqFailure{Threshold: 1},
		}

		t.Run("normal call", func(t *testing.T) {
			req := httptest.NewRequest("GET", srv.URL, nil)
			_, err := cb.RoundTrip(req)
			require.Nil(t, err)
			assert.Equal(t, StateClosed, cb.state)
		})

		t.Run("context cancel ok", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(2 * time.Millisecond)
				cancel()
			}()
			req := httptest.NewRequest("GET", srvTO.URL, nil)
			req = req.WithContext(ctx)
			_, err := cb.RoundTrip(req)
			assert.Equal(t, context.Canceled, err)
			assert.Equal(t, StateClosed, cb.state)
		})

		t.Run("turn to open", func(t *testing.T) {
			req := httptest.NewRequest("GET", srv500.URL, nil)
			_, err := cb.RoundTrip(req)
			require.Nil(t, err)
			assert.Equal(t, StateOpen, cb.state)
		})
	})

	t.Run("Half-open state", func(t *testing.T) {
		srv200 := srvOk()
		defer srv200.Close()
		srv500 := srvErr(500)
		defer srv500.Close()

		type want struct {
			err   error
			state State
		}

		tests := []struct {
			name    string
			rate    int32
			skipped int32
			url     string
			rec     Recoverer
			want    want
		}{
			{
				name:    "skip",
				rate:    1,
				skipped: 0,
				url:     srv200.URL,
				rec:     &recovererMock{test: false},
				want:    want{err: ErrOpenState, state: StateHalfOpen},
			},
			{
				name:    "pass but open",
				rate:    1,
				skipped: 1,
				url:     srv500.URL,
				rec:     &recovererMock{test: false},
				want:    want{err: nil, state: StateOpen},
			},
			{
				name:    "pass and close",
				rate:    1,
				skipped: 1,
				url:     srv200.URL,
				rec:     &recovererMock{test: true},
				want:    want{err: nil, state: StateClosed},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cb := &CircuitBreaker{
					HalfOpenPassRate: tt.rate,
					Recoverer:        tt.rec,
					state:            StateHalfOpen,
					skipped:          tt.skipped,
				}
				req := httptest.NewRequest("GET", tt.url, nil)
				_, err := cb.RoundTrip(req)
				assert.Equal(t, tt.want.err, err)
				assert.Equal(t, tt.want.state, cb.state)
			})
		}
	})
}

func TestCircuitBreaker_Get(t *testing.T) {
	srv := srvOk()
	defer srv.Close()
	cb := immediateCB()
	resp, err := cb.Get(srv.URL)
	require.Nil(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, StateClosed, cb.state)
}

func TestCircuitBreaker_GetWithContext(t *testing.T) {
	t.Run("with background context", func(t *testing.T) {
		srv := srvOk()
		defer srv.Close()
		cb := immediateCB()
		_, err := cb.GetWithContext(context.Background(), srv.URL)
		require.Nil(t, err)
		assert.Equal(t, StateClosed, cb.state)
	})

	t.Run("with cancel context", func(t *testing.T) {
		srv := srvSlow(30 * time.Millisecond)
		defer srv.Close()
		cb := immediateCB()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Millisecond)
			cancel()
		}()
		_, err := cb.GetWithContext(ctx, srv.URL)
		assert.Equal(t, context.Canceled, err)
		assert.Equal(t, StateClosed, cb.state)
	})

	t.Run("with timeout context", func(t *testing.T) {
		srv := srvSlow(30 * time.Millisecond)
		defer srv.Close()
		cb := immediateCB()
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err := cb.GetWithContext(ctx, srv.URL)
		assert.Equal(t, context.DeadlineExceeded, err)
		assert.Equal(t, StateOpen, cb.state)
	})
}

func TestCircuitBreaker_Post(t *testing.T) {
	srv := srvOk()
	defer srv.Close()
	cb := immediateCB()
	resp, err := cb.Post(srv.URL, "application/json", nil)
	require.Nil(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, StateClosed, cb.state)
}

func TestCircuitBreaker_PostWithContext(t *testing.T) {
	t.Run("with background context", func(t *testing.T) {
		srv := srvOk()
		defer srv.Close()
		cb := immediateCB()
		_, err := cb.PostWithContext(context.Background(), srv.URL, "application/json", nil)
		require.Nil(t, err)
		assert.Equal(t, StateClosed, cb.state)
	})

	t.Run("with cancel context", func(t *testing.T) {
		srv := srvSlow(30 * time.Millisecond)
		defer srv.Close()
		cb := immediateCB()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Millisecond)
			cancel()
		}()
		_, err := cb.PostWithContext(ctx, srv.URL, "application/json", nil)
		assert.Equal(t, context.Canceled, err)
		assert.Equal(t, StateClosed, cb.state)
	})

	t.Run("with timeout context", func(t *testing.T) {
		srv := srvSlow(30 * time.Millisecond)
		defer srv.Close()
		cb := immediateCB()
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_, err := cb.PostWithContext(ctx, srv.URL, "application/json", nil)
		assert.Equal(t, context.DeadlineExceeded, err)
		assert.Equal(t, StateOpen, cb.state)
	})
}

func TestState_String(t *testing.T) {
	tests := []struct {
		name string
		s    State
		want string
	}{
		{name: "closed", s: StateClosed, want: "CLOSED"},
		{name: "open", s: StateOpen, want: "OPEN"},
		{name: "half-open", s: StateHalfOpen, want: "HALF-OPEN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, fmt.Sprintf("%s", tt.s))
		})
	}
}
