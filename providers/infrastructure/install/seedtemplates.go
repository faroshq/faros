/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package install

// Seed Templates baked into the binary. Without these the catalog is
// empty on a fresh workspace, the portal renders "No templates match
// the current filters", and tenants have nothing to APIBind against
// to demonstrate the platform end-to-end.
//
// The set lives under install/templates/*.yaml and is embedded at
// build time so init does not depend on a host kubectl + path. The
// caller (init_cmd.go) invokes SeedTemplates after CRDs +
// PlatformSchemaInAPIExport + PlatformCachedResources so the
// Template CRD exists by the time we POST.
//
// Operators who maintain their own catalog can disable seeding via
// INFRASTRUCTURE_SKIP_SEED_TEMPLATES=1 — useful for production
// clusters where the catalog is managed by GitOps and the dev seed
// would only add noise.

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

//go:embed templates/*.yaml
var seedTemplatesFS embed.FS

// templateGVR is what the dynamic client writes against to upsert
// Template CRs. Matches the CRD installed by install.CRDs.
var templateGVR = schema.GroupVersionResource{
	Group:    infrav1alpha1.GroupName,
	Version:  infrav1alpha1.Version,
	Resource: "templates",
}

// SeedTemplateRequirement identifies one embedded Template that must be
// current and backend-ready before the provider can report ready.
type SeedTemplateRequirement struct {
	Name string
}

// RequiredSeedTemplateResources returns every Template embedded in this binary.
// The name is retained for callers, but flattened Instances removed the old
// per-template API resource contract. Custom Templates reconcile independently
// and do not gate provider readiness. Every embedded Template must have a
// registered backend; otherwise startup fails rather than advertise an unusable
// built-in catalog.
func RequiredSeedTemplateResources(registeredBackends []string) ([]SeedTemplateRequirement, error) {
	entries, err := fs.ReadDir(seedTemplatesFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("read embedded templates/: %w", err)
	}
	backends := make(map[string]struct{}, len(registeredBackends))
	for _, backend := range registeredBackends {
		backends[backend] = struct{}{}
	}
	required := map[string]struct{}{}
	missingBackends := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := fs.ReadFile(seedTemplatesFS, "templates/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded templates/%s: %w", e.Name(), err)
		}
		var tmpl infrav1alpha1.Template
		if err := utilyaml.UnmarshalStrict(raw, &tmpl); err != nil {
			return nil, fmt.Errorf("decode embedded template %s: %w", e.Name(), err)
		}
		if strings.TrimSpace(tmpl.Name) == "" {
			return nil, fmt.Errorf("embedded template %s has no metadata.name", e.Name())
		}
		if _, registered := backends[tmpl.Spec.Backend]; !registered {
			missingBackends[tmpl.Spec.Backend] = struct{}{}
		}
		if _, duplicate := required[tmpl.Name]; duplicate {
			return nil, fmt.Errorf("duplicate embedded template %q", tmpl.Name)
		}
		required[tmpl.Name] = struct{}{}
	}
	if len(missingBackends) > 0 {
		missing := make([]string, 0, len(missingBackends))
		for backend := range missingBackends {
			missing = append(missing, backend)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("embedded seed templates require unavailable backends: %s", strings.Join(missing, ", "))
	}

	result := make([]SeedTemplateRequirement, 0, len(required))
	for name := range required {
		result = append(result, SeedTemplateRequirement{Name: name})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// SeedTemplatesReady reports whether every embedded seed Template
// is current and backend-ready. The one stable Instance API is installed during
// provider bootstrap; Template readiness proves each built-in product has a
// live backend graph before the provider advertises readiness.
func SeedTemplatesReady(ctx context.Context, dyn dynamic.Interface, required []SeedTemplateRequirement) (bool, error) {
	if len(required) == 0 {
		return true, nil
	}
	for _, requirement := range required {
		ready, err := seedTemplateBackendReady(ctx, dyn, requirement)
		if err != nil {
			return false, err
		}
		if !ready {
			return false, nil
		}
	}
	return true, nil
}

func seedTemplateBackendReady(ctx context.Context, dyn dynamic.Interface, requirement SeedTemplateRequirement) (bool, error) {
	obj, err := dyn.Resource(templateGVR).Get(ctx, requirement.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get seed Template %q: %w", requirement.Name, err)
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return false, fmt.Errorf("marshal seed Template %q: %w", requirement.Name, err)
	}
	var tmpl infrav1alpha1.Template
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return false, fmt.Errorf("decode seed Template %q: %w", requirement.Name, err)
	}
	if tmpl.Status.ObservedGeneration != tmpl.Generation || !tmpl.Status.Backend.Ready {
		return false, nil
	}
	return currentTrueCondition(tmpl.Status.Conditions, infrav1alpha1.ConditionBackendReady, tmpl.Generation) &&
		currentTrueCondition(tmpl.Status.Conditions, infrav1alpha1.ConditionReady, tmpl.Generation), nil
}

func currentTrueCondition(conditions []metav1.Condition, conditionType string, generation int64) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == generation
		}
	}
	return false
}

// SeedTemplates upserts every Template YAML baked into install/templates/
// into the workspace the supplied rest.Config points at. Idempotent —
// existing Templates are patched in place, ResourceVersion preserved.
//
// Callers skip this function when INFRASTRUCTURE_SKIP_SEED_TEMPLATES is set to
// any non-empty value. When seeding is enabled, errors are fatal to bootstrap:
// reporting the provider ready with an incomplete embedded catalog would leave
// its required instance APIs unavailable.
func SeedTemplates(ctx context.Context, config *rest.Config) error {
	log := klog.FromContext(ctx).WithName("install.seedtemplates")

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("dynamic client for seed Templates: %w", err)
	}

	entries, err := fs.ReadDir(seedTemplatesFS, "templates")
	if err != nil {
		return fmt.Errorf("read embedded templates/: %w", err)
	}

	var applied int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := fs.ReadFile(seedTemplatesFS, "templates/"+e.Name())
		if err != nil {
			return fmt.Errorf("read embedded templates/%s: %w", e.Name(), err)
		}
		if err := applyTemplate(ctx, dyn, raw); err != nil {
			return fmt.Errorf("apply %s: %w", e.Name(), err)
		}
		applied++
	}
	log.Info("seeded Templates", "count", applied)
	return nil
}

// applyTemplate CREATEs or UPDATEs a single Template from raw YAML.
// Parsed through utilyaml (handles document separators + JSON-tagged
// fields uniformly) and re-serialized through encoding/json so the
// dynamic client gets a clean map[string]any with no leftover YAML
// node metadata.
func applyTemplate(ctx context.Context, dyn dynamic.Interface, raw []byte) error {
	// utilyaml.Unmarshal happily decodes YAML into a struct, but we
	// don't have a typed scheme registered here — the dynamic client
	// path stays JSON-only. Go via map[string]any to avoid pulling in
	// the apis package's runtime.Scheme just for this upsert.
	var obj map[string]any
	if err := utilyaml.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("unmarshal YAML: %w", err)
	}
	if obj == nil {
		return fmt.Errorf("empty Template document")
	}
	name, _, _ := unstructured.NestedString(obj, "metadata", "name")
	if name == "" {
		return fmt.Errorf("Template missing metadata.name")
	}

	// Round-trip through JSON so any numeric / bool YAML scalars land
	// as the typed Go values the apiserver expects under
	// spec.schema and spec.backendConfig (both are
	// XPreserveUnknownFields, so they survive verbatim).
	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal YAML→JSON: %w", err)
	}
	u := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, &u.Object); err != nil {
		return fmt.Errorf("unmarshal JSON→Unstructured: %w", err)
	}

	existing, err := dyn.Resource(templateGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get existing Template: %w", err)
	}
	if apierrors.IsNotFound(err) {
		_, err = dyn.Resource(templateGVR).Create(ctx, u, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create Template: %w", err)
		}
		return nil
	}

	// Preserve the apiserver-supplied ResourceVersion so the update
	// is a proper compare-and-set rather than a blind overwrite that
	// would race against the Template controller's status patches.
	u.SetResourceVersion(existing.GetResourceVersion())
	// Don't overwrite the apiserver-assigned UID or status either —
	// status lives on the existing object's tree, and our seed YAML
	// has no status section anyway.
	if status, ok, _ := unstructured.NestedMap(existing.Object, "status"); ok {
		_ = unstructured.SetNestedMap(u.Object, status, "status")
	}
	_, err = dyn.Resource(templateGVR).Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update Template: %w", err)
	}
	return nil
}
