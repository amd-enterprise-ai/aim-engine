// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
)

// cacheSyncTimeout bounds how long each controller waits for its watches to
// sync. A watch on a kind or version the API server does not serve never syncs,
// so its controller fails within this window and the manager stops with an error.
const cacheSyncTimeout = 10 * time.Second

// TestManagerStartsWithAllControllers starts the manager with every controller
// against the generated CRDs and fails if any controller cannot start.
func TestManagerStartsWithAllControllers(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a control plane; skipped in -short mode")
	}
	ctrl.SetLogger(zap.New())

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			moduleCRDDir(t, "github.com/kserve/kserve", "config/crd/full"),
			moduleCRDDir(t, "sigs.k8s.io/gateway-api", "config/crd/standard"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest control plane (make test sets KUBEBUILDER_ASSETS): %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stop envtest control plane: %v", err)
		}
	})

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:     scheme,
		Cache:      cacheOptions(),
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: config.Controller{CacheSyncTimeout: cacheSyncTimeout},
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	if err := setupControllers(mgr, aimv1alpha2.RuntimeProjectionModeDefault); err != nil {
		t.Fatalf("set up controllers: %v", err)
	}

	// Start returns nil once the context ends, or an error as soon as a
	// controller fails to start.
	ctx, cancel := context.WithTimeout(context.Background(), cacheSyncTimeout+5*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("manager failed to start: %v", err)
	}
}

// moduleCRDDir returns a CRD directory shipped in a Go module dependency, for
// the third-party kinds (KServe, Gateway API) the controllers watch.
func moduleCRDDir(t *testing.T, module, dir string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module).Output()
	if err != nil {
		t.Fatalf("locate module %s: %v", module, err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), dir)
}
