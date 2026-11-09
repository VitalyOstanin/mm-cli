// Package fakemm is a fake Mattermost HTTP server for tests.
package fakemm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Request is a recorded request.
type Request struct {
	Query  url.Values
	Body   []byte
	Header http.Header
}

// Server is an httptest server with routes keyed by method and exact path.
type Server struct {
	*httptest.Server
	t      testing.TB
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	counts map[string]int
	last   map[string]Request
}

// New starts a server that is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{t: t, routes: map[string]http.HandlerFunc{}, counts: map[string]int{}, last: map[string]Request{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func key(method, path string) string { return method + " " + path }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	k := key(r.Method, r.URL.Path)
	s.mu.Lock()
	s.counts[k]++
	s.last[k] = Request{Query: r.URL.Query(), Body: body, Header: r.Header.Clone()}
	h := s.routes[k]
	s.mu.Unlock()
	if h == nil {
		s.t.Logf("fakemm: unhandled %s", k)
		AppError(w, http.StatusNotFound, "Sorry, we could not find the page.")
		return
	}
	h(w, r)
}

// Handle registers a handler for a method and exact path.
func (s *Server) Handle(method, path string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[key(method, path)] = h
}

// JSON registers a fixed JSON response.
func (s *Server) JSON(method, path string, status int, v any) {
	s.Handle(method, path, func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, status, v)
	})
}

// Count is the number of requests to a method and path.
func (s *Server) Count(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key(method, path)]
}

// Total is the number of requests to all paths.
func (s *Server) Total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.counts {
		n += c
	}
	return n
}

// Last is the last request to a method and path.
func (s *Server) Last(method, path string) Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last[key(method, path)]
}

// WriteJSON writes v as JSON with the status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// AppError writes a Mattermost-style error body.
func AppError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]any{"id": "fake.error", "message": msg, "status_code": status})
}

// ID returns a valid 26-character id that starts with prefix.
func ID(prefix string) string {
	if len(prefix) >= 26 {
		return prefix[:26]
	}
	return prefix + strings.Repeat("0", 26-len(prefix))
}
