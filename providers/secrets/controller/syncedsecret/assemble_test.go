/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package syncedsecret

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
	"github.com/faroshq/provider-secrets/backend/stub"
)

func testStore() *secretsv1alpha1.SecretStore {
	return &secretsv1alpha1.SecretStore{
		Spec: secretsv1alpha1.SecretStoreSpec{Backend: secretsv1alpha1.BackendVault},
	}
}

func TestAssembleDataFrom(t *testing.T) {
	b := stub.New() // seeds demo/config with username+password
	data, err := assemble(context.Background(), b, testStore(), backend.Credential{Token: "t"}, &secretsv1alpha1.SyncedSecretSpec{
		DataFrom: []secretsv1alpha1.RemoteReference{{Path: "demo/config"}},
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(data) != 2 || string(data["username"]) != "demo-user" {
		t.Fatalf("data = %v", data)
	}
}

func TestAssembleDataMappingWinsOverDataFrom(t *testing.T) {
	b := stub.New()
	b.Set("other/path", backend.SecretValues{"password": []byte("override")})
	data, err := assemble(context.Background(), b, testStore(), backend.Credential{Token: "t"}, &secretsv1alpha1.SyncedSecretSpec{
		DataFrom: []secretsv1alpha1.RemoteReference{{Path: "demo/config"}},
		Data: []secretsv1alpha1.SyncedSecretData{
			{SecretKey: "password", RemoteRef: secretsv1alpha1.RemoteReference{Path: "other/path"}},
		},
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if string(data["password"]) != "override" {
		t.Fatalf("password = %q, want override (explicit data must win)", data["password"])
	}
	if string(data["username"]) != "demo-user" {
		t.Fatalf("username = %q, want demo-user", data["username"])
	}
}

func TestAssembleEmptyPropertyDefaultsToSecretKey(t *testing.T) {
	b := stub.New()
	data, err := assemble(context.Background(), b, testStore(), backend.Credential{Token: "t"}, &secretsv1alpha1.SyncedSecretSpec{
		Data: []secretsv1alpha1.SyncedSecretData{
			{SecretKey: "username", RemoteRef: secretsv1alpha1.RemoteReference{Path: "demo/config"}},
		},
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if string(data["username"]) != "demo-user" {
		t.Fatalf("username = %q, want demo-user", data["username"])
	}
}

func TestAssembleMissingProperty(t *testing.T) {
	b := stub.New()
	_, err := assemble(context.Background(), b, testStore(), backend.Credential{Token: "t"}, &secretsv1alpha1.SyncedSecretSpec{
		Data: []secretsv1alpha1.SyncedSecretData{
			{SecretKey: "nope", RemoteRef: secretsv1alpha1.RemoteReference{Path: "demo/config"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `no property "nope"`) {
		t.Fatalf("err = %v, want missing-property error", err)
	}
}

func TestHashDataStableAndSensitive(t *testing.T) {
	a := map[string][]byte{"a": []byte("1"), "b": []byte("2")}
	b := map[string][]byte{"b": []byte("2"), "a": []byte("1")}
	if HashData(a) != HashData(b) {
		t.Fatal("hash depends on map order")
	}
	if !strings.HasPrefix(HashData(a), "sha256:") {
		t.Fatalf("hash %q lacks sha256: prefix", HashData(a))
	}
	c := map[string][]byte{"a": []byte("1"), "b": []byte("3")}
	if HashData(a) == HashData(c) {
		t.Fatal("hash did not change with content")
	}
	// Key/value boundary: {"ab":"c"} must differ from {"a":"bc"}.
	d := map[string][]byte{"ab": []byte("c")}
	e := map[string][]byte{"a": []byte("bc")}
	if HashData(d) == HashData(e) {
		t.Fatal("hash is ambiguous across key/value boundaries")
	}
}

func TestRefreshIntervalBounds(t *testing.T) {
	if got := refreshInterval(&secretsv1alpha1.SyncedSecretSpec{}); got != defaultRefreshInterval {
		t.Fatalf("default interval = %v", got)
	}
	short := &secretsv1alpha1.SyncedSecretSpec{RefreshInterval: &metav1.Duration{Duration: time.Millisecond}}
	if got := refreshInterval(short); got != minRefreshInterval {
		t.Fatalf("floored interval = %v, want %v", got, minRefreshInterval)
	}
	custom := &secretsv1alpha1.SyncedSecretSpec{RefreshInterval: &metav1.Duration{Duration: 5 * time.Minute}}
	if got := refreshInterval(custom); got != 5*time.Minute {
		t.Fatalf("custom interval = %v, want 5m", got)
	}
}
