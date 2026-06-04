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
// Start binds a UDS at /var/run/kynomesh/broker.sock in-pod (POD_NAME set)
// or 127.0.0.1:8088 locally, and mounts the transports listed in
// card.SupportedInterfaces.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const defaultShutdownTimeout = 10 * time.Second

type Option func(*options)

type options struct {
	address         string
	shutdownTimeout time.Duration
	handlerOpts     []a2asrv.RequestHandlerOption
	health          *Health
}

// WithAddress overrides the listener address. An absolute path opens a
// Unix domain socket; anything else is treated as a TCP host:port.
func WithAddress(addr string) Option {
	return func(o *options) { o.address = addr }
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

	cfg := resolveListener(o)
	ln, err := newListener(cfg)
	if err != nil {
		return err
	}

	// Advertise to the broker only when colocated in the same pod; in
	// local dev there is no broker reading this file.
	if cfg.isUDS() {
		if err := writeServerInfo(cfg); err != nil {
			_ = ln.Close()
			return err
		}
	}

	// Plaintext HTTP/2 lets gRPC share the listener; the broker
	// terminates external TLS, and the in-pod hop is localhost-only.
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{
		Handler:           st.dispatcher(),
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		st.grpcServer.Stop()
		return err
	}

	// Flip readiness first so kynoprobe pulls this replica out of
	// rotation before the listener closes.
	o.health.SetServing(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), o.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		st.grpcServer.Stop()
		return fmt.Errorf("shutdown: %w", err)
	}
	st.grpcServer.GracefulStop()
	return <-serveErr
}
