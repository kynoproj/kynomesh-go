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

// Package server provides the in-agent A2A server helper for Kynomesh.
//
// Start binds two independent listeners, one for HTTP (AgentCard,
// JSON-RPC, REST, /healthz) and one for gRPC (A2A gRPC transport,
// grpc.health.v1); locally each defaults to its own TCP port
// (127.0.0.1:8088 for HTTP, 127.0.0.1:8089 for gRPC). Start mounts the
// transports listed in card.SupportedInterfaces.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/grpc"
)

const defaultShutdownTimeout = 10 * time.Second

type Option func(*options)

type options struct {
	httpAddress     string
	grpcAddress     string
	shutdownTimeout time.Duration
	handlerOpts     []a2asrv.RequestHandlerOption
	health          *Health
}

// WithHTTPAddress overrides the HTTP listener address. An absolute path
// opens a Unix domain socket; anything else is treated as a TCP host:port.
func WithHTTPAddress(addr string) Option {
	return func(o *options) { o.httpAddress = addr }
}

// WithGRPCAddress overrides the gRPC listener address. An absolute path
// opens a Unix domain socket; anything else is treated as a TCP host:port.
func WithGRPCAddress(addr string) Option {
	return func(o *options) { o.grpcAddress = addr }
}

func WithShutdownTimeout(d time.Duration) Option {
	return func(o *options) { o.shutdownTimeout = d }
}

func WithRequestHandlerOptions(opts ...a2asrv.RequestHandlerOption) Option {
	return func(o *options) { o.handlerOpts = append(o.handlerOpts, opts...) }
}

// WithHealth installs a caller-owned Health handle so the agent can flip
// readiness (e.g., NOT_SERVING during graceful drain). If omitted, Start
// uses an internal Health that stays SERVING for the lifetime of the
// process. The gRPC health service and HTTP /healthz are always mounted.
func WithHealth(h *Health) Option {
	return func(o *options) { o.health = h }
}

func Start(ctx context.Context, executor a2asrv.AgentExecutor, card *a2a.AgentCard, opts ...Option) error {
	if executor == nil {
		return errors.New("kynomesh server: executor is required")
	}
	if card == nil {
		return errors.New("kynomesh server: agent card is required")
	}

	o := options{shutdownTimeout: defaultShutdownTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	if o.health == nil {
		o.health = NewHealth()
	}

	handler := a2asrv.NewHandler(executor, o.handlerOpts...)
	st := buildStack(handler, card, o.health)

	httpCfg, grpcCfg := resolveListeners(o)
	httpLn, err := newListener(httpCfg)
	if err != nil {
		return err
	}
	grpcLn, err := newListener(grpcCfg)
	if err != nil {
		_ = httpLn.Close()
		return err
	}

	// Advertise to the broker only when colocated in the same pod; in
	// local dev there is no broker reading this file.
	if httpCfg.isUDS() {
		if err := writeServerInfo(httpCfg); err != nil {
			_ = httpLn.Close()
			_ = grpcLn.Close()
			return err
		}
	}

	log := slog.With("agent", card.Name, "version", card.Version)
	log.Info("Kynomesh server starting",
		"httpNetwork", httpCfg.network,
		"httpAddress", httpCfg.address,
		"grpcNetwork", grpcCfg.network,
		"grpcAddress", grpcCfg.address,
		"transports", st.transports,
		"health", []string{"grpc", "http " + HealthPath},
	)

	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpSrv := &http.Server{
		Handler:           st.httpHandler,
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 2)
	go func() {
		err := httpSrv.Serve(httpLn)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	go func() {
		err := st.grpcServer.Serve(grpcLn)
		if errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		_ = httpSrv.Close()
		st.grpcServer.Stop()
		<-serveErr
		return err
	}

	// Flip readiness first so kynoprobe pulls this replica out of
	// rotation before the listeners close.
	log.Info("Kynomesh server shutting down")
	o.health.SetServing(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), o.shutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		st.grpcServer.Stop()
		<-serveErr
		<-serveErr
		return fmt.Errorf("shutdown: %w", err)
	}
	st.grpcServer.GracefulStop()
	httpErr := <-serveErr
	grpcErr := <-serveErr
	log.Info("Kynomesh server stopped")
	if httpErr != nil {
		return httpErr
	}
	return grpcErr
}
