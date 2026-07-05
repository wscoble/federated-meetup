// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// metrics is the global metrics collector for the web server.
var metrics = &metricsCollector{
	requests:   make(map[string]*atomic.Int64),
	durations:  make(map[string]*rollingDuration),
}

type metricsCollector struct {
	mu         sync.RWMutex
	requests   map[string]*atomic.Int64
	durations  map[string]*rollingDuration
}

// rollingDuration tracks a simple sum of durations for average calculation.
type rollingDuration struct {
	count atomic.Int64
	sumNs atomic.Int64
}

// recordRequest increments the request counter and adds duration for the given route.
func (m *metricsCollector) recordRequest(route string, duration time.Duration) {
	key := strings.ToLower(route)

	m.mu.RLock()
	counter, ok := m.requests[key]
	dur, dok := m.durations[key]
	m.mu.RUnlock()

	if !ok {
		m.mu.Lock()
		counter, ok = m.requests[key]
		if !ok {
			counter = &atomic.Int64{}
			m.requests[key] = counter
		}
		dur, dok = m.durations[key]
		if !dok {
			dur = &rollingDuration{}
			m.durations[key] = dur
		}
		m.mu.Unlock()
	}

	counter.Add(1)
	if dur != nil {
		dur.count.Add(1)
		dur.sumNs.Add(int64(duration))
	}
}

// metricsMiddleware wraps an http.Handler and records request count and duration
// per route (method + path pattern).
func (s *Server) metricsMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(rw, r)
		duration := time.Since(start)
		route := fmt.Sprintf("%s_%s", r.Method, normalizePath(r.URL.Path))
		metrics.recordRequest(route, duration)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// normalizePath converts a URL path to a stable route label.
// e.g. /events/abc/def -> /events/{group_key}/{event_id}
func normalizePath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) <= 1 {
		if path == "/" || path == "" {
			return "root"
		}
		return strings.ReplaceAll(path, "/", "_")
	}

	// Known route patterns
	switch segments[0] {
	case "groups":
		if len(segments) >= 2 {
			if len(segments) >= 3 && segments[2] == "calendar.ics" {
				return "groups_name_calendar_ics"
			}
			if len(segments) >= 3 && segments[2] == "feed.xml" {
				return "groups_name_feed_xml"
			}
			if segments[1] == "new" {
				return "groups_new"
			}
			return "groups_name"
		}
		return "groups"
	case "events":
		if len(segments) >= 3 {
			if len(segments) >= 4 && segments[3] == "calendar.ics" {
				return "events_group_event_calendar_ics"
			}
			return "events_group_event"
		}
		return "events"
	case "dashboard":
		if len(segments) >= 2 {
			if len(segments) >= 3 && segments[2] == "attendees.csv" {
				return "dashboard_events_csv"
			}
			return "dashboard_" + segments[1]
		}
		return "dashboard"
	case "rsvp":
		if len(segments) >= 2 {
			return "rsvp_token"
		}
		return "rsvp"
	case "my-rsvps":
		return "my_rsvps"
	case "checkout":
		if len(segments) >= 2 {
			return "checkout_order"
		}
		return "checkout"
	case "healthz":
		return "healthz"
	case "identity":
		return "identity"
	case "api":
		return "api"
	case "llms.txt":
		return "llms_txt"
	default:
		return strings.ReplaceAll(path, "/", "_")
	}
}

// handleMetrics renders Prometheus text format metrics.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var sb strings.Builder

	// Total requests by route
	sb.WriteString("# HELP fedmeetup_http_requests_total Total HTTP requests by route\n")
	sb.WriteString("# TYPE fedmeetup_http_requests_total counter\n")

	metrics.mu.RLock()
	for route, counter := range metrics.requests {
		sb.WriteString(fmt.Sprintf("fedmeetup_http_requests_total{route=\"%s\"} %d\n", route, counter.Load()))
	}

	// Average duration by route
	sb.WriteString("\n# HELP fedmeetup_http_request_duration_seconds_avg Average request duration in seconds by route\n")
	sb.WriteString("# TYPE fedmeetup_http_request_duration_seconds_avg gauge\n")

	for route, dur := range metrics.durations {
		count := dur.count.Load()
		if count == 0 {
			continue
		}
		avgSec := float64(dur.sumNs.Load()) / float64(count) / 1e9
		sb.WriteString(fmt.Sprintf("fedmeetup_http_request_duration_seconds_avg{route=\"%s\"} %.6f\n", route, avgSec))
	}
	metrics.mu.RUnlock()

	// Process info
	sb.WriteString("\n# HELP fedmeetup_process_start_time_seconds Start time of the process since unix epoch in seconds\n")
	sb.WriteString("# TYPE fedmeetup_process_start_time_seconds gauge\n")
	sb.WriteString(fmt.Sprintf("fedmeetup_process_start_time_seconds %d\n", processStart.Unix()))

	w.Write([]byte(sb.String()))
}

var processStart = time.Now()