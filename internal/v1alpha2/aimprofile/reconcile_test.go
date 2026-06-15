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

package aimprofile

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	aimv1alpha2 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha2"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

func TestBuildComponentHealth(t *testing.T) {
	tests := []struct {
		name              string
		accelModel        string
		accelCount        int32
		resolvedResources *corev1.ResourceRequirements
		nodeErr           error
		matchResult       NodeMatchResult
		wantLen           int
		wantState         constants.AIMStatus
		wantReason        string
	}{
		{
			name:        "no accelerator returns nil",
			matchResult: NodeMatchResult{},
			wantLen:     0,
		},
		{
			name: "cpu-only resources returns nil",
			resolvedResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("32Gi"),
				},
			},
			matchResult: NodeMatchResult{},
			wantLen:     0,
		},
		{
			name:        "node list failure returns degraded",
			accelModel:  "MI300X",
			nodeErr:     fmt.Errorf("connection refused"),
			matchResult: NodeMatchResult{},
			wantLen:     1,
			wantState:   constants.AIMStatusDegraded,
			wantReason:  "NodeListFailed",
		},
		{
			name:              "matching nodes returns ready",
			accelModel:        "MI300X",
			accelCount:        1,
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 1, nil, "MI300X", nil),
			matchResult:       NodeMatchResult{MatchingNodes: 2},
			wantLen:           1,
			wantState:         constants.AIMStatusReady,
			wantReason:        aimv1alpha2.AIMProfileReasonHardwareAvailable,
		},
		{
			name:              "no matching nodes returns not available",
			accelModel:        "MI300X",
			accelCount:        1,
			resolvedResources: ResolveResources(aimv1alpha2.AcceleratorTypeGPU, 1, nil, "MI300X", nil),
			matchResult:       NodeMatchResult{MatchingNodes: 0},
			wantLen:           1,
			wantState:         constants.AIMStatusNotAvailable,
			wantReason:        aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildComponentHealth(tt.accelModel, tt.accelCount, tt.resolvedResources, tt.nodeErr, tt.matchResult)
			if len(result) != tt.wantLen {
				t.Fatalf("len(result) = %d, want %d", len(result), tt.wantLen)
			}
			if tt.wantLen > 0 {
				if result[0].State != tt.wantState {
					t.Errorf("state = %q, want %q", result[0].State, tt.wantState)
				}
				if result[0].Reason != tt.wantReason {
					t.Errorf("reason = %q, want %q", result[0].Reason, tt.wantReason)
				}
			}
		})
	}
}

func TestDecorateProfileStatus(t *testing.T) {
	tests := []struct {
		name            string
		spec            aimv1alpha2.AIMProfileSpecCommon
		nodeErr         error
		matchResult     NodeMatchResult
		wantVersion     string
		wantHWSummary   string
		wantMatchNodes  int32
		wantCondType    string
		wantCondStatus  metav1.ConditionStatus
		wantCondReason  string
		wantNoCond      bool
		wantHasAffinity bool
	}{
		{
			name: "CPU-only profile (no accelerator)",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image: "registry.io/model:2.0.0",
			},
			matchResult:    NodeMatchResult{},
			wantVersion:    "2.0.0",
			wantHWSummary:  "CPU",
			wantMatchNodes: 0,
			wantCondType:   aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: aimv1alpha2.AIMProfileReasonNoAccelerator,
		},
		{
			name: "GPU profile with matching nodes",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:1.0.0",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 1,
			},
			matchResult: NodeMatchResult{
				MatchingNodes: 3,
				NodeAffinity:  BuildNodeAffinity(aimv1alpha2.AcceleratorTypeGPU, "MI300X", "unpartitioned"),
			},
			wantVersion:     "1.0.0",
			wantHWSummary:   "1 x MI300X",
			wantMatchNodes:  3,
			wantCondType:    aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus:  metav1.ConditionTrue,
			wantCondReason:  aimv1alpha2.AIMProfileReasonHardwareAvailable,
			wantHasAffinity: true,
		},
		{
			name: "GPU profile with no matching nodes",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:0.8.5",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 4,
			},
			matchResult:     NodeMatchResult{MatchingNodes: 0},
			wantVersion:     "0.8.5",
			wantHWSummary:   "4 x MI300X",
			wantMatchNodes:  0,
			wantCondType:    aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus:  metav1.ConditionFalse,
			wantCondReason:  aimv1alpha2.AIMProfileReasonHardwareNotAvailable,
			wantHasAffinity: false,
		},
		{
			name: "image without tag produces empty version",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image: "registry.io/model",
			},
			matchResult:    NodeMatchResult{},
			wantVersion:    "",
			wantHWSummary:  "CPU",
			wantCondType:   aimv1alpha2.AIMProfileConditionHardwareAvailable,
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: aimv1alpha2.AIMProfileReasonNoAccelerator,
		},
		{
			name: "node list error skips HardwareAvailable condition",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image:            "registry.io/model:1.0.0",
				AcceleratorModel: "MI300X",
				AcceleratorType:  aimv1alpha2.AcceleratorTypeGPU,
				AcceleratorCount: 1,
			},
			nodeErr:        fmt.Errorf("connection refused"),
			matchResult:    NodeMatchResult{},
			wantVersion:    "1.0.0",
			wantHWSummary:  "1 x MI300X",
			wantMatchNodes: 0,
			wantNoCond:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &aimv1alpha2.AIMProfileStatus{}
			cm := controllerutils.NewConditionManager(nil)
			resolvedResources := ResolveResources(tt.spec.AcceleratorType, tt.spec.AcceleratorCount, tt.spec.Resources, tt.spec.AcceleratorModel, tt.spec.EngineEnv)

			decorateProfileStatus(
				status, cm, tt.spec,
				resolvedResources, tt.nodeErr, tt.matchResult,
				IsProfileDeployable(tt.spec), nil, "", "",
			)

			if status.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", status.Version, tt.wantVersion)
			}
			if status.HardwareSummary != tt.wantHWSummary {
				t.Errorf("HardwareSummary = %q, want %q", status.HardwareSummary, tt.wantHWSummary)
			}
			if status.MatchingNodes != tt.wantMatchNodes {
				t.Errorf("MatchingNodes = %d, want %d", status.MatchingNodes, tt.wantMatchNodes)
			}
			if tt.wantHasAffinity && status.ResolvedNodeAffinity == nil {
				t.Error("expected ResolvedNodeAffinity to be non-nil")
			}

			conds := cm.Conditions()
			if tt.wantNoCond {
				for _, c := range conds {
					if c.Type == aimv1alpha2.AIMProfileConditionHardwareAvailable {
						t.Errorf("expected no HardwareAvailable condition, but found one with status=%q reason=%q", c.Status, c.Reason)
					}
				}
				return
			}

			found := false
			for _, c := range conds {
				if c.Type == tt.wantCondType {
					found = true
					if c.Status != tt.wantCondStatus {
						t.Errorf("condition %q status = %q, want %q", c.Type, c.Status, tt.wantCondStatus)
					}
					if c.Reason != tt.wantCondReason {
						t.Errorf("condition %q reason = %q, want %q", c.Type, c.Reason, tt.wantCondReason)
					}
					break
				}
			}
			if !found {
				t.Errorf("expected condition %q not found in %v", tt.wantCondType, conds)
			}
		})
	}
}

func TestDecorateProfileStatus_SetsDeployableAndOriginAndCondition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		spec           aimv1alpha2.AIMProfileSpecCommon
		deployable     bool
		source         *aimv1alpha2.ProfileSourceModel
		origin         aimv1alpha1.ProfileOrigin
		wantCondStatus metav1.ConditionStatus
		wantCondReason string
	}{
		{
			name: "deployable profile gets Deployable=True",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				AimId: "qwen/qwen3-32b",
				Image: "registry.io/x:1.0.0",
				ModelSources: []aimv1alpha1.AIMModelSource{{
					ModelID:   "qwen/qwen3-32b",
					SourceURI: "hf://qwen/qwen3-32b",
				}},
			},
			deployable: true,
			source: &aimv1alpha2.ProfileSourceModel{
				Name: "qwen-model",
				Kind: aimv1alpha2.ProfileSourceModelKindAIMModel,
			},
			origin:         aimv1alpha1.ProfileOriginDiscovered,
			wantCondStatus: metav1.ConditionTrue,
			wantCondReason: aimv1alpha2.AIMProfileReasonDeployable,
		},
		{
			name: "base profile gets Deployable=False",
			spec: aimv1alpha2.AIMProfileSpecCommon{
				Image: "registry.io/x:1.0.0",
			},
			deployable:     false,
			origin:         aimv1alpha1.ProfileOriginUserAuthored,
			wantCondStatus: metav1.ConditionFalse,
			wantCondReason: aimv1alpha2.AIMProfileReasonBaseProfile,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := &aimv1alpha2.AIMProfileStatus{}
			cm := controllerutils.NewConditionManager(nil)
			decorateProfileStatus(
				status, cm, tc.spec,
				ResolveResources(tc.spec.AcceleratorType, tc.spec.AcceleratorCount, tc.spec.Resources, tc.spec.AcceleratorModel, tc.spec.EngineEnv),
				nil, NodeMatchResult{},
				tc.deployable, tc.source, tc.origin, "",
			)
			if status.Deployable != tc.deployable {
				t.Fatalf("status.Deployable = %v, want %v", status.Deployable, tc.deployable)
			}
			if status.SourceModel != tc.source {
				t.Fatalf("status.SourceModel = %#v, want %#v", status.SourceModel, tc.source)
			}
			if status.Origin != tc.origin {
				t.Fatalf("status.Origin = %q, want %q", status.Origin, tc.origin)
			}
			var got metav1.Condition
			for _, c := range cm.Conditions() {
				if c.Type == aimv1alpha2.AIMProfileConditionDeployable {
					got = c
					break
				}
			}
			if got.Type == "" {
				t.Fatalf("Deployable condition missing in %v", cm.Conditions())
			}
			if got.Status != tc.wantCondStatus {
				t.Fatalf("Deployable status = %q, want %q", got.Status, tc.wantCondStatus)
			}
			if got.Reason != tc.wantCondReason {
				t.Fatalf("Deployable reason = %q, want %q", got.Reason, tc.wantCondReason)
			}
		})
	}
}

func TestStampProfileProvenance_AndDerivers(t *testing.T) {
	t.Parallel()

	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "team-a",
			Labels:    map[string]string{"user-key": "user-value"},
		},
	}
	src := &aimv1alpha2.ProfileSourceModel{
		Name:      "owner-model",
		Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
		Namespace: "team-a",
	}
	StampProfileProvenance(
		profile,
		constants.LabelValueProfileRoleDeployable,
		aimv1alpha1.ProfileOriginDiscovered,
		src,
	)
	if profile.Labels["user-key"] != "user-value" {
		t.Fatalf("user-set label was lost: %#v", profile.Labels)
	}
	if profile.Labels[constants.LabelKeyProfileRole] != constants.LabelValueProfileRoleDeployable {
		t.Fatalf("role label = %q", profile.Labels[constants.LabelKeyProfileRole])
	}
	if profile.Labels[constants.LabelKeyProfileOrigin] != string(aimv1alpha1.ProfileOriginDiscovered) {
		t.Fatalf("origin label = %q", profile.Labels[constants.LabelKeyProfileOrigin])
	}
	if profile.Labels[constants.LabelKeySourceModel] != "owner-model" {
		t.Fatalf("source-model label = %q", profile.Labels[constants.LabelKeySourceModel])
	}
	if profile.Labels[constants.LabelKeySourceModelScope] != constants.LabelValueSourceModelScopeNamespace {
		t.Fatalf("source-model-scope label = %q", profile.Labels[constants.LabelKeySourceModelScope])
	}

	// nil source should clear the source-model labels.
	StampProfileProvenance(profile, constants.LabelValueProfileRoleDeployable, aimv1alpha1.ProfileOriginUserAuthored, nil)
	if _, ok := profile.Labels[constants.LabelKeySourceModel]; ok {
		t.Fatalf("source-model label not cleared: %#v", profile.Labels)
	}
}

func TestDeriveProfileOrigin_PrefersExistingLabel(t *testing.T) {
	t.Parallel()

	profile := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{constants.LabelKeyProfileOrigin: string(aimv1alpha1.ProfileOriginDiscovered)},
		},
	}
	if got := DeriveProfileOrigin(profile); got != aimv1alpha1.ProfileOriginDiscovered {
		t.Fatalf("DeriveProfileOrigin() = %q, want %q (existing label wins)", got, aimv1alpha1.ProfileOriginDiscovered)
	}

	owned := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: aimv1alpha2.GroupVersion.String(),
				Kind:       "AIMModel",
				Name:       "owner",
			}},
			Annotations: map[string]string{AnnotationProfileSource: ProfileSourceImage},
		},
	}
	if got := DeriveProfileOrigin(owned); got != aimv1alpha1.ProfileOriginDiscovered {
		t.Fatalf("AIMModel-owned image source = %q, want Discovered", got)
	}

	derived := &aimv1alpha2.AIMProfile{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: aimv1alpha2.GroupVersion.String(),
				Kind:       "AIMProfileSet",
				Name:       "owner-set",
			}},
		},
	}
	if got := DeriveProfileOrigin(derived); got != aimv1alpha1.ProfileOriginDerived {
		t.Fatalf("AIMProfileSet-owned profile = %q, want Derived", got)
	}

	user := &aimv1alpha2.AIMProfile{}
	if got := DeriveProfileOrigin(user); got != aimv1alpha1.ProfileOriginUserAuthored {
		t.Fatalf("unowned profile = %q, want UserAuthored", got)
	}
}

// TestSourceModelFromOwnerRefs_ProfileSetOwnerInheritsPropagatedLabels
// pins the contract that profiles owned by an AIM(Cluster)ProfileSet — the
// derivation path — read their source-model from the propagated labels
// stamped on the profile via apply.PropagateLabels rather than returning
// nil. Without this branch, the AIMProfile reconciler's pre-step
// EnsureProfileProvenanceLabels would race with the propagation step:
// PropagateLabels stamps source-model on every parent reconcile, then the
// reconciler clears it because SourceModelFromOwnerRefs returned nil. The
// resulting label flap drove the AIMService spec.model.name resolver into
// a hot loop (~150 patches/sec) on profiles like the ones produced by the
// resolve-by-model AIMService chainsaw test.
func TestSourceModelFromOwnerRefs_ProfileSetOwnerInheritsPropagatedLabels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		ownerKind string
		labels    map[string]string
		namespace string
		want      *aimv1alpha2.ProfileSourceModel
	}{
		{
			name:      "AIMProfileSet owner with namespace-scoped propagated labels",
			ownerKind: "AIMProfileSet",
			labels: map[string]string{
				constants.LabelKeySourceModel:      "owner-model",
				constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
			},
			namespace: "team-a",
			want: &aimv1alpha2.ProfileSourceModel{
				Name:      "owner-model",
				Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
				Namespace: "team-a",
			},
		},
		{
			name:      "AIMClusterProfileSet owner with cluster-scoped propagated labels",
			ownerKind: "AIMClusterProfileSet",
			labels: map[string]string{
				constants.LabelKeySourceModel:      "owner-cluster-model",
				constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeCluster,
			},
			want: &aimv1alpha2.ProfileSourceModel{
				Name: "owner-cluster-model",
				Kind: aimv1alpha2.ProfileSourceModelKindAIMClusterModel,
			},
		},
		{
			name:      "AIMProfileSet owner with no propagated labels yet returns nil",
			ownerKind: "AIMProfileSet",
			labels:    nil,
			namespace: "team-a",
			want:      nil,
		},
		{
			name:      "AIMProfileSet owner missing scope label defaults to namespace kind",
			ownerKind: "AIMProfileSet",
			labels:    map[string]string{constants.LabelKeySourceModel: "owner-model"},
			namespace: "team-a",
			want: &aimv1alpha2.ProfileSourceModel{
				Name:      "owner-model",
				Kind:      aimv1alpha2.ProfileSourceModelKindAIMModel,
				Namespace: "team-a",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			profile := &aimv1alpha2.AIMProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "p",
					Namespace: tc.namespace,
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: aimv1alpha2.GroupVersion.String(),
						Kind:       tc.ownerKind,
						Name:       "owner-set",
						Controller: ptrTrue(),
					}},
					Labels: tc.labels,
				},
			}
			got := SourceModelFromOwnerRefs(profile, tc.namespace)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("SourceModelFromOwnerRefs() = %v, want %v", got, tc.want)
			}
			if got == nil {
				return
			}
			if got.Name != tc.want.Name {
				t.Errorf("Name = %q, want %q", got.Name, tc.want.Name)
			}
			if got.Kind != tc.want.Kind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want.Kind)
			}
			if got.Namespace != tc.want.Namespace {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tc.want.Namespace)
			}
		})
	}
}

func ptrTrue() *bool {
	t := true
	return &t
}

func TestProvenanceLabelSelector_RoleAndScopeAndOrigin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		selector    aimv1alpha1.ProfileSelector
		wantScope   SelectorScope
		wantMatches map[string]string
		wantNoMatch map[string]string
	}{
		{
			name:     "default role matches profiles not labelled base",
			selector: aimv1alpha1.ProfileSelector{},
			wantMatches: map[string]string{
				constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable,
			},
			wantNoMatch: map[string]string{
				constants.LabelKeyProfileRole: constants.LabelValueProfileRoleBase,
			},
			wantScope: SelectorScopeAny,
		},
		{
			name:     "role: base only matches base-labelled",
			selector: aimv1alpha1.ProfileSelector{Role: aimv1alpha1.ProfileSelectorRoleBase},
			wantMatches: map[string]string{
				constants.LabelKeyProfileRole: constants.LabelValueProfileRoleBase,
			},
			wantNoMatch: map[string]string{
				constants.LabelKeyProfileRole: constants.LabelValueProfileRoleDeployable,
			},
			wantScope: SelectorScopeAny,
		},
		{
			name: "modelRef with Namespace scope requires source-model and namespace scope",
			selector: aimv1alpha1.ProfileSelector{
				ModelRef: &aimv1alpha1.ProfileSelectorModelRef{
					Name:  "owner",
					Scope: aimv1alpha1.ProfileSelectorScopeNamespace,
				},
			},
			wantMatches: map[string]string{
				constants.LabelKeyProfileRole:      constants.LabelValueProfileRoleDeployable,
				constants.LabelKeySourceModel:      "owner",
				constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeNamespace,
			},
			wantNoMatch: map[string]string{
				constants.LabelKeyProfileRole:      constants.LabelValueProfileRoleDeployable,
				constants.LabelKeySourceModel:      "owner",
				constants.LabelKeySourceModelScope: constants.LabelValueSourceModelScopeCluster,
			},
			wantScope: SelectorScopeNamespace,
		},
		{
			name: "origin filter requires matching origin label",
			selector: aimv1alpha1.ProfileSelector{
				Origin: aimv1alpha1.ProfileOriginDerived,
			},
			wantMatches: map[string]string{
				constants.LabelKeyProfileOrigin: string(aimv1alpha1.ProfileOriginDerived),
			},
			wantNoMatch: map[string]string{
				constants.LabelKeyProfileOrigin: string(aimv1alpha1.ProfileOriginDiscovered),
			},
			wantScope: SelectorScopeAny,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, scope, err := ProvenanceLabelSelector(tc.selector)
			if err != nil {
				t.Fatalf("ProvenanceLabelSelector() error = %v", err)
			}
			if scope != tc.wantScope {
				t.Fatalf("scope = %v, want %v", scope, tc.wantScope)
			}
			if !sel.Matches(labels.Set(tc.wantMatches)) {
				t.Fatalf("selector %q failed to match %v", sel.String(), tc.wantMatches)
			}
			if sel.Matches(labels.Set(tc.wantNoMatch)) {
				t.Fatalf("selector %q unexpectedly matched %v", sel.String(), tc.wantNoMatch)
			}
		})
	}
}
