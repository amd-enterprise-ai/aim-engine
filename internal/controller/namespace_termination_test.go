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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsNamespaceTerminating(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name      string
		namespace *corev1.Namespace
		want      bool
	}{
		{
			name: "deleted namespace",
			want: true,
		},
		{
			name: "namespace with a deletion timestamp",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name:              "team-a",
				DeletionTimestamp: &now,
				Finalizers:        []string{"kubernetes"},
			}},
			want: true,
		},
		{
			name:      "active namespace",
			namespace: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team-a"}},
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder()
			if tt.namespace != nil {
				builder = builder.WithObjects(tt.namespace)
			}
			got, err := IsNamespaceTerminating(context.Background(), builder.Build(), "team-a")
			if err != nil {
				t.Fatalf("IsNamespaceTerminating: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsNamespaceTerminating = %v, want %v", got, tt.want)
			}
		})
	}
}
