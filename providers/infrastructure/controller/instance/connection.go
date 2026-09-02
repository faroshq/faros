// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package instance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
	"github.com/faroshq/provider-infrastructure/kro"
)

var connectionSecretGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

const (
	connectionRequeue                  = 10 * time.Second
	connectionManagedByLabel           = "faros.sh/managed-by"
	connectionManagedByValue           = "infrastructure-connection"
	connectionTargetRuntimeLabel       = "faros.sh/connection-target-runtime"
	connectionTargetRuntimeAnnotation  = "faros.sh/connection-target-runtime-identity"
	connectionRevisionAnnotation       = "faros.sh/connection-revision"
	connectionEntryRevisionsAnnotation = "faros.sh/connection-entry-revisions"
	connectionInventoryAnnotation      = "faros.sh/connection-inventory"
	connectionMountPath                = "/var/run/faros/connections"
)

type connectionReconciler struct{ c *Controller }

type resolvedConnection struct {
	connection       *infrav1alpha1.Connection
	runtimeNamespace string
	runtimeIdentity  string
	aggregateName    string
	targetSelector   map[string]string
	sourceSelector   map[string]string
	sourcePort       int32
	targetKind       string
	targetName       string
	targetUID        string
	sourceSecretUID  string
	sourceVersion    string
	sourceData       map[string]string
	mappings         []infrav1alpha1.ConnectionMapping
}

type aggregateConnectionSecret struct {
	Name                string
	Namespace           string
	RuntimeIdentity     string
	TargetSelector      map[string]string
	NetworkRules        []connectionNetworkRule
	Data                map[string]string
	Inventory           map[string][]string
	Revision            string
	ConnectionRevisions map[string]string
}

func (r *connectionReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	cluster := string(req.ClusterName)
	cl, err := r.c.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting workspace cluster %s: %w", cluster, err)
	}
	tenantClient := cl.GetClient()
	connection := &infrav1alpha1.Connection{}
	if err := tenantClient.Get(ctx, req.NamespacedName, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !connection.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, tenantClient, cluster, connection)
	}
	if !controllerutil.ContainsFinalizer(connection, infrav1alpha1.ConnectionFinalizer) {
		controllerutil.AddFinalizer(connection, infrav1alpha1.ConnectionFinalizer)
		if err := tenantClient.Update(ctx, connection); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	resolved, err := r.c.resolveConnection(ctx, tenantClient, cluster, connection)
	if err != nil {
		withdrawErr := r.c.withdrawConnectionFromAggregate(ctx, connection)
		message := err.Error()
		statusAggregate := (*aggregateConnectionSecret)(nil)
		if withdrawErr != nil {
			message = fmt.Sprintf("%s; previous credentials withdrawal failed: %v", message, withdrawErr)
			// Keep the managed reference only while cleanup is pending so the
			// next reconcile can retry the exact aggregate withdrawal. A
			// successful withdrawal below clears it from status entirely.
			statusAggregate = aggregateReferenceFromConnectionStatus(connection)
		}
		statusErr := r.c.persistConnectionStatus(ctx, tenantClient, connection, statusAggregate, "InvalidConnection", message)
		if withdrawErr != nil {
			if statusErr != nil {
				return ctrl.Result{RequeueAfter: connectionRequeue}, fmt.Errorf("persist invalid Connection status: %v; withdraw previous credentials: %w", statusErr, withdrawErr)
			}
			return ctrl.Result{RequeueAfter: connectionRequeue}, withdrawErr
		}
		return ctrl.Result{RequeueAfter: connectionRequeue}, statusErr
	}
	aggregate, err := r.c.buildAggregateConnectionSecret(ctx, tenantClient, cluster, resolved.runtimeIdentity, "")
	if err != nil {
		return ctrl.Result{RequeueAfter: connectionRequeue}, r.c.persistConnectionStatus(ctx, tenantClient, connection, nil, "AggregateUnavailable", err.Error())
	}
	if err := applyAggregateConnectionSecret(ctx, r.c.cfg.Runtime, aggregate); err != nil {
		return ctrl.Result{RequeueAfter: connectionRequeue}, r.c.persistConnectionStatus(ctx, tenantClient, connection, nil, "SecretSyncFailed", err.Error())
	}
	if err := applyAggregateConnectionNetworkPolicy(ctx, r.c.cfg.Runtime, aggregate); err != nil {
		return ctrl.Result{RequeueAfter: connectionRequeue}, r.c.persistConnectionStatus(ctx, tenantClient, connection, nil, "NetworkPolicySyncFailed", err.Error())
	}
	return ctrl.Result{RequeueAfter: connectionRequeue}, r.c.persistConnectionStatus(ctx, tenantClient, connection, aggregate, "Ready", "connection credentials and network access are synchronized")
}

func (r *connectionReconciler) finalize(ctx context.Context, tenantClient client.Client, cluster string, connection *infrav1alpha1.Connection) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(connection, infrav1alpha1.ConnectionFinalizer) {
		return ctrl.Result{}, nil
	}
	if resolved, err := r.c.resolveConnection(ctx, tenantClient, cluster, connection); err == nil {
		aggregate, buildErr := r.c.buildAggregateConnectionSecret(ctx, tenantClient, cluster, resolved.runtimeIdentity, string(connection.UID))
		if buildErr != nil {
			if connection.Status.ManagedSecretRef == nil {
				return ctrl.Result{RequeueAfter: connectionRequeue}, buildErr
			}
			if err := removeConnectionFromAggregate(ctx, r.c.cfg.Runtime, connection.Status.ManagedSecretRef.Namespace, connection.Status.ManagedSecretRef.Name, connection.Status.ManagedSecretRef.TargetRuntimeIdentity, string(connection.UID)); err != nil {
				return ctrl.Result{RequeueAfter: connectionRequeue}, err
			}
			aggregate = nil
		}
		if aggregate != nil && aggregate.Namespace == "" {
			aggregate.Namespace = resolved.runtimeNamespace
		}
		if aggregate != nil {
			if err := applyAggregateConnectionSecret(ctx, r.c.cfg.Runtime, aggregate); err != nil {
				return ctrl.Result{RequeueAfter: connectionRequeue}, err
			}
			if err := applyAggregateConnectionNetworkPolicy(ctx, r.c.cfg.Runtime, aggregate); err != nil {
				return ctrl.Result{RequeueAfter: connectionRequeue}, err
			}
		}
	} else if connection.Status.ManagedSecretRef != nil {
		if err := removeConnectionFromAggregate(ctx, r.c.cfg.Runtime, connection.Status.ManagedSecretRef.Namespace, connection.Status.ManagedSecretRef.Name, connection.Status.ManagedSecretRef.TargetRuntimeIdentity, string(connection.UID)); err != nil {
			return ctrl.Result{RequeueAfter: connectionRequeue}, err
		}
	}
	controllerutil.RemoveFinalizer(connection, infrav1alpha1.ConnectionFinalizer)
	if err := tenantClient.Update(ctx, connection); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (c *Controller) resolveConnection(ctx context.Context, tenantClient client.Client, tenant string, connection *infrav1alpha1.Connection) (*resolvedConnection, error) {
	if connection == nil {
		return nil, fmt.Errorf("connection is required")
	}
	if strings.TrimSpace(connection.Spec.Source.InstanceRef.Name) == "" {
		return nil, fmt.Errorf("source Instance name is required")
	}
	if strings.TrimSpace(connection.Spec.Source.InstanceRef.UID) == "" {
		return nil, fmt.Errorf("source Instance UID is required")
	}
	if strings.TrimSpace(connection.Spec.Target.Name) == "" {
		return nil, fmt.Errorf("target %s name is required", connection.Spec.Target.Kind)
	}
	if strings.TrimSpace(connection.Spec.Target.UID) == "" {
		return nil, fmt.Errorf("target %s UID is required", connection.Spec.Target.Kind)
	}
	source := &unstructured.Unstructured{}
	source.SetGroupVersionKind(instanceGVK)
	if err := tenantClient.Get(ctx, client.ObjectKey{Name: connection.Spec.Source.InstanceRef.Name}, source); err != nil {
		return nil, fmt.Errorf("source Instance %q: %w", connection.Spec.Source.InstanceRef.Name, err)
	}
	if strings.TrimSpace(string(source.GetUID())) == "" || string(source.GetUID()) != connection.Spec.Source.InstanceRef.UID {
		return nil, fmt.Errorf("source Instance %q UID mismatch", source.GetName())
	}
	sourceTemplateName, _, _ := unstructured.NestedString(source.Object, "spec", "template")
	sourceTemplate, _, err := c.resolveTemplate(ctx, sourceTemplateName)
	if err != nil {
		return nil, fmt.Errorf("source Template %q: %w", sourceTemplateName, err)
	}
	provided, ok := providedConnectionInterface(sourceTemplate, connection.Spec.Source.Interface)
	if !ok {
		return nil, fmt.Errorf("source Template %q does not provide interface %q", sourceTemplateName, connection.Spec.Source.Interface)
	}

	targetTemplate, runtimeNamespace, runtimeIdentity, targetUID, targetSelector, err := c.resolveConnectionTarget(ctx, tenantClient, tenant, connection)
	if err != nil {
		return nil, err
	}
	consumed, ok := consumedConnectionInterface(targetTemplate, connection.Spec.Target.Interface)
	if !ok {
		return nil, fmt.Errorf("target Template %q does not consume interface %q", targetTemplate.Name, connection.Spec.Target.Interface)
	}
	if provided.Type != consumed.Type {
		return nil, fmt.Errorf("source interface type %q is incompatible with target type %q", provided.Type, consumed.Type)
	}
	mappings, err := validateConnectionMappings(connection.Spec.Mappings, provided, consumed)
	if err != nil {
		return nil, err
	}

	sourceRuntimeNamespace := instanceRuntimeNamespace(tenant, source)
	if sourceRuntimeNamespace != runtimeNamespace {
		return nil, fmt.Errorf("source runtime namespace %q does not match target runtime namespace %q", sourceRuntimeNamespace, runtimeNamespace)
	}
	secretName, secretNamespace, err := connectionSecretReference(source, provided.SecretRefPath)
	if err != nil {
		return nil, err
	}
	if secretNamespace == "" {
		secretNamespace = sourceRuntimeNamespace
	}
	if secretNamespace != runtimeNamespace {
		return nil, fmt.Errorf("source Secret namespace %q does not match target runtime namespace %q", secretNamespace, runtimeNamespace)
	}
	secret, err := c.cfg.Runtime.Resource(connectionSecretGVR).Namespace(secretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("source Secret %s/%s: %w", secretNamespace, secretName, err)
	}
	data, _, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil {
		return nil, fmt.Errorf("source Secret %s/%s data: %w", secretNamespace, secretName, err)
	}
	for _, mapping := range mappings {
		if _, ok := data[mapping.SourceKey]; !ok {
			return nil, fmt.Errorf("source Secret %s/%s does not contain allowlisted key %q", secretNamespace, secretName, mapping.SourceKey)
		}
	}
	if targetSelector != nil && !connectionNetworkEndpointAllowlisted(provided.Keys) {
		return nil, fmt.Errorf("source interface %q must allowlist host and port for a sandbox target", connection.Spec.Source.Interface)
	}
	sourceSelector, sourcePort, err := resolveConnectionSourceEndpoint(ctx, c.cfg.Runtime, runtimeNamespace, source.GetName(), data, targetSelector != nil)
	if err != nil {
		return nil, fmt.Errorf("source interface %q network endpoint: %w", connection.Spec.Source.Interface, err)
	}
	return &resolvedConnection{
		connection: connection, runtimeNamespace: runtimeNamespace, runtimeIdentity: runtimeIdentity,
		aggregateName: aggregateConnectionSecretName(runtimeIdentity), targetKind: connection.Spec.Target.Kind,
		targetSelector: targetSelector, sourceSelector: sourceSelector, sourcePort: sourcePort,
		targetName: connection.Spec.Target.Name, targetUID: targetUID, sourceSecretUID: string(secret.GetUID()),
		sourceVersion: secret.GetResourceVersion(), sourceData: data, mappings: mappings,
	}, nil
}

func (c *Controller) resolveConnectionTarget(ctx context.Context, tenantClient client.Client, tenant string, connection *infrav1alpha1.Connection) (*infrav1alpha1.Template, string, string, string, map[string]string, error) {
	switch connection.Spec.Target.Kind {
	case infrav1alpha1.ConnectionTargetInstance:
		target := &unstructured.Unstructured{}
		target.SetGroupVersionKind(instanceGVK)
		if err := tenantClient.Get(ctx, client.ObjectKey{Name: connection.Spec.Target.Name}, target); err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target Instance %q: %w", connection.Spec.Target.Name, err)
		}
		if strings.TrimSpace(string(target.GetUID())) == "" || string(target.GetUID()) != connection.Spec.Target.UID {
			return nil, "", "", "", nil, fmt.Errorf("target Instance %q UID mismatch", target.GetName())
		}
		templateName, _, _ := unstructured.NestedString(target.Object, "spec", "template")
		tmpl, _, err := c.resolveTemplate(ctx, templateName)
		if err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target Template %q: %w", templateName, err)
		}
		ns := instanceRuntimeNamespace(tenant, target)
		identity := "instance/" + string(target.GetUID())
		var selector map[string]string
		if templateName == infrav1alpha1.UniversalCodingSandboxTemplateName {
			selector = map[string]string{"app": target.GetName()}
		}
		return tmpl, ns, identity, string(target.GetUID()), selector, nil
	case infrav1alpha1.ConnectionTargetService:
		service := &infrav1alpha1.DevelopmentService{}
		if err := tenantClient.Get(ctx, client.ObjectKey{Name: connection.Spec.Target.Name}, service); err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target DevelopmentService %q: %w", connection.Spec.Target.Name, err)
		}
		if err := validateDevelopmentServiceReferences(service.Spec); err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target DevelopmentService %q: %w", service.Name, err)
		}
		if strings.TrimSpace(string(service.UID)) == "" || string(service.UID) != connection.Spec.Target.UID {
			return nil, "", "", "", nil, fmt.Errorf("target DevelopmentService %q UID mismatch", service.Name)
		}
		sandbox := &unstructured.Unstructured{}
		sandbox.SetGroupVersionKind(instanceGVK)
		if err := tenantClient.Get(ctx, client.ObjectKey{Name: service.Spec.SandboxRef.Name}, sandbox); err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target sandbox Instance %q: %w", service.Spec.SandboxRef.Name, err)
		}
		if strings.TrimSpace(string(sandbox.GetUID())) == "" || string(sandbox.GetUID()) != service.Spec.SandboxRef.UID {
			return nil, "", "", "", nil, fmt.Errorf("target sandbox Instance %q UID mismatch", sandbox.GetName())
		}
		templateName, _, _ := unstructured.NestedString(sandbox.Object, "spec", "template")
		tmpl, _, err := c.resolveTemplate(ctx, templateName)
		if err != nil {
			return nil, "", "", "", nil, fmt.Errorf("target sandbox Template %q: %w", templateName, err)
		}
		ns := instanceRuntimeNamespace(tenant, sandbox)
		identity := "sandbox/" + string(sandbox.GetUID())
		return tmpl, ns, identity, string(service.UID), map[string]string{"app": sandbox.GetName()}, nil
	default:
		return nil, "", "", "", nil, fmt.Errorf("target kind must be Instance or DevelopmentService")
	}
}

func (c *Controller) buildAggregateConnectionSecret(ctx context.Context, tenantClient client.Client, tenant, runtimeIdentity, excludeUID string) (*aggregateConnectionSecret, error) {
	list := &infrav1alpha1.ConnectionList{}
	if err := tenantClient.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list Connections: %w", err)
	}
	resolved := make([]*resolvedConnection, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		if !item.DeletionTimestamp.IsZero() || string(item.UID) == excludeUID {
			continue
		}
		_, _, candidateIdentity, _, _, targetErr := c.resolveConnectionTarget(ctx, tenantClient, tenant, item)
		entry, err := c.resolveConnection(ctx, tenantClient, tenant, item)
		if err != nil {
			// A broken binding to this exact runtime must not cause a partial
			// aggregate that silently removes its last-good credentials.
			if targetErr == nil && candidateIdentity == runtimeIdentity {
				return nil, fmt.Errorf("Connection %q is not ready: %w", item.Name, err)
			}
			continue
		}
		if entry.runtimeIdentity == runtimeIdentity {
			resolved = append(resolved, entry)
		}
	}
	return aggregateResolvedConnections(runtimeIdentity, resolved)
}

func aggregateResolvedConnections(runtimeIdentity string, resolved []*resolvedConnection) (*aggregateConnectionSecret, error) {
	aggregate := &aggregateConnectionSecret{
		Name: aggregateConnectionSecretName(runtimeIdentity), Data: map[string]string{}, Inventory: map[string][]string{},
		ConnectionRevisions: map[string]string{}, RuntimeIdentity: runtimeIdentity,
	}
	for _, item := range resolved {
		if aggregate.Namespace == "" {
			aggregate.Namespace = item.runtimeNamespace
		} else if aggregate.Namespace != item.runtimeNamespace {
			return nil, fmt.Errorf("runtime namespace confusion for aggregate %q", aggregate.Name)
		}
		uid := string(item.connection.UID)
		if item.targetSelector != nil {
			if aggregate.TargetSelector == nil {
				aggregate.TargetSelector = item.targetSelector
			} else if !reflect.DeepEqual(aggregate.TargetSelector, item.targetSelector) {
				return nil, fmt.Errorf("runtime target selector confusion for aggregate %q", aggregate.Name)
			}
			aggregate.NetworkRules = append(aggregate.NetworkRules, connectionNetworkRule{
				SourceSelector: item.sourceSelector,
				Port:           item.sourcePort,
			})
		}
		var keys []string
		for _, mapping := range item.mappings {
			key := mapping.TargetKey
			if item.targetKind == infrav1alpha1.ConnectionTargetService {
				key = connectionDataKey(uid, mapping.TargetKey)
			}
			if _, exists := aggregate.Data[key]; exists {
				return nil, fmt.Errorf("connection target key collision on %q", mapping.TargetKey)
			}
			aggregate.Data[key] = item.sourceData[mapping.SourceKey]
			keys = append(keys, key)
		}
		sort.Strings(keys)
		aggregate.Inventory[uid] = keys
		entryRevision := connectionRevision(item, keys)
		aggregate.ConnectionRevisions[uid] = entryRevision
	}
	aggregate.Revision = aggregateRevision(aggregate.ConnectionRevisions)
	return aggregate, nil
}

func applyAggregateConnectionSecret(ctx context.Context, runtime dynamic.Interface, aggregate *aggregateConnectionSecret) error {
	if aggregate == nil || aggregate.Namespace == "" || runtime == nil {
		return nil
	}
	secrets := runtime.Resource(connectionSecretGVR).Namespace(aggregate.Namespace)
	if len(aggregate.Data) == 0 {
		existing, err := secrets.Get(ctx, aggregate.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateAggregateSecretOwnership(existing, aggregate.RuntimeIdentity); err != nil {
			return err
		}
		if err := secrets.Delete(ctx, aggregate.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	inventory, _ := json.Marshal(aggregate.Inventory)
	entryRevisions, _ := json.Marshal(aggregate.ConnectionRevisions)
	want := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{
			"name": aggregate.Name, "namespace": aggregate.Namespace,
			"labels":      map[string]any{connectionManagedByLabel: connectionManagedByValue, connectionTargetRuntimeLabel: shortHash(aggregate.RuntimeIdentity)},
			"annotations": map[string]any{connectionRevisionAnnotation: aggregate.Revision, connectionInventoryAnnotation: string(inventory), connectionEntryRevisionsAnnotation: string(entryRevisions), connectionTargetRuntimeAnnotation: aggregate.RuntimeIdentity},
		},
		"type": "Opaque", "data": stringMapAny(aggregate.Data),
	}}
	existing, err := secrets.Get(ctx, aggregate.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, want, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if err := validateAggregateSecretOwnership(existing, aggregate.RuntimeIdentity); err != nil {
		return err
	}
	if aggregateSecretMatches(existing, aggregate, string(inventory), string(entryRevisions)) {
		return nil
	}
	want.SetResourceVersion(existing.GetResourceVersion())
	_, err = secrets.Update(ctx, want, metav1.UpdateOptions{})
	return err
}

func removeConnectionFromAggregate(ctx context.Context, runtime dynamic.Interface, namespace, name, runtimeIdentity, uid string) error {
	if runtime == nil || namespace == "" || name == "" {
		return nil
	}
	secrets := runtime.Resource(connectionSecretGVR).Namespace(namespace)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if runtimeIdentity == "" {
		return fmt.Errorf("managed aggregate Secret reference has no target runtime identity")
	}
	if err := validateAggregateSecretOwnership(secret, runtimeIdentity); err != nil {
		return err
	}
	var inventory map[string][]string
	if err := json.Unmarshal([]byte(secret.GetAnnotations()[connectionInventoryAnnotation]), &inventory); err != nil || inventory == nil {
		// The inventory is provider-owned bookkeeping. If it is missing or
		// corrupted, deleting the entire owned aggregate is the only safe way
		// to guarantee that the withdrawn connection's old credential cannot
		// remain reachable under an unknown key. Other valid connections will
		// be rebuilt on their next reconcile.
		return deleteOwnedAggregateSecret(ctx, secrets, secret, runtimeIdentity)
	}
	data, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	var entryRevisions map[string]string
	if err := json.Unmarshal([]byte(secret.GetAnnotations()[connectionEntryRevisionsAnnotation]), &entryRevisions); err != nil || entryRevisions == nil {
		return deleteOwnedAggregateSecret(ctx, secrets, secret, runtimeIdentity)
	}
	for _, key := range inventory[uid] {
		delete(data, key)
	}
	delete(inventory, uid)
	delete(entryRevisions, uid)
	if len(inventory) == 0 {
		err := secrets.Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	rawInventory, _ := json.Marshal(inventory)
	rawEntryRevisions, _ := json.Marshal(entryRevisions)
	secret.SetAnnotations(mergeStringMap(secret.GetAnnotations(), map[string]string{
		connectionInventoryAnnotation:      string(rawInventory),
		connectionEntryRevisionsAnnotation: string(rawEntryRevisions),
		connectionRevisionAnnotation:       aggregateRevision(entryRevisions),
	}))
	if err := unstructured.SetNestedStringMap(secret.Object, data, "data"); err != nil {
		return err
	}
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func deleteOwnedAggregateSecret(ctx context.Context, secrets dynamic.ResourceInterface, secret *unstructured.Unstructured, runtimeIdentity string) error {
	if err := validateAggregateSecretOwnership(secret, runtimeIdentity); err != nil {
		return err
	}
	if err := secrets.Delete(ctx, secret.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Controller) withdrawConnectionFromAggregate(ctx context.Context, connection *infrav1alpha1.Connection) error {
	if c == nil || connection == nil || connection.Status.ManagedSecretRef == nil {
		return nil
	}
	ref := connection.Status.ManagedSecretRef
	if err := removeConnectionFromAggregate(ctx, c.cfg.Runtime, ref.Namespace, ref.Name, ref.TargetRuntimeIdentity, string(connection.UID)); err != nil {
		return err
	}
	// If the binding is no longer valid, remove the aggregate policy rather
	// than leaving stale network authority behind. Other valid bindings for the
	// same runtime recreate the complete policy on their next reconciliation.
	return deleteAggregateConnectionNetworkPolicy(ctx, c.cfg.Runtime, ref.Namespace, ref.Name, ref.TargetRuntimeIdentity)
}

func aggregateReferenceFromConnectionStatus(connection *infrav1alpha1.Connection) *aggregateConnectionSecret {
	if connection == nil || connection.Status.ManagedSecretRef == nil {
		return nil
	}
	ref := connection.Status.ManagedSecretRef
	return &aggregateConnectionSecret{
		Name: ref.Name, Namespace: ref.Namespace, RuntimeIdentity: ref.TargetRuntimeIdentity,
		ConnectionRevisions: map[string]string{string(connection.UID): connection.Status.Revision},
	}
}

func (c *Controller) persistConnectionStatus(ctx context.Context, tenantClient client.Client, connection *infrav1alpha1.Connection, aggregate *aggregateConnectionSecret, reason, message string) error {
	previous := connection.Status.DeepCopy()
	status := connection.Status.DeepCopy()
	status.ObservedGeneration = connection.Generation
	if aggregate != nil {
		status.Revision = aggregate.ConnectionRevisions[string(connection.UID)]
		status.ManagedSecretRef = &infrav1alpha1.ConnectionManagedSecretReference{Name: aggregate.Name, Namespace: aggregate.Namespace, TargetRuntimeIdentity: aggregate.RuntimeIdentity}
	} else {
		// A non-ready/invalid connection must not keep advertising the prior
		// aggregate revision or Secret reference after its credentials have been
		// withdrawn.
		status.Revision = ""
		status.ManagedSecretRef = nil
	}
	conditionStatus := metav1.ConditionFalse
	if reason == "Ready" {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: infrav1alpha1.ConnectionConditionReady, Status: conditionStatus, Reason: reason, Message: message, ObservedGeneration: connection.Generation})
	if reflect.DeepEqual(previous, status) {
		return nil
	}
	connection.Status = *status
	return tenantClient.Status().Update(ctx, connection)
}

func (c *Controller) connectionRuntimeMetadata(ctx context.Context, tenant string, instance *unstructured.Unstructured) (string, string, error) {
	identity := "instance/" + string(instance.GetUID())
	if templateName, _, _ := unstructured.NestedString(instance.Object, "spec", "template"); templateName == infrav1alpha1.UniversalCodingSandboxTemplateName {
		identity = "sandbox/" + string(instance.GetUID())
	}
	name := aggregateConnectionSecretName(identity)
	namespace := instanceRuntimeNamespace(tenant, instance)
	secret, err := c.cfg.Runtime.Resource(connectionSecretGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return name, "", nil
	}
	if err != nil {
		return "", "", err
	}
	if err := validateAggregateSecretOwnership(secret, identity); err != nil {
		return "", "", err
	}
	return name, secret.GetAnnotations()[connectionRevisionAnnotation], nil
}

func providedConnectionInterface(tmpl *infrav1alpha1.Template, name string) (infrav1alpha1.TemplateProvidedConnection, bool) {
	if tmpl == nil || tmpl.Spec.Connections == nil {
		return infrav1alpha1.TemplateProvidedConnection{}, false
	}
	for _, item := range tmpl.Spec.Connections.Provides {
		if item.Name == name {
			return item, true
		}
	}
	return infrav1alpha1.TemplateProvidedConnection{}, false
}

func consumedConnectionInterface(tmpl *infrav1alpha1.Template, name string) (infrav1alpha1.TemplateConsumedConnection, bool) {
	if tmpl == nil || tmpl.Spec.Connections == nil {
		return infrav1alpha1.TemplateConsumedConnection{}, false
	}
	for _, item := range tmpl.Spec.Connections.Consumes {
		if item.Name == name {
			return item, true
		}
	}
	return infrav1alpha1.TemplateConsumedConnection{}, false
}

func validateConnectionMappings(requested []infrav1alpha1.ConnectionMapping, provided infrav1alpha1.TemplateProvidedConnection, consumed infrav1alpha1.TemplateConsumedConnection) ([]infrav1alpha1.ConnectionMapping, error) {
	allowedSource := map[string]bool{}
	for _, key := range provided.Keys {
		allowedSource[key] = true
	}
	allowedPair := map[string]bool{}
	defaults := make([]infrav1alpha1.ConnectionMapping, 0, len(consumed.Mappings))
	for _, mapping := range consumed.Mappings {
		allowedPair[mapping.SourceKey+"\x00"+mapping.TargetKey] = true
		defaults = append(defaults, infrav1alpha1.ConnectionMapping(mapping))
	}
	if len(requested) == 0 {
		requested = defaults
	}
	seenTarget := map[string]bool{}
	for _, mapping := range requested {
		if !allowedSource[mapping.SourceKey] {
			return nil, fmt.Errorf("source key %q is not allowlisted by the provided interface", mapping.SourceKey)
		}
		if !allowedPair[mapping.SourceKey+"\x00"+mapping.TargetKey] {
			return nil, fmt.Errorf("mapping %q to %q is not allowlisted by the consumed interface", mapping.SourceKey, mapping.TargetKey)
		}
		if seenTarget[mapping.TargetKey] {
			return nil, fmt.Errorf("target key %q is mapped more than once", mapping.TargetKey)
		}
		seenTarget[mapping.TargetKey] = true
	}
	return append([]infrav1alpha1.ConnectionMapping(nil), requested...), nil
}

func connectionSecretReference(instance *unstructured.Unstructured, dotPath string) (string, string, error) {
	parts := strings.Split(dotPath, ".")
	if len(parts) < 2 || parts[0] != "status" {
		return "", "", fmt.Errorf("provided interface secretRefPath must be below status")
	}
	value, found, err := unstructured.NestedMap(instance.Object, parts...)
	if err != nil || !found {
		return "", "", fmt.Errorf("source interface Secret reference %q is not available", dotPath)
	}
	name, _ := value["name"].(string)
	namespace, _ := value["namespace"].(string)
	if name == "" {
		return "", "", fmt.Errorf("source interface Secret reference %q has no name", dotPath)
	}
	return name, namespace, nil
}

func instanceRuntimeNamespace(tenant string, instance *unstructured.Unstructured) string {
	if namespace, _, _ := unstructured.NestedString(instance.Object, "status", "runtimeNamespace"); namespace != "" {
		return namespace
	}
	return kro.RuntimeNamespace(tenant, instance.GetNamespace())
}

func aggregateConnectionSecretName(runtimeIdentity string) string {
	return "faros-connections-" + shortHash(runtimeIdentity)
}
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
func connectionDataKey(uid, targetKey string) string { return "c-" + shortHash(uid+"/"+targetKey) }
func connectionRevision(item *resolvedConnection, keys []string) string {
	parts := []string{item.sourceSecretUID, item.sourceVersion, string(item.connection.UID), strings.Join(keys, ",")}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
func stringMapAny(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}
func mergeStringMap(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func validateAggregateSecretOwnership(secret *unstructured.Unstructured, runtimeIdentity string) error {
	if secret == nil {
		return fmt.Errorf("aggregate Secret is required")
	}
	labels := secret.GetLabels()
	annotations := secret.GetAnnotations()
	if labels[connectionManagedByLabel] != connectionManagedByValue || labels[connectionTargetRuntimeLabel] != shortHash(runtimeIdentity) || annotations[connectionTargetRuntimeAnnotation] != runtimeIdentity {
		return fmt.Errorf("Secret %s/%s already exists and is not owned by this connection target", secret.GetNamespace(), secret.GetName())
	}
	return nil
}

func aggregateSecretMatches(existing *unstructured.Unstructured, aggregate *aggregateConnectionSecret, inventory, entryRevisions string) bool {
	if existing == nil || aggregate == nil {
		return false
	}
	data, _, _ := unstructured.NestedStringMap(existing.Object, "data")
	if !reflect.DeepEqual(data, aggregate.Data) {
		return false
	}
	labels := existing.GetLabels()
	annotations := existing.GetAnnotations()
	return labels[connectionManagedByLabel] == connectionManagedByValue &&
		labels[connectionTargetRuntimeLabel] == shortHash(aggregate.RuntimeIdentity) &&
		annotations[connectionTargetRuntimeAnnotation] == aggregate.RuntimeIdentity &&
		annotations[connectionRevisionAnnotation] == aggregate.Revision &&
		annotations[connectionInventoryAnnotation] == inventory &&
		annotations[connectionEntryRevisionsAnnotation] == entryRevisions
}

func aggregateRevision(entryRevisions map[string]string) string {
	parts := make([]string, 0, len(entryRevisions))
	for uid, revision := range entryRevisions {
		parts = append(parts, uid+"="+revision)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:16])
}

func (c *Controller) developmentServiceConnectionFiles(ctx context.Context, tenantClient client.Client, tenant string, service *infrav1alpha1.DevelopmentService) (map[string]string, string, error) {
	files := map[string]string{}
	var revisions []string
	for _, name := range service.Spec.ConnectionRefs {
		connection := &infrav1alpha1.Connection{}
		if err := tenantClient.Get(ctx, client.ObjectKey{Name: name}, connection); err != nil {
			return nil, "", fmt.Errorf("Connection %q: %w", name, err)
		}
		if connection.Spec.Target.Kind != infrav1alpha1.ConnectionTargetService || connection.Spec.Target.Name != service.Name || connection.Spec.Target.UID != string(service.UID) {
			return nil, "", fmt.Errorf("Connection %q does not target this DevelopmentService UID", name)
		}
		ready := meta.FindStatusCondition(connection.Status.Conditions, infrav1alpha1.ConnectionConditionReady)
		if ready == nil || ready.Status != metav1.ConditionTrue || connection.Status.Revision == "" {
			return nil, "", fmt.Errorf("Connection %q is not ready", name)
		}
		resolved, err := c.resolveConnection(ctx, tenantClient, tenant, connection)
		if err != nil {
			return nil, "", fmt.Errorf("Connection %q: %w", name, err)
		}
		for _, mapping := range resolved.mappings {
			if _, exists := files[mapping.TargetKey]; exists {
				return nil, "", fmt.Errorf("Connection %q collides on environment name %q", name, mapping.TargetKey)
			}
			files[mapping.TargetKey] = connectionMountPath + "/" + connectionDataKey(string(connection.UID), mapping.TargetKey)
		}
		revisions = append(revisions, connection.Status.Revision)
	}
	if len(revisions) == 0 {
		return files, "", nil
	}
	sort.Strings(revisions)
	sum := sha256.Sum256([]byte(strings.Join(revisions, "\n")))
	return files, hex.EncodeToString(sum[:16]), nil
}
