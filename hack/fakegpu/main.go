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

// Command fakegpu prints fake AMD GPU nodes for KWOK: Node objects with the
// labels, taints and capacity of AMD GPU nodes, and for DRA groups the
// ResourceSlices the AMD GPU DRA driver would publish. The nodes exist only in
// the API, so tests can check where GPU workloads are scheduled without GPUs.
//
//	go run ./hack/fakegpu -f hack/fakegpu/fleet.yaml | kubectl create -f -
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func main() {
	fleetPath := flag.String("f", "hack/fakegpu/fleet.yaml", "fleet file describing the node groups")
	deviceClass := flag.Bool("device-class", false,
		"print only the "+DriverName+" DeviceClass, for clusters without the AMD GPU DRA driver")
	flag.Parse()

	if err := run(os.Stdout, *fleetPath, *deviceClass); err != nil {
		fmt.Fprintln(os.Stderr, "fakegpu:", err)
		os.Exit(1)
	}
}

func run(out io.Writer, fleetPath string, deviceClass bool) error {
	if deviceClass {
		return writeYAML(out, []runtime.Object{DeviceClass()})
	}
	data, err := os.ReadFile(fleetPath)
	if err != nil {
		return err
	}
	fleet, err := ParseFleet(data)
	if err != nil {
		return fmt.Errorf("%s: %w", fleetPath, err)
	}
	objects, err := fleet.Objects()
	if err != nil {
		return err
	}
	return writeYAML(out, objects)
}

// writeYAML prints the objects as YAML documents, without the zero-valued
// fields the API server and KWOK fill in.
func writeYAML(out io.Writer, objects []runtime.Object) error {
	for _, obj := range objects {
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return err
		}
		unstructured.RemoveNestedField(u, "metadata", "creationTimestamp")
		unstructured.RemoveNestedField(u, "status", "daemonEndpoints")
		unstructured.RemoveNestedField(u, "status", "nodeInfo")
		data, err := yaml.Marshal(u)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "---\n%s", data); err != nil {
			return err
		}
	}
	return nil
}
