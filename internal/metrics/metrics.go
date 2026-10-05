package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	mu           sync.Mutex
	requests     map[string]uint64
	requestTimes map[string]uint64
	inFlight     atomic.Int64
}

func New() *Metrics {
	return &Metrics{
		requests:     make(map[string]uint64),
		requestTimes: make(map[string]uint64),
	}
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Add(1)
		defer m.inFlight.Add(-1)
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		key := r.Method + "\x00" + strconv.Itoa(status)
		m.mu.Lock()
		m.requests[key]++
		m.requestTimes[key] += uint64(time.Since(start).Microseconds())
		m.mu.Unlock()
	})
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP streamforge_http_requests_total Total HTTP requests by method and status.")
	fmt.Fprintln(w, "# TYPE streamforge_http_requests_total counter")
	fmt.Fprintln(w, "# HELP streamforge_http_request_duration_microseconds_total Total request duration in microseconds.")
	fmt.Fprintln(w, "# TYPE streamforge_http_request_duration_microseconds_total counter")
	for key, count := range m.requests {
		for method, status := range splitKey(key) {
			fmt.Fprintf(w, "streamforge_http_requests_total{method=%q,status=%q} %d\n", method, status, count)
			fmt.Fprintf(w, "streamforge_http_request_duration_microseconds_total{method=%q,status=%q} %d\n", method, status, m.requestTimes[key])
		}
	}
	fmt.Fprintln(w, "# HELP streamforge_http_requests_in_flight Current HTTP requests in flight.")
	fmt.Fprintln(w, "# TYPE streamforge_http_requests_in_flight gauge")
	fmt.Fprintf(w, "streamforge_http_requests_in_flight %d\n", m.inFlight.Load())
}

func splitKey(key string) map[string]string {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return map[string]string{key[:i]: key[i+1:]}
		}
	}
	return map[string]string{key: "200"}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}
