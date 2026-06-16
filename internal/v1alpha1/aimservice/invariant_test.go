// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
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

package aimservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
)

// repoRoot walks up from the test's working directory to the module root (the
// directory containing go.mod) so the guard can read config/ files regardless
// of where `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test working directory")
		}
		dir = parent
	}
}

func readRepoFile(t *testing.T, root string, rel ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{root}, rel...)...)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// helmString navigates a parsed values.yaml map and returns the string at the
// given key path, failing the test if any segment is missing or not a string.
func helmString(t *testing.T, values map[string]interface{}, path ...string) string {
	t.Helper()
	var cur interface{} = values
	for i, key := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("values path %q: %q is not a map", strings.Join(path, "."), strings.Join(path[:i], "."))
		}
		cur, ok = m[key]
		if !ok {
			t.Fatalf("values path %q: key %q not found", strings.Join(path, "."), key)
		}
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("values path %q: value %v (%T) is not a string", strings.Join(path, "."), cur, cur)
	}
	return s
}

// TestGatewayActivationInvariant pins the scale-from-zero activation invariant
// across every place it is independently expressed, so the Helm path and the
// kustomize/build-installer path cannot silently disagree:
//
//   - controller compiled-in constant (internal/constants)
//   - the OTel collector      (config/prereqs/.../kgateway-metrics-collector.yaml
//     kustomize prereq + config/helm/templates/scale-from-zero-collector.yaml)
//   - the collector scrape interval (config/helm/values.yaml + kustomize prereq)
//
// The dangerous coupling is operationOverTime=avg <-> the collector's
// cumulativetodelta processor at a 1s scrape: avg of per-scrape deltas == req/s
// and is reset-safe, whereas `rate` over raw cumulatives goes negative on Envoy
// counter resets. operationOverTime (and targetValue) are compiled-in controller
// constants -- deliberately not Helm/env knobs -- because `avg` is the only
// correct value; this test is the guard that keeps the constant aligned with the
// collector pipeline. A drift in any one of these breaks activation silently, so
// we fail the build instead.
func TestGatewayActivationInvariant(t *testing.T) {
	root := repoRoot(t)

	var values map[string]interface{}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, root, "config", "helm", "values.yaml")), &values); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}

	// The compiled-in aggregation is the single source of truth and must be
	// `avg` to stay reset-safe against the collector's cumulativetodelta output.
	if op := constants.DefaultGatewayActivationOperationOverTime; op != "avg" {
		t.Errorf("operationOverTime=%q breaks the cumulativetodelta coupling; it must be %q", op, "avg")
	}

	// The scrape interval is the time base that makes avg-of-deltas == req/s.
	if got := helmString(t, values, "scaleFromZero", "gatewayMetricsCollector", "scrapeInterval"); got != "1s" {
		t.Errorf("scrapeInterval=%q breaks the avg-of-deltas-at-1s assumption; expected %q", got, "1s")
	}

	collectors := map[string]string{
		"kustomize prereq": readRepoFile(t, root, "config", "prereqs", "scale-from-zero", "kgateway-metrics-collector.yaml"),
		"helm template":    readRepoFile(t, root, "config", "helm", "templates", "scale-from-zero-collector.yaml"),
	}
	for name, body := range collectors {
		if !strings.Contains(body, "cumulativetodelta") {
			t.Errorf("%s collector is missing the cumulativetodelta processor that operationOverTime=avg depends on", name)
		}
		// The controller queries this exact metric name; the collector must
		// export it (see gatewayActivationMetricName in scaledobject.go).
		if !strings.Contains(body, gatewayActivationMetricName) {
			t.Errorf("%s collector does not reference the activation metric %q the controller queries", name, gatewayActivationMetricName)
		}
	}

	// The kustomize prereq carries a literal scrape interval (no Helm
	// templating); it must match the time base above.
	if prereq := collectors["kustomize prereq"]; !strings.Contains(prereq, "scrape_interval: 1s") {
		t.Error("kustomize prereq collector does not scrape at 1s; breaks the avg-of-deltas-at-1s assumption")
	}
}
