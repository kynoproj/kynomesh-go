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
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/kynoproj/kynomesh-go/pkg/server/serverinfo"
)

const (
	envPodName = "POD_NAME"

	// brokerHTTPSocketPath and brokerGRPCSocketPath must stay in sync
	// with kmv1.BrokerHTTPSocketPath / kmv1.BrokerGRPCSocketPath in
	// kynoproj/kynomesh: the broker dials the agent's HTTP and gRPC
	// servers at these independent sockets in-pod.
	brokerHTTPSocketPath = "/var/run/kynomesh/broker-http.sock"
	brokerGRPCSocketPath = "/var/run/kynomesh/broker-grpc.sock"

	// defaultLocalHTTPAddr and defaultLocalGRPCAddr must stay in sync
	// with DefaultLocalAgentHTTPAddr / DefaultLocalAgentGRPCAddr in
	// kynoproj/kynomesh.
	defaultLocalHTTPAddr = "127.0.0.1:8088"
	defaultLocalGRPCAddr = "127.0.0.1:8089"
)

// serverInfoPath is a test seam; production uses serverinfo.DefaultFilePath.
var serverInfoPath = serverinfo.DefaultFilePath

// listenMode is a test seam wrapping os.Getenv(envPodName).
var listenMode = func() bool { return os.Getenv(envPodName) != "" }

type listenerConfig struct {
	network string
	address string
}

func (c listenerConfig) isUDS() bool { return c.network == "unix" }

// resolveListeners picks the HTTP and gRPC listener targets. An explicit
// override (WithHTTPAddress / WithGRPCAddress) wins for its protocol;
// otherwise in-pod uses the broker UDS sockets and local-dev uses the
// default TCP ports.
func resolveListeners(opts options) (httpCfg, grpcCfg listenerConfig) {
	httpCfg = resolveListener(opts.httpAddress, brokerHTTPSocketPath, defaultLocalHTTPAddr)
	grpcCfg = resolveListener(opts.grpcAddress, brokerGRPCSocketPath, defaultLocalGRPCAddr)
	return httpCfg, grpcCfg
}

func resolveListener(explicit, udsDefault, tcpDefault string) listenerConfig {
	if explicit != "" {
		network := "tcp"
		if filepath.IsAbs(explicit) {
			network = "unix"
		}
		return listenerConfig{network: network, address: explicit}
	}
	if listenMode() {
		return listenerConfig{network: "unix", address: udsDefault}
	}
	return listenerConfig{network: "tcp", address: tcpDefault}
}

func newListener(cfg listenerConfig) (net.Listener, error) {
	if cfg.isUDS() {
		if err := os.MkdirAll(filepath.Dir(cfg.address), 0o755); err != nil {
			return nil, fmt.Errorf("create socket dir: %w", err)
		}
		// A leftover socket from a prior crash would make Listen fail.
		if err := os.Remove(cfg.address); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale socket %q: %w", cfg.address, err)
		}
	}
	ln, err := net.Listen(cfg.network, cfg.address)
	if err != nil {
		return nil, fmt.Errorf("listen %s %q: %w", cfg.network, cfg.address, err)
	}
	if cfg.isUDS() {
		if err := os.Chmod(cfg.address, 0o660); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("chmod socket %q: %w", cfg.address, err)
		}
	}
	return ln, nil
}

// writeServerInfo publishes the agent's metadata so the colocated broker
// can read it at startup. httpCfg's protocol is recorded; HTTP and gRPC
// always resolve to the same protocol (both UDS or both TCP).
func writeServerInfo(httpCfg listenerConfig) error {
	info := serverinfo.Default()
	info.Protocol = serverinfo.UDS
	if !httpCfg.isUDS() {
		info.Protocol = serverinfo.TCP
	}
	if err := serverinfo.Write(serverInfoPath, info); err != nil {
		return fmt.Errorf("write server-info: %w", err)
	}
	return nil
}
