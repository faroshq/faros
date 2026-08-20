// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tenant

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateUsesTypedMutationAndPreservesCreateConflict(t *testing.T) {
	var requestBody struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	client := &GraphQLClient{
		hubBase: "https://hub.invalid",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(body, &requestBody); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":{"agents_faros_sh":{"v1alpha1":{"createAgent":{"__typename":"Agent"}}}}}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	scope, err := client.For("cluster", "token")
	if err != nil {
		t.Fatal(err)
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "agents.faros.sh/v1alpha1",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "research"},
	}}
	res := Resource{
		GVR:  schema.GroupVersionResource{Group: "agents.faros.sh", Version: "v1alpha1", Resource: "agents"},
		Kind: "Agent",
	}
	got, err := scope.Create(t.Context(), res, "", obj)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetName() != "research" {
		t.Fatalf("created object name = %q, want research", got.GetName())
	}
	if !strings.Contains(requestBody.Query, "mutation($object: AgentsFarosShV1alpha1Agent_Input!)") ||
		!strings.Contains(requestBody.Query, "createAgent(object: $object) { __typename }") {
		t.Fatalf("create query used the wrong mutation: %s", requestBody.Query)
	}
	object, ok := requestBody.Variables["object"].(map[string]any)
	if !ok || object["metadata"].(map[string]any)["name"] != "research" {
		t.Fatalf("create variables did not carry the object identity: %#v", requestBody.Variables)
	}
}

func TestCreateMapsAlreadyExistsWithoutApplying(t *testing.T) {
	var requestQuery string
	client := &GraphQLClient{
		hubBase: "https://hub.invalid",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			var request struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			requestQuery = request.Query
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"errors":[{"message":"agents.faros.sh \"research\" already exists"}]}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	scope, err := client.For("cluster", "token")
	if err != nil {
		t.Fatal(err)
	}
	res := Resource{
		GVR:  schema.GroupVersionResource{Group: "agents.faros.sh", Version: "v1alpha1", Resource: "agents"},
		Kind: "Agent",
	}
	_, err = scope.Create(t.Context(), res, "", &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "agents.faros.sh/v1alpha1",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "research"},
	}})
	if !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create error = %v, want AlreadyExists", err)
	}
	if strings.Contains(requestQuery, "applyYaml") {
		t.Fatalf("create path unexpectedly used applyYaml: %s", requestQuery)
	}
}
