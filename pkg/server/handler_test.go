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
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// The gRPC server is always built so kynoprobe's grpc.health.v1 check
// succeeds regardless of which A2A transports the card advertises.
func TestBuildStackAlwaysMountsGRPCForHealth(t *testing.T) {
	cases := [][]a2a.TransportProtocol{
		{a2a.TransportProtocolJSONRPC},
		{a2a.TransportProtocolHTTPJSON},
		{a2a.TransportProtocolGRPC},
		{a2a.TransportProtocolJSONRPC, a2a.TransportProtocolHTTPJSON, a2a.TransportProtocolGRPC},
		nil,
	}
	for _, transports := range cases {
		st := buildStack(a2asrv.NewHandler(noopExecutor{}), newCard(transports...), NewHealth())
		if st.grpcServer == nil {
			t.Errorf("transports=%v: grpcServer is nil, expected always-on for health", transports)
		}
		if _, ok := st.grpcServer.GetServiceInfo()[healthpb.Health_ServiceDesc.ServiceName]; !ok {
			t.Errorf("transports=%v: gRPC health service not registered", transports)
		}
	}
}

// The HTTP listener only ever serves the HTTP mux; gRPC has its own
// listener and server entirely, so the AgentCard route must answer
// regardless of what content-type/proto version a request carries.
func TestHTTPHandlerServesAgentCardRegardlessOfHeaders(t *testing.T) {
	st := buildStack(
		a2asrv.NewHandler(noopExecutor{}),
		newCard(a2a.TransportProtocolJSONRPC, a2a.TransportProtocolGRPC),
		NewHealth(),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, a2asrv.WellKnownAgentCardPath, nil)
	st.httpHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (AgentCard)", rec.Code)
	}
}
