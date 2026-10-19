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
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	kservev1alpha1 "github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	servingv1beta1 "github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/migration/templatemigration"
)

const usage = "usage: aim-template-migration plan|apply [flags]\n"

type clientFactory func(kubeconfigPath, contextName string) (client.Client, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, newKubernetesClient))
}

func run(args []string, stdout, stderr io.Writer, factory clientFactory) int {
	if len(args) == 0 {
		writef(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		writef(stdout, usage)
		return 0
	case "plan":
		return runPlan(args[1:], stdout, stderr, factory)
	case "apply":
		return runApply(args[1:], stdout, stderr, factory)
	default:
		writef(stderr, usage)
		return 2
	}
}

func runPlan(args []string, stdout, stderr io.Writer, factory clientFactory) int {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var namespaces []string
	var outputDirectory, kubeconfigPath, contextName, runtimeProjectionModeValue string
	flags.Func("namespace", "namespace to inventory; repeat for each selected namespace", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("namespace must not be empty")
		}
		namespaces = append(namespaces, value)
		return nil
	})
	flags.StringVar(&outputDirectory, "output-dir", "", "new private directory for backup.yaml and plan.json")
	flags.StringVar(
		&runtimeProjectionModeValue,
		"runtime-projection-mode",
		"",
		"controller deployment mode: Exhaustive, Reduced, or Both",
	)
	flags.StringVar(&kubeconfigPath, "kubeconfig", "", "kubeconfig file; defaults to standard loading rules")
	flags.StringVar(&contextName, "kube-context", "", "kubeconfig context; defaults to the current context")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		writef(stderr, "invalid plan arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 || len(namespaces) == 0 || strings.TrimSpace(outputDirectory) == "" {
		writef(stderr, "invalid plan arguments: at least one --namespace and --output-dir are required\n")
		return 2
	}
	if strings.TrimSpace(runtimeProjectionModeValue) == "" {
		writef(stderr, "invalid plan arguments: --runtime-projection-mode is required and must match the controller deployment\n")
		return 2
	}
	runtimeProjectionMode, err := aimv1alpha2.ParseRuntimeProjectionMode(runtimeProjectionModeValue)
	if err != nil {
		writef(stderr, "invalid plan arguments: %v\n", err)
		return 2
	}
	kubernetesClient, err := factory(kubeconfigPath, contextName)
	if err != nil {
		writef(stderr, "configure Kubernetes reader: %v\n", err)
		return 1
	}
	result, err := templatemigration.Plan(
		context.Background(),
		kubernetesClient,
		templatemigration.PlanOptions{
			Namespaces:            namespaces,
			RuntimeProjectionMode: runtimeProjectionMode,
		},
	)
	if err != nil {
		writef(stderr, "plan failed: %v\n", err)
		return 1
	}
	paths, err := templatemigration.WritePlanDirectory(outputDirectory, result)
	if err != nil {
		writef(stderr, "write plan: %v\n", err)
		return 1
	}
	summary := result.Summary()
	writePlanSummary(stdout, summary, paths)
	return 0
}

func writePlanSummary(
	writer io.Writer,
	summary templatemigration.Summary,
	paths templatemigration.Paths,
) {
	writef(
		writer,
		"planned %d services: %d automatic, %d manual, %d warnings\nbackup: %s\nplan: %s\n",
		summary.Services,
		summary.Automatic,
		summary.Manual,
		summary.Warnings,
		paths.Backup,
		paths.Plan,
	)
	for _, evidencePath := range paths.DedicatedHandoffEvidence {
		writef(writer, "dedicated handoff evidence: %s\n", evidencePath)
	}
	if len(paths.DedicatedHandoffEvidence) != 0 {
		writef(
			writer,
			"manual Dedicated handoff runbook: %s\n",
			templatemigration.DedicatedHandoffRunbookReference,
		)
	}
}

func runApply(args []string, stdout, stderr io.Writer, factory clientFactory) int {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var planPath, kubeconfigPath, contextName string
	var commit bool
	flags.StringVar(&planPath, "plan", "", "editable plan.json produced by plan")
	flags.BoolVar(&commit, "commit", false, "persist changes; defaults to server-side dry-run")
	flags.StringVar(&kubeconfigPath, "kubeconfig", "", "kubeconfig file; defaults to standard loading rules")
	flags.StringVar(&contextName, "kube-context", "", "kubeconfig context; defaults to the current context")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		writef(stderr, "invalid apply arguments: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 || planPath == "" {
		writef(stderr, "invalid apply arguments: --plan is required\n")
		return 2
	}
	kubernetesClient, err := factory(kubeconfigPath, contextName)
	if err != nil {
		writef(stderr, "configure Kubernetes client: %v\n", err)
		return 1
	}
	summary, err := templatemigration.Apply(
		context.Background(),
		kubernetesClient,
		templatemigration.ApplyOptions{PlanPath: planPath, Commit: commit},
	)
	if err != nil {
		writef(stderr, "apply failed: %v\n", err)
		if summary.ResultPath != "" {
			writeApplySummary(stderr, summary)
		}
		return 1
	}
	writeApplySummary(stdout, summary)
	return 0
}

func writeApplySummary(writer io.Writer, summary templatemigration.ApplySummary) {
	writef(
		writer,
		"apply %s summary: %d succeeded, %d failed, %d manual, %d skipped; %d operations\nresult: %s\n",
		summary.Mode,
		summary.Succeeded,
		summary.Failed,
		summary.Manual,
		summary.Skipped,
		summary.Operations,
		summary.ResultPath,
	)
}

func newKubernetesClient(kubeconfigPath, contextName string) (client.Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		overrides,
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		aimv1alpha1.AddToScheme,
		aimv1alpha2.AddToScheme,
		kservev1alpha1.AddToScheme,
		servingv1beta1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return nil, fmt.Errorf("register Kubernetes scheme: %w", err)
		}
	}
	kubernetesClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes reader: %w", err)
	}
	return kubernetesClient, nil
}

func writef(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format, args...)
}
