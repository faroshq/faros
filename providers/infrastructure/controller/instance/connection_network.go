// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package instance

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var connectionNetworkPolicyGVR = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
var connectionServiceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

type connectionNetworkRule struct {
	SourceSelector map[string]string
	Port           int32
}

func connectionNetworkEndpointAllowlisted(keys []string) bool {
	host := false
	port := false
	for _, key := range keys {
		switch key {
		case "host":
			host = true
		case "port":
			port = true
		}
	}
	return host && port
}

// connectionSourcePort reads the provider-owned, non-secret port field from
// the source Secret. Kubernetes Secret data is base64 encoded in the dynamic
// client representation. Sandboxes have default-deny egress, so a connected
// source without a usable port is rejected instead of advertising a Ready but
// unreachable connection.
func connectionSourcePort(data map[string]string, required bool) (int32, error) {
	raw := strings.TrimSpace(data["port"])
	if raw == "" {
		if required {
			return 0, fmt.Errorf("source Secret must contain an allowlisted port key for a sandbox target")
		}
		return 0, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("decode port: %w", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(decoded)))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be an integer between 1 and 65535")
	}
	return int32(port), nil
}

func resolveConnectionSourceEndpoint(ctx context.Context, runtime dynamic.Interface, namespace, sourceInstance string, data map[string]string, required bool) (map[string]string, int32, error) {
	port, err := connectionSourcePort(data, required)
	if err != nil || !required {
		return nil, port, err
	}
	host, err := decodeConnectionSecretField(data, "host")
	if err != nil {
		return nil, 0, err
	}
	parts := strings.Split(host, ".")
	serviceName := parts[0]
	if serviceName == "" || (len(parts) > 1 && (len(parts) < 3 || parts[1] != namespace || parts[2] != "svc")) {
		return nil, 0, fmt.Errorf("host %q is not a Service in runtime namespace %q", host, namespace)
	}
	service, err := runtime.Resource(connectionServiceGVR).Namespace(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("resolve Service %s/%s: %w", namespace, serviceName, err)
	}
	if service.GetLabels()["kro.run/instance-name"] != sourceInstance {
		return nil, 0, fmt.Errorf("Service %s/%s is not owned by source Instance %q", namespace, serviceName, sourceInstance)
	}
	selector, found, err := unstructured.NestedStringMap(service.Object, "spec", "selector")
	if err != nil || !found || len(selector) == 0 {
		return nil, 0, fmt.Errorf("Service %s/%s has no usable pod selector", namespace, serviceName)
	}
	servicePorts, _, _ := unstructured.NestedSlice(service.Object, "spec", "ports")
	portFound := false
	for _, item := range servicePorts {
		entry, _ := item.(map[string]any)
		candidate, _, _ := unstructured.NestedInt64(entry, "port")
		if candidate == int64(port) {
			portFound = true
			break
		}
	}
	if !portFound {
		return nil, 0, fmt.Errorf("Service %s/%s does not expose declared port %d", namespace, serviceName, port)
	}
	return selector, port, nil
}

func decodeConnectionSecretField(data map[string]string, key string) (string, error) {
	raw := strings.TrimSpace(data[key])
	if raw == "" {
		return "", fmt.Errorf("source Secret must contain an allowlisted %s key for a sandbox target", key)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", key, err)
	}
	value := strings.TrimSpace(string(decoded))
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
}

func aggregateConnectionNetworkPolicyName(aggregateName string) string {
	return aggregateName + "-egress"
}

func applyAggregateConnectionNetworkPolicy(ctx context.Context, runtime dynamic.Interface, aggregate *aggregateConnectionSecret) error {
	if aggregate == nil || aggregate.Namespace == "" || runtime == nil {
		return nil
	}
	policies := runtime.Resource(connectionNetworkPolicyGVR).Namespace(aggregate.Namespace)
	name := aggregateConnectionNetworkPolicyName(aggregate.Name)
	if aggregate.TargetSelector == nil || len(aggregate.NetworkRules) == 0 {
		existing, err := policies.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateAggregateNetworkPolicyOwnership(existing, aggregate.RuntimeIdentity); err != nil {
			return err
		}
		if err := policies.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	rules := append([]connectionNetworkRule(nil), aggregate.NetworkRules...)
	sort.Slice(rules, func(i, j int) bool {
		left := fmt.Sprintf("%v/%d", rules[i].SourceSelector, rules[i].Port)
		right := fmt.Sprintf("%v/%d", rules[j].SourceSelector, rules[j].Port)
		return left < right
	})
	egress := make([]any, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		key := fmt.Sprintf("%v/%d", rule.SourceSelector, rule.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		egress = append(egress, map[string]any{
			"to":    []any{map[string]any{"podSelector": map[string]any{"matchLabels": stringMapAny(rule.SourceSelector)}}},
			"ports": []any{map[string]any{"protocol": "TCP", "port": int64(rule.Port)}},
		})
	}
	want := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      name,
			"namespace": aggregate.Namespace,
			"labels": map[string]any{
				connectionManagedByLabel:     connectionManagedByValue,
				connectionTargetRuntimeLabel: shortHash(aggregate.RuntimeIdentity),
			},
			"annotations": map[string]any{connectionTargetRuntimeAnnotation: aggregate.RuntimeIdentity},
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": stringMapAny(aggregate.TargetSelector)},
			"policyTypes": []any{"Egress"},
			"egress":      egress,
		},
	}}
	existing, err := policies.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = policies.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if err := validateAggregateNetworkPolicyOwnership(existing, aggregate.RuntimeIdentity); err != nil {
		return err
	}
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	wantSpec, _, _ := unstructured.NestedMap(want.Object, "spec")
	if reflect.DeepEqual(existingSpec, wantSpec) {
		return nil
	}
	want.SetResourceVersion(existing.GetResourceVersion())
	_, err = policies.Update(ctx, want, metav1.UpdateOptions{})
	return err
}

func validateAggregateNetworkPolicyOwnership(policy *unstructured.Unstructured, runtimeIdentity string) error {
	if policy == nil {
		return fmt.Errorf("NetworkPolicy is required")
	}
	labels := policy.GetLabels()
	annotations := policy.GetAnnotations()
	if labels[connectionManagedByLabel] != connectionManagedByValue ||
		labels[connectionTargetRuntimeLabel] != shortHash(runtimeIdentity) ||
		annotations[connectionTargetRuntimeAnnotation] != runtimeIdentity {
		return fmt.Errorf("NetworkPolicy %s/%s already exists and is not owned by this connection target", policy.GetNamespace(), policy.GetName())
	}
	return nil
}

func deleteAggregateConnectionNetworkPolicy(ctx context.Context, runtime dynamic.Interface, namespace, aggregateName, runtimeIdentity string) error {
	if runtime == nil || namespace == "" || aggregateName == "" || runtimeIdentity == "" {
		return nil
	}
	policies := runtime.Resource(connectionNetworkPolicyGVR).Namespace(namespace)
	name := aggregateConnectionNetworkPolicyName(aggregateName)
	existing, err := policies.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateAggregateNetworkPolicyOwnership(existing, runtimeIdentity); err != nil {
		return err
	}
	if err := policies.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
