/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package application

// Access-token bridge: gives an instance the SAME credential a caller outside
// the runtime cluster already holds.
//
// Templates that gate themselves (searxng, browser) need a shared secret with
// their caller — usually the agents provider, which is not a pod in the
// instance's namespace and so cannot mount the instance's generated Secret.
// Generating the token in-graph is a dead end for those: nothing surfaces a
// generated value to a human or to another service ("Secret values are not
// surfaced in status" — every template says so, deliberately).
//
// So the tenant owns the token instead. They already keep one: the agents
// provider stores a Connection's credential as a Secret in the tenant
// workspace. The instance names that Secret in spec.tokenSecretRef, and this
// bridge copies it into the runtime namespace where the instance's auth gate
// mounts it. One credential, authored once, never duplicated into a spec and
// never decoded here — the base64 payload is passed through verbatim.
//
// This mirrors bridgeRegistryPullSecret (tenant workspace → runtime namespace,
// same lifecycle, same cleanup obligation); only the shape differs.

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/faroshq/provider-infrastructure/kro"
)

const (
	// tokenSecretKey is the key read from the tenant Secret and written to the
	// bridged one. It matches the key the agents provider stores a Connection
	// credential under, so an agents Connection Secret can be referenced as-is.
	tokenSecretKey = "token"

	// tokenSpecPath is where an instance names the tenant Secret holding its
	// access token. Templates that gate themselves declare it in their schema.
	tokenSpecField = "tokenSecretRef"

	// tokenSpecNameField is platform-stamped, not tenant-authored: the name the
	// bridged Secret takes in the runtime namespace. The RGD can't compute it —
	// it is keyed on the instance name, while a graph only sees spec.name, an
	// independent input — so the controller stamps it and the graph's auth gate
	// reads it back. Same indirection as credentialsSecretName.
	tokenSpecNameField = "tokenSecretName"
)

// bridgedTokenSecretName is the name the bridged Secret takes in the runtime
// namespace. Deterministic per instance so a template's pod spec can reference
// it without knowing what the tenant called the source.
func bridgedTokenSecretName(instance string) string { return instance + "-access" }

// bridgeAccessToken copies the tenant Secret named by spec.tokenSecretRef into
// the instance's runtime namespace as "<instance>-access".
//
// No ref is not an error: a template may legitimately run ungated, or generate
// its own token for in-cluster consumers only. A named-but-missing Secret IS an
// error — silently starting an ungated instance because a typo'd Secret name
// didn't resolve is the failure mode worth being loud about.
func (c *Controller) bridgeAccessToken(ctx context.Context, tenantClient client.Client, tenant, srcNamespace string, app *unstructured.Unstructured) error {
	ref := nestedString(app, "spec", tokenSpecField)
	if ref == "" {
		return nil
	}

	src := &unstructured.Unstructured{}
	src.SetGroupVersionKind(secretGVK)
	key := types.NamespacedName{Namespace: c.cfg.CredentialsNamespace, Name: ref}
	if err := tenantClient.Get(ctx, key, src); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("tenant Secret %s/%s not found — spec.%s names the Secret holding this instance's access token (an agents Connection Secret, e.g. kedge-agents-conn-<name>)",
				c.cfg.CredentialsNamespace, ref, tokenSpecField)
		}
		return fmt.Errorf("reading tenant token secret: %w", err)
	}
	// Secret .data is base64 over the wire; pass it through verbatim so the
	// credential is never decoded into memory as plaintext.
	data, _, _ := unstructured.NestedStringMap(src.Object, "data")
	encoded, ok := data[tokenSecretKey]
	if !ok || encoded == "" {
		return fmt.Errorf("tenant Secret %s/%s has no %q key", c.cfg.CredentialsNamespace, ref, tokenSecretKey)
	}

	ns := kro.RuntimeNamespace(tenant, srcNamespace)
	return c.writeRuntimeTokenSecret(ctx, ns, bridgedTokenSecretName(app.GetName()), encoded)
}

// writeRuntimeTokenSecret create-or-updates the bridged Secret. Update (not
// patch) so a rotated token in the tenant workspace replaces the runtime copy
// on the next reconcile rather than accumulating stale keys.
func (c *Controller) writeRuntimeTokenSecret(ctx context.Context, ns, secretName, encodedToken string) error {
	if err := c.ensureNamespace(ctx, ns); err != nil {
		return err
	}
	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": ns,
			"labels":    map[string]any{kro.LabelManagedBy: kro.ManagedByValue},
		},
		"type": "Opaque",
		"data": map[string]any{tokenSecretKey: encodedToken},
	}}
	existing, err := c.cfg.Runtime.Resource(secretGVR).Namespace(ns).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := c.cfg.Runtime.Resource(secretGVR).Namespace(ns).Create(ctx, desired, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create runtime token secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get runtime token secret: %w", err)
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	if _, err := c.cfg.Runtime.Resource(secretGVR).Namespace(ns).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update runtime token secret: %w", err)
	}
	return nil
}

// cleanupAccessToken removes the bridged Secret when the instance goes away.
// It lives outside the instance's cluster, so ownerRef GC cannot reap it.
// NotFound is success — the namespace may already be gone.
func (c *Controller) cleanupAccessToken(ctx context.Context, tenant, srcNamespace, name string) error {
	ns := kro.RuntimeNamespace(tenant, srcNamespace)
	err := c.cfg.Runtime.Resource(secretGVR).Namespace(ns).Delete(ctx, bridgedTokenSecretName(name), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete runtime token secret: %w", err)
	}
	return nil
}
