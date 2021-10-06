package circuitbreaker_test

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/vasmik/circuitbreaker"
)

// The following example shows how to use default CircuitBreaker for the regular Http Request
func ExampleCircuitBreaker_httpRequestDefault() {
	// Create circuit breaker instance
	cb := &circuitbreaker.CircuitBreaker{}

	// Use circuit breaker instead of regular http.Get
	res, err := cb.Get("http://someservice.com/api/endpoint")
	if err != nil {
		switch err {
		case circuitbreaker.ErrOpenState:
			// Falback code
			log.Println("This is a fallback result")
		default:
			// Error handling
			log.Printf("can't call remote service: %v", err)
		}
	}
	body, _ := ioutil.ReadAll(res.Body)
	fmt.Println(body)
}

// The following example shows how to use CircuitBreaker for the regular Http Request
func ExampleCircuitBreaker_httpRequest() {
	// Create circuit breaker instance
	cb := &circuitbreaker.CircuitBreaker{
		Trigger:          &circuitbreaker.TriggerConseqFailure{Threshold: 3},
		Recoverer:        &circuitbreaker.RecovererConseqSuccess{Threshold: 3},
		Timeout:          10 * time.Second,
		Jitter:           300 * time.Millisecond,
		HalfOpenPassRate: 5,
		OnStateChange: func(s circuitbreaker.State) {
			log.Printf("cirquitbreaker state changed %s", s)
		},
	}

	// Use circuit breaker instead of regular http.Get
	res, err := cb.Get("http://someservice.com/api/endpoint")
	if err != nil {
		switch err {
		case circuitbreaker.ErrOpenState:
			// Falback code
			log.Println("This is a fallback result")
		default:
			// Error handling
			log.Printf("can't call remote service: %v", err)
		}
	}
	body, _ := ioutil.ReadAll(res.Body)
	fmt.Println(body)
}

func ExampleCircuitBreaker_reverseProxy() {
	// Create reverse proxy with circuit breaker
	revProxy := &httputil.ReverseProxy{
		Transport: &circuitbreaker.CircuitBreaker{
			OnStateChange: func(s circuitbreaker.State) {
				log.Printf("cirquitbreaker state changed %s", s)
			},
		},
		ErrorHandler: func(rw http.ResponseWriter, r *http.Request, err error) {
			switch err {
			case circuitbreaker.ErrOpenState:
				// Fallback code
				rw.WriteHeader(http.StatusOK)
				rw.Write([]byte("Fallback response"))
			default:
				rw.WriteHeader(http.StatusBadGateway)
			}
			log.Printf("reverse proxy error: %v", err)
		},
	}

	// Use reverse proxy
	handler := func(rw http.ResponseWriter, r *http.Request) {
		revProxy.ServeHTTP(rw, r)
	}

	_ = handler
}
