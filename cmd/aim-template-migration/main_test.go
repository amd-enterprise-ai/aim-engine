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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/migration/templatemigration"
)

func TestRunPlan_WritesDocumentsAndPrintsConciseSummary(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aimv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := aimv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	factoryCalls := 0
	factory := func(kubeconfig, contextName string) (client.Client, error) {
		factoryCalls++
		if kubeconfig == "/fixture/failing-kubeconfig" {
			return nil, errors.New("fixture client failure")
		}
		if kubeconfig != "/fixture/kubeconfig" || contextName != "fixture-context" {
			t.Fatalf("factory arguments = %q, %q", kubeconfig, contextName)
		}
		return kubernetesClient, nil
	}
	outputDirectory := filepath.Join(t.TempDir(), "plan")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run([]string{
		"plan",
		"--namespace", "team-b",
		"--namespace", "team-a",
		"--namespace", "team-b",
		"--output-dir", outputDirectory,
		"--runtime-projection-mode", "Both",
		"--kubeconfig", "/fixture/kubeconfig",
		"--kube-context", "fixture-context",
	}, &stdout, &stderr, factory)

	if exitCode != 0 {
		t.Fatalf("exit code = %d; stderr: %s", exitCode, stderr.String())
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if !strings.HasPrefix(
		stdout.String(),
		"planned 0 services: 0 automatic, 0 manual, 0 warnings\nbackup: ",
	) ||
		!strings.Contains(stdout.String(), "\nplan: ") ||
		strings.Contains(stdout.String(), "handoff") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	for _, name := range []string{"backup.yaml", "plan.json"} {
		if _, err := os.Stat(filepath.Join(outputDirectory, name)); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := os.ReadFile(filepath.Join(outputDirectory, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), `"selectedNamespaces": [
    "team-a",
    "team-b"
  ]`) {
		t.Fatalf("namespaces were not sorted and deduplicated:\n%s", plan)
	}
}

func TestWritePlanSummary_PrintsDedicatedEvidenceAndRunbook(t *testing.T) {
	paths := templatemigration.Paths{
		Backup: "plan/backup.yaml",
		Plan:   "plan/plan.json",
		DedicatedHandoffEvidence: []string{
			"plan/dedicated-handoff-team-a--service-a-one.json",
			"plan/dedicated-handoff-team-a--service-b-two.json",
		},
	}
	var output bytes.Buffer

	writePlanSummary(
		&output,
		templatemigration.Summary{Services: 2, Manual: 2},
		paths,
	)

	for _, path := range paths.DedicatedHandoffEvidence {
		if !strings.Contains(output.String(), "dedicated handoff evidence: "+path+"\n") {
			t.Fatalf("stdout omitted evidence path %q: %q", path, output.String())
		}
	}
	if strings.Count(output.String(), templatemigration.DedicatedHandoffRunbookReference) != 1 {
		t.Fatalf("stdout runbook reference = %q", output.String())
	}
}

func TestRunPlan_ValidatesRequiredArgumentsBeforeClientCreation(t *testing.T) {
	unexpectedClient := fake.NewClientBuilder().Build()
	factory := func(_, _ string) (client.Client, error) {
		t.Error("client factory called for invalid arguments")
		return unexpectedClient, errors.New("unexpected client creation")
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "namespace required",
			args: []string{"plan", "--output-dir", filepath.Join(t.TempDir(), "plan")},
			want: "at least one --namespace and --output-dir are required",
		},
		{
			name: "output directory required",
			args: []string{"plan", "--namespace", "team-a"},
			want: "at least one --namespace and --output-dir are required",
		},
		{
			name: "positional arguments rejected",
			args: []string{"plan", "--namespace", "team-a", "--output-dir", "plan", "extra"},
			want: "at least one --namespace and --output-dir are required",
		},
		{
			name: "runtime projection mode required",
			args: []string{"plan", "--namespace", "team-a", "--output-dir", "plan"},
			want: "--runtime-projection-mode is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if exitCode := run(test.args, &bytes.Buffer{}, &stderr, factory); exitCode != 2 {
				t.Fatalf("exit code = %d; stderr: %s", exitCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestRun_UsageAndApplyValidation(t *testing.T) {
	kubernetesClient := fake.NewClientBuilder().Build()
	factoryCalls := 0
	factory := func(kubeconfig, contextName string) (client.Client, error) {
		factoryCalls++
		if kubeconfig == "/fixture/failing-kubeconfig" {
			return nil, errors.New("fixture client failure")
		}
		if kubeconfig != "/fixture/kubeconfig" || contextName != "fixture-context" {
			t.Fatalf("factory arguments = %q, %q", kubeconfig, contextName)
		}
		return kubernetesClient, nil
	}
	tests := []struct {
		name     string
		args     []string
		exitCode int
		want     string
		factory  bool
	}{
		{name: "missing command", exitCode: 2, want: "usage: aim-template-migration"},
		{name: "unknown command", args: []string{"unknown"}, exitCode: 2, want: "usage: aim-template-migration"},
		{
			name:     "apply validates required plan",
			args:     []string{"apply"},
			exitCode: 2,
			want:     "--plan is required",
		},
		{
			name: "apply wires client and plan",
			args: []string{
				"apply",
				"--plan", filepath.Join(t.TempDir(), "missing-plan.json"),
				"--kubeconfig", "/fixture/kubeconfig",
				"--kube-context", "fixture-context",
			},
			exitCode: 1,
			want:     "read plan",
			factory:  true,
		},
		{
			name: "apply reports client setup failure",
			args: []string{
				"apply",
				"--plan", filepath.Join(t.TempDir(), "plan.json"),
				"--kubeconfig", "/fixture/failing-kubeconfig",
			},
			exitCode: 1,
			want:     "configure Kubernetes client: fixture client failure",
			factory:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeCalls := factoryCalls
			var stderr bytes.Buffer
			exitCode := run(test.args, &bytes.Buffer{}, &stderr, factory)
			if exitCode != test.exitCode {
				t.Fatalf("exit code = %d, want %d", exitCode, test.exitCode)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			gotFactoryCall := factoryCalls > beforeCalls
			if gotFactoryCall != test.factory {
				t.Fatalf("factory called = %t, want %t", gotFactoryCall, test.factory)
			}
		})
	}
}

func TestRunApply_PrintsTerminalBatchCountsAndResultLocation(t *testing.T) {
	document := templatemigration.PlanDocument{
		DocumentVersion:       templatemigration.DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{"team-a"},
		Units: []templatemigration.MigrationUnit{{
			ID:        "team-a/manual",
			Namespace: "team-a",
			Service: templatemigration.ResourceIdentity{
				Namespace: "team-a",
				Name:      "manual",
			},
			Outcome: templatemigration.OutcomeManual,
		}},
	}
	contents, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	factory := func(_, _ string) (client.Client, error) {
		return fake.NewClientBuilder().Build(), nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run(
		[]string{"apply", "--plan", planPath},
		&stdout,
		&stderr,
		factory,
	); exitCode != 0 {
		t.Fatalf("exit code = %d; stderr: %s", exitCode, stderr.String())
	}
	if !strings.Contains(
		stdout.String(),
		"apply dry-run summary: 0 succeeded, 0 failed, 1 manual, 0 skipped; 0 operations",
	) ||
		!strings.Contains(stdout.String(), "result: "+filepath.Join(filepath.Dir(planPath), "apply-result.json")) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunApply_PrintsFailedAndManualTerminalCounts(t *testing.T) {
	document := templatemigration.PlanDocument{
		DocumentVersion:       templatemigration.DocumentVersion,
		RuntimeProjectionMode: string(aimv1alpha2.RuntimeProjectionModeBoth),
		SelectedNamespaces:    []string{"team-a"},
		Units: []templatemigration.MigrationUnit{
			{
				ID:        "team-a/automatic",
				Namespace: "team-a",
				Service: templatemigration.ResourceIdentity{
					Namespace: "team-a",
					Name:      "automatic",
				},
				Outcome: templatemigration.OutcomeAutomatic,
			},
			{
				ID:        "team-a/manual",
				Namespace: "team-a",
				Service: templatemigration.ResourceIdentity{
					Namespace: "team-a",
					Name:      "manual",
				},
				Outcome: templatemigration.OutcomeManual,
			},
		},
	}
	contents, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	factory := func(_, _ string) (client.Client, error) {
		return fake.NewClientBuilder().Build(), nil
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := run(
		[]string{"apply", "--plan", planPath},
		&stdout,
		&stderr,
		factory,
	); exitCode != 1 {
		t.Fatalf("exit code = %d; stderr: %s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(
		stderr.String(),
		"apply dry-run summary: 0 succeeded, 1 failed, 1 manual, 0 skipped; 0 operations",
	) ||
		!strings.Contains(stderr.String(), "result: "+filepath.Join(filepath.Dir(planPath), "apply-result.json")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
