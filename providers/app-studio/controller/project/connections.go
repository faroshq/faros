/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

const (
	projectConnectionManagedByLabel           = "app-studio.faros.sh/managed-by"
	projectConnectionManagedByValue           = "project-connection"
	projectConnectionProjectLabel             = "faros.sh/project"
	projectConnectionProjectUIDLabel          = "faros.sh/project-uid"
	projectConnectionEnvironmentLabel         = "faros.sh/environment"
	projectConnectionLogicalNameLabel         = "app-studio.faros.sh/connection-name"
	projectDevelopmentServiceLogicalNameLabel = "app-studio.faros.sh/service-name"
)

var (
	projectConnectionGVK             = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "Connection"}
	projectConnectionListGVK         = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "ConnectionList"}
	projectDevelopmentServiceGVK     = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "DevelopmentService"}
	projectDevelopmentServiceListGVK = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "DevelopmentServiceList"}
)

func projectHasEnvironmentConnections(p *aiv1alpha1.Project) bool {
	if p == nil {
		return false
	}
	for _, env := range p.Spec.Environments {
		if len(env.Connections) > 0 {
			return true
		}
	}
	return false
}

func projectConnectionPhysicalName(p *aiv1alpha1.Project, environment, logicalName string) string {
	part := func(value string, max int) string {
		value = strings.Trim(strings.ToLower(value), "-")
		if len(value) > max {
			value = strings.TrimRight(value[:max], "-")
		}
		return value
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{p.Name, string(p.UID), environment, logicalName}, "\x00")))
	return fmt.Sprintf("pconn-%s-%s-%s-%s", part(p.Name, 18), part(environment, 12), part(logicalName, 16), hex.EncodeToString(sum[:6]))
}

func projectOwnedDevelopmentServices(ctx context.Context, c client.Client, p *aiv1alpha1.Project) (map[string]*unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(projectDevelopmentServiceListGVK)
	if err := c.List(ctx, list); err != nil {
		if apierrors.IsNotFound(err) {
			return map[string]*unstructured.Unstructured{}, nil
		}
		return nil, err
	}
	out := map[string]*unstructured.Unstructured{}
	for i := range list.Items {
		item := list.Items[i].DeepCopy()
		labels := item.GetLabels()
		if labels[projectConnectionProjectLabel] != p.Name || labels[projectConnectionProjectUIDLabel] != string(p.UID) || !projectDevelopmentServiceOwnerMatches(item, p) {
			continue
		}
		logicalName := strings.TrimSpace(labels[projectDevelopmentServiceLogicalNameLabel])
		if logicalName == "" {
			continue
		}
		out[logicalName] = item
	}
	return out, nil
}

func projectDevelopmentServiceOwnerMatches(service *unstructured.Unstructured, project *aiv1alpha1.Project) bool {
	if service == nil || project == nil || project.UID == "" {
		return false
	}
	for _, owner := range service.GetOwnerReferences() {
		if owner.APIVersion == aiv1alpha1.SchemeGroupVersion.String() && owner.Kind == "Project" && owner.Name == project.Name && owner.UID == project.UID && owner.Controller != nil && *owner.Controller {
			return true
		}
	}
	return false
}

func reconcileProjectConnections(ctx context.Context, c client.Client, p *aiv1alpha1.Project, instances map[string]map[string]*unstructured.Unstructured) (map[string][]aiv1alpha1.ProjectEnvironmentConnectionStatus, bool, error) {
	statuses := map[string][]aiv1alpha1.ProjectEnvironmentConnectionStatus{}
	services, err := projectOwnedDevelopmentServices(ctx, c, p)
	if err != nil {
		return nil, false, fmt.Errorf("list DevelopmentServices: %w", err)
	}
	desiredNames := map[string]struct{}{}
	serviceRefs := map[string][]string{}
	needsRetry := false

	for _, env := range p.Spec.Environments {
		for _, intent := range env.Connections {
			name := projectConnectionPhysicalName(p, env.Name, intent.Name)
			desiredNames[name] = struct{}{}
			status := aiv1alpha1.ProjectEnvironmentConnectionStatus{Name: intent.Name, Phase: "Pending", Reason: "ResolvingEndpoints", Message: "waiting for connection endpoints"}
			source, sourceReason := projectConnectionBindingInstance(p, env, intent.SourceRef, instances[env.Name])
			if sourceReason != "" {
				status.Reason, status.Message = sourceReason, projectConnectionReasonMessage(sourceReason, intent.SourceRef.Name)
				status.Phase = projectConnectionResolutionPhase(sourceReason)
				statuses[env.Name] = append(statuses[env.Name], status)
				needsRetry = needsRetry || status.Phase == "Pending"
				continue
			}
			target, targetKind, targetReason := projectConnectionTarget(p, env, intent.TargetRef, instances[env.Name], services)
			if targetReason != "" {
				status.Reason, status.Message = targetReason, projectConnectionReasonMessage(targetReason, intent.TargetRef.Name)
				status.Phase = projectConnectionResolutionPhase(targetReason)
				statuses[env.Name] = append(statuses[env.Name], status)
				needsRetry = needsRetry || status.Phase == "Pending"
				continue
			}
			want := desiredProjectConnection(p, env.Name, intent, name, source, target, targetKind)
			got := &unstructured.Unstructured{}
			got.SetGroupVersionKind(projectConnectionGVK)
			err := c.Get(ctx, types.NamespacedName{Name: name}, got)
			switch {
			case apierrors.IsNotFound(err):
				if err := c.Create(ctx, want); err != nil && !apierrors.IsAlreadyExists(err) {
					return nil, false, fmt.Errorf("create Connection %q: %w", name, err)
				}
				status.Reason = "Creating"
				status.Message = "creating the runtime connection"
				needsRetry = true
			case err != nil:
				return nil, false, fmt.Errorf("get Connection %q: %w", name, err)
			case !got.GetDeletionTimestamp().IsZero():
				status.Reason = "Replacing"
				status.Message = "waiting for the previous runtime connection to finish deleting"
				needsRetry = true
			case !projectConnectionSpecEqual(got, want):
				if err := c.Delete(ctx, got); err != nil && !apierrors.IsNotFound(err) {
					return nil, false, fmt.Errorf("replace immutable Connection %q: %w", name, err)
				}
				status.Reason = "Replacing"
				status.Message = "replacing the runtime connection after an endpoint identity or mapping change"
				needsRetry = true
			default:
				status = projectConnectionStatus(intent.Name, got)
				if status.Phase != "Ready" {
					needsRetry = true
				}
			}
			statuses[env.Name] = append(statuses[env.Name], status)
			if targetKind == "DevelopmentService" {
				serviceRefs[target.GetName()] = append(serviceRefs[target.GetName()], name)
			}
		}
	}

	stalePending, err := cleanupStaleProjectConnections(ctx, c, p, desiredNames)
	if err != nil {
		return nil, false, err
	}
	if stalePending {
		needsRetry = true
	}
	if err := reconcileDevelopmentServiceConnectionRefs(ctx, c, services, serviceRefs); err != nil {
		return nil, false, err
	}
	for env := range statuses {
		sort.Slice(statuses[env], func(i, j int) bool { return statuses[env][i].Name < statuses[env][j].Name })
	}
	return statuses, needsRetry, nil
}

func projectConnectionResolutionPhase(reason string) string {
	switch reason {
	case "InvalidSourceReference", "InvalidTargetReference", "InvalidBinding", "BindingNotFound":
		return "Failed"
	default:
		return "Pending"
	}
}

func projectConnectionBindingInstance(p *aiv1alpha1.Project, env aiv1alpha1.ProjectEnvironmentSpec, ref aiv1alpha1.ProjectConnectionEndpointReference, instances map[string]*unstructured.Unstructured) (*unstructured.Unstructured, string) {
	if ref.Kind != aiv1alpha1.ProjectConnectionReferenceBinding {
		return nil, "InvalidSourceReference"
	}
	for _, binding := range env.Bindings {
		if binding.Name != ref.Name {
			continue
		}
		if binding.Kind != aiv1alpha1.ProjectBindingKindProviderResource || binding.ResourceRef == nil || binding.ResourceRef.APIVersion != "infrastructure.faros.sh/v1alpha1" || binding.ResourceRef.Kind != "Instance" {
			return nil, "InvalidBinding"
		}
		if obj := instances[ref.Name]; obj != nil && obj.GetUID() != "" {
			return obj, ""
		}
		return nil, "SourcePending"
	}
	_ = p
	return nil, "BindingNotFound"
}

func projectConnectionTarget(p *aiv1alpha1.Project, env aiv1alpha1.ProjectEnvironmentSpec, ref aiv1alpha1.ProjectConnectionEndpointReference, instances map[string]*unstructured.Unstructured, services map[string]*unstructured.Unstructured) (*unstructured.Unstructured, string, string) {
	switch ref.Kind {
	case aiv1alpha1.ProjectConnectionReferenceDevelopmentService:
		if env.Name != projectDevelopmentEnvironmentName {
			return nil, "", "InvalidTargetReference"
		}
		if service := services[ref.Name]; service != nil && service.GetUID() != "" {
			return service, "DevelopmentService", ""
		}
		return nil, "", "TargetPending"
	case aiv1alpha1.ProjectConnectionReferenceBinding:
		obj, reason := projectConnectionBindingInstance(p, env, ref, instances)
		if reason != "" {
			return nil, "", reason
		}
		return obj, "Instance", ""
	default:
		return nil, "", "InvalidTargetReference"
	}
}

func projectConnectionReasonMessage(reason, ref string) string {
	switch reason {
	case "BindingNotFound":
		return fmt.Sprintf("binding %q does not exist in this environment", ref)
	case "InvalidBinding":
		return fmt.Sprintf("binding %q is not an Infrastructure Instance", ref)
	case "InvalidSourceReference", "InvalidTargetReference":
		return fmt.Sprintf("connection endpoint %q has an unsupported reference kind", ref)
	case "SourcePending":
		return fmt.Sprintf("waiting for source binding %q", ref)
	default:
		return fmt.Sprintf("waiting for target %q", ref)
	}
}

func desiredProjectConnection(p *aiv1alpha1.Project, environment string, intent aiv1alpha1.ProjectEnvironmentConnectionSpec, name string, source, target *unstructured.Unstructured, targetKind string) *unstructured.Unstructured {
	mappings := make([]any, 0, len(intent.Mappings))
	for _, mapping := range intent.Mappings {
		mappings = append(mappings, map[string]any{"sourceKey": mapping.SourceKey, "targetKey": mapping.TargetKey})
	}
	controller, block := true, true
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectConnectionGVK.GroupVersion().String(), "kind": projectConnectionGVK.Kind,
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"source": map[string]any{"instanceRef": map[string]any{"name": source.GetName(), "uid": string(source.GetUID())}, "interface": intent.SourceInterface},
			"target": map[string]any{"kind": targetKind, "name": target.GetName(), "uid": string(target.GetUID()), "interface": intent.TargetInterface},
		},
	}}
	if len(mappings) > 0 {
		_ = unstructured.SetNestedSlice(obj.Object, mappings, "spec", "mappings")
	}
	obj.SetLabels(map[string]string{
		projectConnectionManagedByLabel:   projectConnectionManagedByValue,
		projectConnectionProjectLabel:     p.Name,
		projectConnectionProjectUIDLabel:  string(p.UID),
		projectConnectionEnvironmentLabel: environment,
		projectConnectionLogicalNameLabel: intent.Name,
	})
	obj.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project", Name: p.Name, UID: p.UID, Controller: &controller, BlockOwnerDeletion: &block}})
	return obj
}

func projectConnectionSpecEqual(got, want *unstructured.Unstructured) bool {
	return equality.Semantic.DeepEqual(got.Object["spec"], want.Object["spec"])
}

func projectConnectionStatus(logicalName string, obj *unstructured.Unstructured) aiv1alpha1.ProjectEnvironmentConnectionStatus {
	status := aiv1alpha1.ProjectEnvironmentConnectionStatus{Name: logicalName, Phase: "Pending", Reason: "Reconciling", Message: "waiting for Infrastructure to reconcile the connection"}
	status.Revision, _, _ = unstructured.NestedString(obj.Object, "status", "revision")
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		status.Reason, _ = condition["reason"].(string)
		status.Message, _ = condition["message"].(string)
		switch condition["status"] {
		case "True":
			status.Phase = "Ready"
		case "False":
			if status.Reason == "InvalidConnection" {
				status.Phase = "Failed"
			}
		}
		break
	}
	return status
}

func cleanupStaleProjectConnections(ctx context.Context, c client.Client, p *aiv1alpha1.Project, desired map[string]struct{}) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(projectConnectionListGVK)
	if err := c.List(ctx, list); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("list Connections: %w", err)
	}
	pending := false
	for i := range list.Items {
		item := &list.Items[i]
		labels := item.GetLabels()
		if labels[projectConnectionManagedByLabel] != projectConnectionManagedByValue || labels[projectConnectionProjectLabel] != p.Name || labels[projectConnectionProjectUIDLabel] != string(p.UID) {
			continue
		}
		if _, ok := desired[item.GetName()]; ok {
			continue
		}
		pending = true
		if item.GetDeletionTimestamp().IsZero() {
			if err := c.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("delete stale Connection %q: %w", item.GetName(), err)
			}
		}
	}
	return pending, nil
}

func reconcileDevelopmentServiceConnectionRefs(ctx context.Context, c client.Client, services map[string]*unstructured.Unstructured, byPhysicalName map[string][]string) error {
	for _, service := range services {
		want := append([]string(nil), byPhysicalName[service.GetName()]...)
		sort.Strings(want)
		got, _, _ := unstructured.NestedStringSlice(service.Object, "spec", "connectionRefs")
		sort.Strings(got)
		if equality.Semantic.DeepEqual(got, want) {
			continue
		}
		next := service.DeepCopy()
		if len(want) == 0 {
			unstructured.RemoveNestedField(next.Object, "spec", "connectionRefs")
		} else if err := unstructured.SetNestedStringSlice(next.Object, want, "spec", "connectionRefs"); err != nil {
			return err
		}
		if err := c.Update(ctx, next); err != nil {
			return fmt.Errorf("update DevelopmentService %q connectionRefs: %w", service.GetName(), err)
		}
	}
	return nil
}

func mergeProjectConnectionStatuses(live []aiv1alpha1.ProjectEnvironmentStatus, specs []aiv1alpha1.ProjectEnvironmentSpec, existing []aiv1alpha1.ProjectEnvironmentStatus, statuses map[string][]aiv1alpha1.ProjectEnvironmentConnectionStatus) []aiv1alpha1.ProjectEnvironmentStatus {
	byName := map[string]int{}
	for i := range live {
		byName[live[i].Name] = i
	}
	existingConnections := map[string]bool{}
	for _, status := range existing {
		existingConnections[status.Name] = len(status.Connections) > 0
	}
	for _, spec := range specs {
		items, exists := statuses[spec.Name]
		if !exists && len(spec.Connections) == 0 && !existingConnections[spec.Name] {
			continue
		}
		i, ok := byName[spec.Name]
		if !ok {
			live = append(live, aiv1alpha1.ProjectEnvironmentStatus{Name: spec.Name, Mode: spec.Mode})
			i = len(live) - 1
			byName[spec.Name] = i
		}
		live[i].Connections = items
		for _, status := range items {
			if status.Phase == "Failed" {
				live[i].Phase = "Failed"
				break
			}
			if status.Phase != "Ready" && live[i].Phase != "Failed" {
				live[i].Phase = "Pending"
			}
		}
	}
	return live
}

func deleteProjectConnections(ctx context.Context, c client.Client, p *aiv1alpha1.Project) (bool, error) {
	return cleanupStaleProjectConnections(ctx, c, p, map[string]struct{}{})
}
