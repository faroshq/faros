// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

import (
	"strings"
	"testing"
)

func TestValidateDevelopmentValidatesConnectionInterfaces(t *testing.T) {
	valid := &TemplateConnections{
		Provides: []TemplateProvidedConnection{{Name: "default", Type: "postgresql", SecretRefPath: "status.connectionSecretRef", Keys: []string{"uri"}}},
		Consumes: []TemplateConsumedConnection{{Name: "postgresql", Type: "postgresql", Mappings: []TemplateConnectionMapping{{SourceKey: "uri", TargetKey: "DATABASE_URL"}}}},
	}
	if err := (&TemplateSpec{Connections: valid}).ValidateDevelopment(); err != nil {
		t.Fatalf("valid interfaces: %v", err)
	}
	tests := []struct {
		name        string
		connections *TemplateConnections
		want        string
	}{
		{name: "secret ref outside status", connections: &TemplateConnections{Provides: []TemplateProvidedConnection{{Name: "default", Type: "postgresql", SecretRefPath: "spec.password", Keys: []string{"uri"}}}}, want: "below status"},
		{name: "duplicate provider", connections: &TemplateConnections{Provides: []TemplateProvidedConnection{{Name: "default", Type: "a", SecretRefPath: "status.ref", Keys: []string{"uri"}}, {Name: "default", Type: "b", SecretRefPath: "status.ref", Keys: []string{"uri"}}}}, want: "duplicated"},
		{name: "invalid target env", connections: &TemplateConnections{Consumes: []TemplateConsumedConnection{{Name: "db", Type: "postgresql", Mappings: []TemplateConnectionMapping{{SourceKey: "uri", TargetKey: "not-an-env"}}}}}, want: "invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&TemplateSpec{Connections: tc.connections}).ValidateDevelopment()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
