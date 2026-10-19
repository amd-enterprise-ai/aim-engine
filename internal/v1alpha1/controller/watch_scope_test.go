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

package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestServiceAndArtifactControllersDoNotWatchCABundleObjects(t *testing.T) {
	for _, path := range []string{
		"../aimservice/watches.go",
		"aimartifact_controller.go",
	} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Watches" {
				return true
			}
			object, ok := call.Args[0].(*ast.UnaryExpr)
			if !ok {
				return true
			}
			literal, ok := object.X.(*ast.CompositeLit)
			if !ok {
				return true
			}
			objectType, ok := literal.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := objectType.X.(*ast.Ident)
			if ok && pkg.Name == "corev1" &&
				(objectType.Sel.Name == "ConfigMap" || objectType.Sel.Name == "Secret") {
				t.Errorf("%s registers a corev1.%s watch", path, objectType.Sel.Name)
			}
			return true
		})
	}
}
