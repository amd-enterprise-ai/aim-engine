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

func assertContainsAll(t *testing.T, subject, body string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Errorf("%s is missing %q", subject, needle)
		}
	}
}

func assertContainsNone(t *testing.T, subject, body string, forbidden ...string) {
	t.Helper()
	for _, needle := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("%s must not contain %q", subject, needle)
		}
	}
}

// TestGatewayActivationInvariant pins the scale-from-zero activation invariant
// across every place it is independently expressed:
//
//   - controller metric naming (internal/v1alpha1/aimservice/scaledobject.go)
//   - the standalone and Helm-managed OTel collectors
//   - the Gateway-scoped Lua policies (bootstrap and standalone)
//   - the development EnvoyProxy source-side delta sink
//   - the kgateway collector scrape interval
//
// Envoy Gateway must calculate deltas at the source so collector restarts cannot
// replay a proxy's cumulative history. kgateway still uses a one-second
// Prometheus scrape and cumulative-to-delta conversion until it gains an
// equivalent source-side sink. A drift in either provider pipeline can break
// activation silently, so we fail the build instead.
func TestGatewayActivationInvariant(t *testing.T) {
	root := repoRoot(t)

	var values map[string]interface{}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, root, "config", "helm", "values.yaml")), &values); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}

	// The trigger is activation-only. Its target must remain unreachable during
	// a normal export interval so even a five-second Envoy delta cannot drive
	// 1->N scaling.
	if target := constants.DefaultGatewayActivationTargetValue; target != "1000000000" {
		t.Errorf("activation targetValue=%q is not the pinned neutralizing ceiling", target)
	}

	// Both provider pipelines deliver delta values to the scaler.
	if op := constants.DefaultGatewayActivationOperationOverTime; op != "avg" {
		t.Errorf("operationOverTime=%q breaks the gateway delta contract; it must be %q", op, "avg")
	}

	if got := helmString(t, values, "scaleFromZero", "gatewayProvider"); got != "none" {
		t.Errorf("gatewayProvider=%q; expected the default provider to be %q", got, "none")
	}
	if got := helmString(t, values, "scaleFromZero", "gatewayMetricsCollector", "management"); got != "helm" {
		t.Errorf("gatewayMetricsCollector.management=%q; expected default ownership %q", got, "helm")
	}
	if base := readRepoFile(t, root, "config", "default", "kustomization.yaml"); strings.Contains(base, "../prereqs/scale-from-zero") {
		t.Error("config/default must not bundle a gateway-specific scale-from-zero collector")
	}

	// scrapeInterval is now kgateway-only; its cumulative-to-delta pipeline
	// retains the one-second time base.
	if got := helmString(t, values, "scaleFromZero", "gatewayMetricsCollector", "scrapeInterval"); got != "1s" {
		t.Errorf("kgateway scrapeInterval=%q; expected %q", got, "1s")
	}

	envoyCollector := readRepoFile(
		t, root, "config", "prereqs", "scale-from-zero", "envoy-gateway-metrics-collector.yaml",
	)
	assertContainsAll(t, "standalone Envoy collector", envoyCollector,
		"automountServiceAccountToken: false",
		"otlp:",
		"endpoint: 0.0.0.0:4317",
		`^http\.lua\.aim_activation_requests\..*`,
		"transform/metric-name",
		"replace_pattern(metric.name",
		envoyGatewayActivationMetricPrefix,
		"receivers: [otlp]",
	)
	assertContainsNone(t, "standalone Envoy collector", envoyCollector,
		"kind: ClusterRole",
		"kubernetes_sd_configs:",
		"cumulativetodelta:",
		"receivers: [prometheus]",
	)

	helmCollector := readRepoFile(t, root, "config", "helm", "templates", "scale-from-zero-collector.yaml")
	assertContainsAll(t, "Helm collector provider selection", helmCollector,
		`(ne $scope "none")`,
		"gatewayProvider=kgateway",
		"gatewayProvider=custom",
		"management=external",
		"automountServiceAccountToken: false",
		"receivers: [otlp]",
		"transform/metric-name",
		envoyGatewayActivationMetricPrefix,
		"envoy_cluster_external_upstream_rq_completed",
		"transform/cluster_labels",
		"cumulativetodelta",
		"replicas must be 1 for kgateway",
	)
	kgatewayCollector := readRepoFile(
		t, root, "config", "prereqs", "scale-from-zero", "kgateway-metrics-collector.yaml",
	)
	assertContainsAll(t, "standalone kgateway collector", kgatewayCollector,
		"envoy_cluster_external_upstream_rq_completed",
		"cumulativetodelta",
		"initial_value: keep",
	)

	// The standalone kgateway collector carries a literal scrape interval (no
	// Helm templating); it must match the time base above.
	if !strings.Contains(kgatewayCollector, "scrape_interval: 1s") {
		t.Error("standalone kgateway collector does not scrape at 1s")
	}

	policies := map[string]string{
		"bootstrap":  readRepoFile(t, root, "hack", "dependencies", "gateway.yaml"),
		"standalone": readRepoFile(t, root, "config", "prereqs", "scale-from-zero", "envoy-gateway-route-metrics.yaml"),
	}
	for name, body := range policies {
		assertContainsAll(t, name+" gateway policy", body,
			"kind: EnvoyExtensionPolicy",
			"request_handle:streamInfo():routeName()",
			"request_handle:stats():counter(metric_name):inc()",
			`string.format("_x%02x", string.byte(character))`,
			"aim_activation_requests.n",
		)
	}
	if !strings.Contains(policies["bootstrap"], "luaValidation: InsecureSyntax") {
		t.Error("bootstrap EnvoyProxy must permit Envoy 1.38's Lua stats API")
	}
	assertContainsAll(t, "bootstrap EnvoyProxy delta sink", policies["bootstrap"],
		"type: OpenTelemetry",
		"host: envoy-gateway-metrics-collector.keda.svc.cluster.local",
		"port: 4317",
		"reportCountersAsDeltas: true",
	)

	helmE2E := readRepoFile(t, root, ".github", "workflows", "test-e2e.yml")
	assertContainsAll(t, "Helm E2E EnvoyProxy sink setup", helmE2E,
		"Point Envoy Gateway at the Helm-managed OTLP collector",
		"aim-engine-envoy-gateway-metrics-collector.aim-system.svc.cluster.local",
		"hack/configure-envoy-collector-sink.sh",
		"@.port==4317",
	)
	releaseE2E := readRepoFile(t, root, ".github", "workflows", "compile-release.yaml")
	assertContainsAll(t, "release E2E EnvoyProxy sink setup", releaseE2E,
		"Point Envoy Gateway at the Helm-managed OTLP collector",
		"aim-engine-envoy-gateway-metrics-collector.aim-system.svc.cluster.local",
		"hack/configure-envoy-collector-sink.sh",
		"@.port==4317",
	)
	sinkHelper := readRepoFile(t, root, "hack", "configure-envoy-collector-sink.sh")
	assertContainsAll(t, "EnvoyProxy sink helper", sinkHelper,
		"COLLECTOR_HOST",
		"reportCountersAsDeltas: true",
		"PROXY_UID_BEFORE",
		"PROXY_UID_AFTER",
	)

	restartRegression := readRepoFile(
		t, root, "tests", "e2e", "aimservice", "common", "scale-from-zero", "chainsaw-test.yaml",
	)
	assertContainsAll(t, "collector-restart regression test", restartRegression,
		"Restart the Envoy activation collector without waking the workload",
		"kubectl rollout restart",
		"PROXY_UID_BEFORE",
		"PROXY_UID_AFTER",
		"for _ in $(seq 1 30)",
		"collector restart falsely woke",
	)
}
