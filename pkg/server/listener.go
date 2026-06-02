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
	envPodName       = "POD_NAME"
	brokerSocketPath = "/var/run/kynomesh/broker.sock"
	defaultLocalAddr = "127.0.0.1:8088"
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

func resolveListener(opts options) listenerConfig {
	if opts.address != "" {
		network := "tcp"
		if filepath.IsAbs(opts.address) {
			network = "unix"
		}
		return listenerConfig{network: network, address: opts.address}
	}
	if listenMode() {
		return listenerConfig{network: "unix", address: brokerSocketPath}
	}
	return listenerConfig{network: "tcp", address: defaultLocalAddr}
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
// can read it at startup.
func writeServerInfo(cfg listenerConfig) error {
	info := serverinfo.Default()
	info.Protocol = serverinfo.UDS
	if !cfg.isUDS() {
		info.Protocol = serverinfo.TCP
	}
	if err := serverinfo.Write(serverInfoPath, info); err != nil {
		return fmt.Errorf("write server-info: %w", err)
	}
	return nil
}
