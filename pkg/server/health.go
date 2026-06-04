/*
Copyright 2026 The Kynoproj Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"net/http"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthPath is the HTTP endpoint kynoprobe hits when invoked with
// --mode=http. The gRPC default (kynoprobe --mode=grpc, empty service)
// is satisfied by the standard grpc.health.v1 service registered on
// the same listener.
const HealthPath = "/healthz"

// Health reports the agent's readiness to serve. The zero value reports
// SERVING; flip it with SetServing(false) during graceful shutdown or
// backpressure. A *Health may be shared across goroutines.
//
// Pass to Start via WithHealth; if omitted, Start uses an internal
// Health that stays SERVING for the lifetime of the process.
type Health struct {
	serving atomic.Int32

	mu   sync.Mutex
	grpc *health.Server // wired by attach when Start builds the gRPC server
}

// NewHealth returns a Health initialised to SERVING.
func NewHealth() *Health {
	h := &Health{}
	h.serving.Store(int32(healthpb.HealthCheckResponse_SERVING))
	return h
}

// SetServing flips the reported status. true → SERVING, false → NOT_SERVING.
// Both the gRPC health service (service "") and the HTTP /healthz handler
// observe the change.
func (h *Health) SetServing(serving bool) {
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		status = healthpb.HealthCheckResponse_SERVING
	}
	h.serving.Store(int32(status))
	h.mu.Lock()
	gh := h.grpc
	h.mu.Unlock()
	if gh != nil {
		gh.SetServingStatus("", status)
	}
}

func (h *Health) status() healthpb.HealthCheckResponse_ServingStatus {
	return healthpb.HealthCheckResponse_ServingStatus(h.serving.Load())
}

// attach binds h to the gRPC server and registers the standard
// grpc.health.v1 service. Each Start owns its own gRPC server; sharing
// one Health across concurrent Starts is not supported.
func (h *Health) attach(srv *grpc.Server) {
	gh := health.NewServer()
	gh.SetServingStatus("", h.status())
	healthpb.RegisterHealthServer(srv, gh)
	h.mu.Lock()
	h.grpc = gh
	h.mu.Unlock()
}

// httpHandler reports the current status as plain text. 200 when
// SERVING, 503 otherwise.
func (h *Health) httpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.status() == healthpb.HealthCheckResponse_SERVING {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("SERVING\n"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("NOT_SERVING\n"))
	})
}
