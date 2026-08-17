// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package install

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestRuntimeTLSForIdentityPreservesSupportedSourceSettings(t *testing.T) {
	t.Run("CAData takes precedence and is copied", func(t *testing.T) {
		source := []byte("inline-ca")
		config := &rest.Config{TLSClientConfig: rest.TLSClientConfig{
			CAData: source, CAFile: filepath.Join(t.TempDir(), "missing-ca"), ServerName: "kcp.internal",
		}}
		caData, insecure, serverName, err := runtimeTLSForIdentity(config)
		if err != nil {
			t.Fatal(err)
		}
		if string(caData) != string(source) || insecure || serverName != "kcp.internal" {
			t.Fatalf("TLS settings = (%q, %t, %q)", caData, insecure, serverName)
		}
		caData[0] = 'X'
		if source[0] == 'X' {
			t.Fatal("CAData aliases the mutable source rest.Config")
		}
	})

	t.Run("CAFile is materialized for portable kubeconfig", func(t *testing.T) {
		caFile := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(caFile, []byte("file-ca"), 0o600); err != nil {
			t.Fatal(err)
		}
		caData, insecure, serverName, err := runtimeTLSForIdentity(&rest.Config{TLSClientConfig: rest.TLSClientConfig{
			CAFile: caFile, ServerName: "front-proxy.internal",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if string(caData) != "file-ca" || insecure || serverName != "front-proxy.internal" {
			t.Fatalf("TLS settings = (%q, %t, %q)", caData, insecure, serverName)
		}
	})

	t.Run("insecure source omits ignored CA but preserves SNI", func(t *testing.T) {
		caData, insecure, serverName, err := runtimeTLSForIdentity(&rest.Config{TLSClientConfig: rest.TLSClientConfig{
			Insecure: true, CAFile: filepath.Join(t.TempDir(), "missing-ca"), ServerName: "sni.internal",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(caData) != 0 || !insecure || serverName != "sni.internal" {
			t.Fatalf("TLS settings = (%q, %t, %q)", caData, insecure, serverName)
		}
	})
}

func TestRuntimeKubeconfigContainsOnlyMintedIdentity(t *testing.T) {
	id := &RuntimeIdentity{
		Server: "https://kcp.internal", CAData: []byte("ca-data"), Token: "runtime-token",
		ServiceAccount: "infrastructure-runtime", Namespace: "default",
	}
	data, err := RuntimeKubeconfig(id)
	if err != nil {
		t.Fatal(err)
	}
	config, err := clientcmd.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.AuthInfos) != 1 || config.AuthInfos[id.ServiceAccount] == nil {
		t.Fatalf("auth infos = %v, want only %q", config.AuthInfos, id.ServiceAccount)
	}
	auth := config.AuthInfos[id.ServiceAccount]
	if auth.Token != id.Token || len(auth.ClientCertificateData) != 0 || len(auth.ClientKeyData) != 0 {
		t.Fatalf("runtime auth unexpectedly retained bootstrap credentials: %#v", auth)
	}
}

func TestBuildRuntimeKubeconfigCarriesMintedTLSSettings(t *testing.T) {
	id := &RuntimeIdentity{
		Server: "https://kcp.internal", CAData: []byte("ca-data"), ServerName: "certificate.internal",
		Token: "runtime-token", ServiceAccount: "runtime", Namespace: "default",
	}
	cluster := buildRuntimeKubeconfig(id).Clusters["provider-workspace"]
	if cluster.Server != id.Server || cluster.TLSServerName != id.ServerName || string(cluster.CertificateAuthorityData) != string(id.CAData) {
		t.Fatalf("cluster TLS settings were not preserved: %#v", cluster)
	}
	if cluster.InsecureSkipTLSVerify {
		t.Fatal("secure source became insecure")
	}

	id.Insecure = true
	id.CAData = nil
	cluster = buildRuntimeKubeconfig(id).Clusters["provider-workspace"]
	if !cluster.InsecureSkipTLSVerify || cluster.TLSServerName != id.ServerName {
		t.Fatalf("insecure/SNI settings were not preserved: %#v", cluster)
	}
}
