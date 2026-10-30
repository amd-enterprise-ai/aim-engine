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

package aimtemplatecache

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/constants"
	controllerutils "github.com/amd-enterprise-ai/aim-engine/internal/controller/utils"
)

const (
	testSourceURI = "hf://meta-llama/Llama-3.1-8B-Instruct"
	testModelID   = "meta-llama/Llama-3.1-8B-Instruct"
)

func testTemplateCache(name string, mode aimv1alpha1.AIMTemplateCacheMode) *aimv1alpha1.AIMTemplateCache {
	return &aimv1alpha1.AIMTemplateCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
		},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			TemplateName:  "test-template",
			TemplateScope: aimv1alpha1.AIMServiceTemplateScopeNamespace,
			Mode:          mode,
		},
	}
}

func testArtifact(name string, status constants.AIMStatus) aimv1alpha1.AIMArtifact {
	return aimv1alpha1.AIMArtifact{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       aimv1alpha1.AIMArtifactSpec{SourceURI: testSourceURI, ModelID: testModelID},
		Status:     aimv1alpha1.AIMArtifactStatus{Status: status},
	}
}

// markTerminating stages an artifact caught mid-deletion: a deletionTimestamp, a
// finalizer still holding the object, and the PVC the cache would otherwise publish.
// A deletionTimestamp is only ever observable while some finalizer holds the object, so
// the fixture carries one.
func markTerminating(artifact aimv1alpha1.AIMArtifact) aimv1alpha1.AIMArtifact {
	deletedAt := metav1.NewTime(time.Date(2026, 7, 30, 12, 58, 18, 0, time.UTC))
	artifact.DeletionTimestamp = &deletedAt
	artifact.Finalizers = []string{"example.com/hold"}
	artifact.Status.PersistentVolumeClaim = artifact.Name + "-cache-e1a0d5dc"
	return artifact
}

func testFetchResult(tc *aimv1alpha1.AIMTemplateCache, artifacts ...aimv1alpha1.AIMArtifact) TemplateCacheFetchResult {
	return TemplateCacheFetchResult{
		templateCache: tc,
		serviceTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
			Value: &aimv1alpha1.AIMServiceTemplate{
				Status: aimv1alpha1.AIMServiceTemplateStatus{
					ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: testModelID, SourceURI: testSourceURI}},
				},
			},
		},
		artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
			Value: &aimv1alpha1.AIMArtifactList{Items: artifacts},
		},
	}
}

func TestArtifactAdoptableBy(t *testing.T) {
	owner := testTemplateCache("owner", aimv1alpha1.TemplateCacheModeDedicated)
	ownerRef := metav1.OwnerReference{Kind: "AIMTemplateCache", Name: owner.Name, UID: owner.UID}

	tests := []struct {
		name          string
		templateCache func() *aimv1alpha1.AIMTemplateCache
		artifact      func() aimv1alpha1.AIMArtifact
		want          bool
	}{
		{
			name: "shared cache adopts an unowned artifact it did not create",
			templateCache: func() *aimv1alpha1.AIMTemplateCache {
				return testTemplateCache("other", aimv1alpha1.TemplateCacheModeShared)
			},
			artifact: func() aimv1alpha1.AIMArtifact { return testArtifact("artifact", constants.AIMStatusReady) },
			want:     true,
		},
		{
			name: "shared cache rejects an owned artifact",
			templateCache: func() *aimv1alpha1.AIMTemplateCache {
				return testTemplateCache("other", aimv1alpha1.TemplateCacheModeShared)
			},
			artifact: func() aimv1alpha1.AIMArtifact {
				a := testArtifact("artifact", constants.AIMStatusReady)
				a.OwnerReferences = []metav1.OwnerReference{ownerRef}
				return a
			},
			want: false,
		},
		{
			name:          "dedicated cache adopts its own artifact",
			templateCache: func() *aimv1alpha1.AIMTemplateCache { return owner },
			artifact: func() aimv1alpha1.AIMArtifact {
				a := testArtifact("artifact", constants.AIMStatusReady)
				a.OwnerReferences = []metav1.OwnerReference{ownerRef}
				return a
			},
			want: true,
		},
		{
			name:          "dedicated cache rejects an unowned artifact",
			templateCache: func() *aimv1alpha1.AIMTemplateCache { return owner },
			artifact:      func() aimv1alpha1.AIMArtifact { return testArtifact("artifact", constants.AIMStatusReady) },
			want:          false,
		},
		{
			name: "cache pinned to a storage class rejects an artifact on another",
			templateCache: func() *aimv1alpha1.AIMTemplateCache {
				tc := testTemplateCache("pinned", aimv1alpha1.TemplateCacheModeShared)
				tc.Spec.StorageClassName = "fast"
				return tc
			},
			artifact: func() aimv1alpha1.AIMArtifact {
				a := testArtifact("artifact", constants.AIMStatusReady)
				a.Spec.StorageClassName = "slow"
				return a
			},
			want: false,
		},
		{
			name: "cache with no storage class accepts any",
			templateCache: func() *aimv1alpha1.AIMTemplateCache {
				return testTemplateCache("unpinned", aimv1alpha1.TemplateCacheModeShared)
			},
			artifact: func() aimv1alpha1.AIMArtifact {
				a := testArtifact("artifact", constants.AIMStatusReady)
				a.Spec.StorageClassName = "slow"
				return a
			},
			want: true,
		},
		{
			// Eligibility must stay true while the artifact is terminating: the artifact
			// watch filters events through this predicate, so rejecting it here would
			// drop both the deletion update and the final Delete event, and every cache
			// that published the artifact would keep advertising it until the next
			// resync. ComposeState is what refuses to adopt it.
			name: "terminating artifact stays eligible so the watch still wakes adopters",
			templateCache: func() *aimv1alpha1.AIMTemplateCache {
				return testTemplateCache("adopter", aimv1alpha1.TemplateCacheModeShared)
			},
			artifact: func() aimv1alpha1.AIMArtifact {
				return markTerminating(testArtifact("artifact", constants.AIMStatusReady))
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := tt.artifact()
			if got := ArtifactAdoptableBy(tt.templateCache(), &artifact); got != tt.want {
				t.Fatalf("ArtifactAdoptableBy() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A shared cache resolves an artifact created by a different cache, which is what
// makes the artifact watch fan-out necessary in the first place.
func TestComposeState_AdoptsArtifactCreatedByAnotherCache(t *testing.T) {
	tc := testTemplateCache("cache-mi300x-lat", aimv1alpha1.TemplateCacheModeShared)
	foreign := testArtifact("hf---meta-llama-llama-3-1-8b-instruct-d4dc6f81cc", constants.AIMStatusReady)
	foreign.Labels = map[string]string{constants.LabelTemplateCacheName: "cache-mi300x-thr"}

	obs := (&TemplateCacheReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc},
		TemplateCacheFetchResult{
			templateCache: tc,
			serviceTemplate: controllerutils.FetchResult[*aimv1alpha1.AIMServiceTemplate]{
				Value: &aimv1alpha1.AIMServiceTemplate{
					Status: aimv1alpha1.AIMServiceTemplateStatus{
						ModelSources: []aimv1alpha1.AIMModelSource{{ModelID: testModelID, SourceURI: testSourceURI}},
					},
				},
			},
			artifacts: controllerutils.FetchResult[*aimv1alpha1.AIMArtifactList]{
				Value: &aimv1alpha1.AIMArtifactList{Items: []aimv1alpha1.AIMArtifact{foreign}},
			},
		},
	)

	if len(obs.MissingCaches) != 0 {
		t.Fatalf("MissingCaches = %v, want none", obs.MissingCaches)
	}
	if got := obs.BestArtifacts[testModelID].Name; got != foreign.Name {
		t.Fatalf("BestArtifacts[%q] = %q, want %q", testModelID, got, foreign.Name)
	}
}

// An artifact that is being deleted must not be adopted: its PVC is about to be
// garbage collected, so publishing it would point every consumer of this cache at
// storage that is going away. It must be reported as terminating whether or not it
// ever published a status, so the cache waits instead of applying over it.
func TestComposeState_doesNotAdoptTerminatingArtifact(t *testing.T) {
	tests := []struct {
		name           string
		mode           aimv1alpha1.AIMTemplateCacheMode
		artifactStatus constants.AIMStatus
	}{
		{
			name:           "shared cache, artifact was ready",
			mode:           aimv1alpha1.TemplateCacheModeShared,
			artifactStatus: constants.AIMStatusReady,
		},
		{
			name:           "dedicated cache, artifact was ready",
			mode:           aimv1alpha1.TemplateCacheModeDedicated,
			artifactStatus: constants.AIMStatusReady,
		},
		{
			// The deletion check must run before the empty-status guard. An artifact
			// deleted before its controller published a first status still has to be
			// waited out, because the replacement hashes to the same name in Shared
			// mode and the API server accepts an apply over a terminating object
			// silently. Reordering these two guards puts the model source back in
			// MissingCaches and writes to the dying object.
			name:           "shared cache, artifact never got a status",
			mode:           aimv1alpha1.TemplateCacheModeShared,
			artifactStatus: "",
		},
		{
			name:           "dedicated cache, artifact never got a status",
			mode:           aimv1alpha1.TemplateCacheModeDedicated,
			artifactStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := testTemplateCache("cache", tt.mode)
			terminating := markTerminating(testArtifact("hf---meta-llama-llama-3-1-8b-instruct-d4dc6f81cc", tt.artifactStatus))
			if tt.mode == aimv1alpha1.TemplateCacheModeDedicated {
				terminating.OwnerReferences = []metav1.OwnerReference{{Kind: "AIMTemplateCache", Name: tc.Name, UID: tc.UID}}
			}

			reconciler := &TemplateCacheReconciler{}
			reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc}
			obs := reconciler.ComposeState(context.Background(), reconcileCtx, testFetchResult(tc, terminating))

			if len(obs.BestArtifacts) != 0 {
				t.Fatalf("BestArtifacts = %v, want none", obs.BestArtifacts)
			}
			if got := obs.TerminatingArtifactNames; len(got) != 1 || got[0] != terminating.Name {
				t.Fatalf("TerminatingArtifactNames = %v, want [%s]", got, terminating.Name)
			}
			// Not MissingCaches: a replacement would hash to the same name in Shared
			// mode, so the apply would patch the object that is being deleted.
			if len(obs.MissingCaches) != 0 {
				t.Fatalf("MissingCaches = %v, want none while the artifact is terminating", obs.MissingCaches)
			}

			plan := reconciler.PlanResources(context.Background(), reconcileCtx, obs)
			if applied := len(plan.GetToApply()) + len(plan.GetToApplyWithoutOwnerRef()); applied != 0 {
				t.Fatalf("planned %d applies while the artifact was terminating, want 0", applied)
			}
		})
	}
}

// A terminating artifact must not shadow a live one serving the same model source.
func TestComposeState_adoptsLiveArtifactAlongsideTerminatingOne(t *testing.T) {
	tc := testTemplateCache("cache", aimv1alpha1.TemplateCacheModeShared)
	terminating := markTerminating(testArtifact("artifact-dying", constants.AIMStatusReady))
	live := testArtifact("artifact-live", constants.AIMStatusReady)

	obs := (&TemplateCacheReconciler{}).ComposeState(
		context.Background(),
		controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc},
		testFetchResult(tc, terminating, live),
	)

	if got := obs.BestArtifacts[testModelID].Name; got != live.Name {
		t.Fatalf("BestArtifacts[%q] = %q, want %q", testModelID, got, live.Name)
	}
	if len(obs.TerminatingArtifactNames) != 0 {
		t.Fatalf("TerminatingArtifactNames = %v, want none once a live artifact resolves the source", obs.TerminatingArtifactNames)
	}
}

// Once the delete completes the artifact leaves the List, so the source becomes missing
// and the next plan recreates it. This is the other half of "no permanent wedge": the
// cache holds off while the artifact terminates, then creates a replacement cleanly.
// The wakeup that ends the wait is the artifact's own Delete event.
func TestPlanResources_recreatesArtifactAfterDeletionCompletes(t *testing.T) {
	tc := testTemplateCache("cache", aimv1alpha1.TemplateCacheModeShared)
	reconcileCtx := controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc}
	reconciler := &TemplateCacheReconciler{}

	terminatingObs := reconciler.ComposeState(context.Background(), reconcileCtx,
		testFetchResult(tc, markTerminating(testArtifact("artifact-dying", constants.AIMStatusReady))))
	terminatingPlan := reconciler.PlanResources(context.Background(), reconcileCtx, terminatingObs)

	if applied := len(terminatingPlan.GetToApply()) + len(terminatingPlan.GetToApplyWithoutOwnerRef()); applied != 0 {
		t.Fatalf("planned %d applies while the artifact was terminating, want 0", applied)
	}

	goneObs := reconciler.ComposeState(context.Background(), reconcileCtx, testFetchResult(tc))
	gonePlan := reconciler.PlanResources(context.Background(), reconcileCtx, goneObs)

	if got := len(gonePlan.GetToApplyWithoutOwnerRef()); got != 1 {
		t.Fatalf("planned %d shared artifacts after the delete completed, want 1", got)
	}
}

// The cache must drop out of Ready while its only candidate is terminating, and it must
// stop advertising the doomed artifact and its PVC.
func TestDecorateStatus_terminatingArtifactIsNotReady(t *testing.T) {
	terminating := markTerminating(testArtifact("artifact-dying", constants.AIMStatusReady))
	status := &aimv1alpha1.AIMTemplateCacheStatus{
		Artifacts: map[string]aimv1alpha1.AIMResolvedArtifact{
			terminating.Name: {
				Name:                  terminating.Name,
				Model:                 testModelID,
				Status:                constants.AIMStatusReady,
				PersistentVolumeClaim: terminating.Status.PersistentVolumeClaim,
			},
		},
	}
	cm := controllerutils.NewConditionManager(nil)

	(&TemplateCacheReconciler{}).DecorateStatus(status, cm, TemplateCacheObservation{
		TerminatingArtifactNames: []string{terminating.Name},
	})

	condition := cm.Get(artifactsReadyConditionType)
	if condition == nil {
		t.Fatalf("no %s condition was set", artifactsReadyConditionType)
	}
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("%s = %v, want False", artifactsReadyConditionType, condition.Status)
	}
	if condition.Reason != artifactTerminatingReason {
		t.Fatalf("reason = %q, want %q", condition.Reason, artifactTerminatingReason)
	}
	if !strings.Contains(condition.Message, terminating.Name) {
		t.Fatalf("message %q does not name the terminating artifact %q", condition.Message, terminating.Name)
	}
	if status.Artifacts != nil {
		t.Fatalf("status.Artifacts = %v, want the doomed artifact and its PVC withdrawn", status.Artifacts)
	}
	migration := cm.Get(controllerutils.ConditionTypeMigrationRequired)
	if migration == nil || migration.Status != metav1.ConditionTrue {
		t.Fatalf("MigrationRequired = %#v, want True", migration)
	}
}

func TestS3ArtifactIdentityIncludesRuntimeConfig(t *testing.T) {
	source := aimv1alpha1.AIMModelSource{
		ModelID:   "org/model",
		SourceURI: "s3://bucket/model",
	}
	first := &aimv1alpha1.AIMTemplateCache{
		ObjectMeta: metav1.ObjectMeta{Name: "first"},
		Spec: aimv1alpha1.AIMTemplateCacheSpec{
			RuntimeConfigRef: aimv1alpha1.RuntimeConfigRef{Name: "first-s3"},
		},
	}
	second := first.DeepCopy()
	second.Name = "second"
	second.Spec.Name = "second-s3"

	firstName, err := generateArtifactName(first, source)
	if err != nil {
		t.Fatal(err)
	}
	secondName, err := generateArtifactName(second, source)
	if err != nil {
		t.Fatal(err)
	}
	if firstName == secondName {
		t.Fatalf("different S3 runtime configs produced the same artifact name %q", firstName)
	}
	if s3RuntimeConfigMatches(
		source.SourceURI,
		first.Spec.RuntimeConfigRef,
		second.Spec.RuntimeConfigRef,
	) {
		t.Fatal("S3 artifacts from different runtime configs must not be reused")
	}
	if !s3RuntimeConfigMatches(
		"hf://org/model",
		first.Spec.RuntimeConfigRef,
		second.Spec.RuntimeConfigRef,
	) {
		t.Fatal("Hugging Face artifact reuse must retain existing behavior")
	}
}

func TestS3ArtifactIdentityNormalizesDefaultRuntimeConfig(t *testing.T) {
	if !s3RuntimeConfigMatches(
		"s3://bucket/model",
		aimv1alpha1.RuntimeConfigRef{},
		aimv1alpha1.RuntimeConfigRef{Name: "default"},
	) {
		t.Fatal("empty and explicit default runtime config references must match")
	}
}

func TestPlanResources_CopiesDeclaredDownloadFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter *aimv1alpha1.AIMDownloadFilter
		// want is the spec the planner must persist. The filter is canonicalized
		// (sorted, deduplicated) before apply so that caches declaring the same file
		// set with reordered or duplicated globs submit byte-identical immutable
		// specs; nil stays nil so "no declaration" never collides with empty {}.
		want *aimv1alpha1.AIMDownloadFilter
	}{
		{name: "no filter", filter: nil, want: nil},
		{
			name:   "declared filter",
			filter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin"}},
			want:   &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin"}},
		},
		{
			name:   "reordered and duplicated globs canonicalized",
			filter: &aimv1alpha1.AIMDownloadFilter{Include: []string{"b/*", "a/*", "a/*"}, Exclude: []string{"*.md", "*.bin"}},
			want:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"a/*", "b/*"}, Exclude: []string{"*.bin", "*.md"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := testTemplateCache("cache", aimv1alpha1.TemplateCacheModeShared)
			obs := TemplateCacheObservation{
				TemplateCacheFetchResult: TemplateCacheFetchResult{templateCache: tc},
				MissingCaches: []aimv1alpha1.AIMModelSource{
					{ModelID: testModelID, SourceURI: testSourceURI, DownloadFilter: tt.filter},
				},
			}

			result := (&TemplateCacheReconciler{}).PlanResources(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc},
				obs,
			)
			planned := result.GetToApplyWithoutOwnerRef()
			if len(planned) != 1 {
				t.Fatalf("expected 1 artifact, got %d", len(planned))
			}
			art := planned[0].(*aimv1alpha1.AIMArtifact)
			if !reflect.DeepEqual(art.Spec.DownloadFilter, tt.want) {
				t.Errorf("artifact downloadFilter = %+v, want %+v", art.Spec.DownloadFilter, tt.want)
			}
		})
	}
}

// The golden names pin today's identity for sources without a declared filter:
// any drift renames every existing shared artifact and orphans its cache.
func TestArtifactName_StableWithoutDeclaredFilter(t *testing.T) {
	source := aimv1alpha1.AIMModelSource{ModelID: "org/model", SourceURI: "hf://org/model"}
	tests := []struct {
		mode aimv1alpha1.AIMTemplateCacheMode
		want string
	}{
		{mode: aimv1alpha1.TemplateCacheModeShared, want: "hf---org-model-04cea1e1c0"},
		{mode: aimv1alpha1.TemplateCacheModeDedicated, want: "hf---org-model-a12a99747c"},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			tc := &aimv1alpha1.AIMTemplateCache{
				ObjectMeta: metav1.ObjectMeta{Name: "tc"},
				Spec:       aimv1alpha1.AIMTemplateCacheSpec{Mode: tt.mode},
			}
			got, err := generateArtifactName(tc, source)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("artifact name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArtifactName_DivergesByDeclaredFilter(t *testing.T) {
	tc := &aimv1alpha1.AIMTemplateCache{
		ObjectMeta: metav1.ObjectMeta{Name: "tc"},
		Spec:       aimv1alpha1.AIMTemplateCacheSpec{Mode: aimv1alpha1.TemplateCacheModeShared},
	}
	nameFor := func(filter *aimv1alpha1.AIMDownloadFilter) string {
		t.Helper()
		name, err := generateArtifactName(tc, aimv1alpha1.AIMModelSource{
			ModelID: "org/model", SourceURI: "hf://org/model", DownloadFilter: filter,
		})
		if err != nil {
			t.Fatal(err)
		}
		return name
	}

	filters := map[string]*aimv1alpha1.AIMDownloadFilter{
		"nil":            nil,
		"empty":          {},
		"exclude subdir": {Exclude: []string{"*/*"}},
		"include only":   {Include: []string{"*.safetensors"}},
	}
	seen := map[string]string{}
	for label, filter := range filters {
		name := nameFor(filter)
		if other, ok := seen[name]; ok {
			t.Errorf("filters %q and %q share artifact name %q", label, other, name)
		}
		seen[name] = label
	}

	ordered := nameFor(&aimv1alpha1.AIMDownloadFilter{Include: []string{"a/*", "b/*"}, Exclude: []string{"*.bin", "*.md"}})
	reordered := nameFor(&aimv1alpha1.AIMDownloadFilter{Include: []string{"b/*", "a/*"}, Exclude: []string{"*.md", "*.bin"}})
	if ordered != reordered {
		t.Errorf("glob order changed the artifact name: %q vs %q", ordered, reordered)
	}
}

func TestComposeState_MatchesArtifactByDownloadFilter(t *testing.T) {
	tests := []struct {
		name           string
		artifactFilter *aimv1alpha1.AIMDownloadFilter
		sourceFilter   *aimv1alpha1.AIMDownloadFilter
		wantAdopted    bool
	}{
		{name: "both absent", wantAdopted: true},
		{name: "source declares, artifact has none", sourceFilter: &aimv1alpha1.AIMDownloadFilter{}},
		{name: "artifact has one, source declares none", artifactFilter: &aimv1alpha1.AIMDownloadFilter{}},
		{
			name:           "different filters",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*/*"}},
			sourceFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{"*.safetensors"}},
		},
		{
			name:           "equal filters in different order",
			artifactFilter: &aimv1alpha1.AIMDownloadFilter{Exclude: []string{"*.bin", "*.md"}},
			sourceFilter:   &aimv1alpha1.AIMDownloadFilter{Include: []string{}, Exclude: []string{"*.md", "*.bin"}},
			wantAdopted:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := testTemplateCache("cache", aimv1alpha1.TemplateCacheModeShared)
			artifact := testArtifact("artifact", constants.AIMStatusReady)
			artifact.Spec.DownloadFilter = tt.artifactFilter
			fetch := testFetchResult(tc, artifact)
			fetch.serviceTemplate.Value.Status.ModelSources[0].DownloadFilter = tt.sourceFilter

			obs := (&TemplateCacheReconciler{}).ComposeState(
				context.Background(),
				controllerutils.ReconcileContext[*aimv1alpha1.AIMTemplateCache]{Object: tc},
				fetch,
			)

			_, adopted := obs.BestArtifacts[testModelID]
			if adopted != tt.wantAdopted {
				t.Errorf("adopted = %v, want %v", adopted, tt.wantAdopted)
			}
			if wantMissing := !tt.wantAdopted; (len(obs.MissingCaches) == 1) != wantMissing {
				t.Errorf("missing caches = %d, want missing=%v", len(obs.MissingCaches), wantMissing)
			}
		})
	}
}
