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
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/grpc"
)

// Paths mirror pkg/broker.JSONRPCEndpoint / RESTEndpoint in the parent repo.
const (
	jsonrpcPath = "/a2a/rpc"
	restPath    = "/a2a/api"

	grpcContentType = "application/grpc"
)

type stack struct {
	httpHandler http.Handler
	grpcServer  *grpc.Server
}

// buildStack assembles the listener-facing handlers. The gRPC server is
// always created so the standard grpc.health.v1 service can answer
// kynoprobe regardless of which A2A transports the card advertises;
// the A2A gRPC handler is mounted only when the card lists gRPC.
func buildStack(handler a2asrv.RequestHandler, card *a2a.AgentCard, h *Health) *stack {
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle(HealthPath, h.httpHandler())

	grpcSrv := grpc.NewServer()
	h.attach(grpcSrv)
	for _, iface := range card.SupportedInterfaces {
		switch iface.ProtocolBinding {
		case a2a.TransportProtocolJSONRPC:
			mux.Handle(jsonrpcPath, a2asrv.NewJSONRPCHandler(handler))
		case a2a.TransportProtocolHTTPJSON:
			mux.Handle(restPath+"/", http.StripPrefix(restPath, a2asrv.NewRESTHandler(handler)))
		case a2a.TransportProtocolGRPC:
			a2agrpc.NewHandler(handler).RegisterWith(grpcSrv)
		}
	}
	return &stack{httpHandler: mux, grpcServer: grpcSrv}
}

func (s *stack) dispatcher() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isGRPCRequest(r) {
			s.grpcServer.ServeHTTP(w, r)
			return
		}
		s.httpHandler.ServeHTTP(w, r)
	})
}

func isGRPCRequest(r *http.Request) bool {
	if r.ProtoMajor != 2 {
		return false
	}
	return strings.HasPrefix(r.Header.Get("Content-Type"), grpcContentType)
}
