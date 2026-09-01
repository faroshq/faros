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

package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	asclient "github.com/faroshq/provider-app-studio/client"
)

type projectProductionTemplateComponentView struct {
	Name       string `json:"name"`
	ImageInput string `json:"imageInput"`
}

type projectProductionTemplateView struct {
	Name        string                                   `json:"name"`
	DisplayName string                                   `json:"displayName,omitempty"`
	Description string                                   `json:"description,omitempty"`
	Category    string                                   `json:"category,omitempty"`
	Components  []projectProductionTemplateComponentView `json:"components"`
}

func projectProductionTemplateComponents(info projectTemplateInfo) []projectProductionTemplateComponentView {
	inputs := projectTemplateImageInputs(info)
	if len(inputs) == 0 && len(info.Components) == 0 {
		if schemaInputs := projectTemplateSchemaImageInputs(info); len(schemaInputs) == 1 {
			inputs["default"] = schemaInputs[0]
		}
	}
	out := make([]projectProductionTemplateComponentView, 0, len(inputs))
	for name, imageInput := range inputs {
		name = strings.TrimSpace(name)
		imageInput = strings.TrimSpace(imageInput)
		if name != "" && imageInput != "" && projectProductionImageInputDeclared(info, imageInput) {
			out = append(out, projectProductionTemplateComponentView{Name: name, ImageInput: imageInput})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func projectProductionImageInputDeclared(info projectTemplateInfo, imageInput string) bool {
	properties, _ := info.ProductionSchema["properties"].(map[string]any)
	raw, found := properties[strings.TrimSpace(imageInput)]
	if !found {
		return false
	}
	field, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	fieldType, _ := field["type"].(string)
	return strings.TrimSpace(fieldType) == "string"
}

func productionTemplateViews(items []unstructured.Unstructured) []projectProductionTemplateView {
	out := make([]projectProductionTemplateView, 0, len(items))
	for i := range items {
		info, err := projectTemplateInfoFromUnstructured(&items[i])
		if err != nil || info.PlatformOwned {
			continue
		}
		components := projectProductionTemplateComponents(info)
		if len(components) == 0 {
			continue
		}
		view := projectProductionTemplateView{Name: info.Name, Components: components}
		view.DisplayName, _, _ = unstructured.NestedString(items[i].Object, "spec", "displayName")
		view.Description, _, _ = unstructured.NestedString(items[i].Object, "spec", "description")
		view.Category, _, _ = unstructured.NestedString(items[i].Object, "spec", "category")
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func listProductionTemplateViews(ctx context.Context, c *asclient.Client) ([]projectProductionTemplateView, error) {
	list, err := c.Resource(templateResource, "").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return productionTemplateViews(list.Items), nil
}

func (s *Server) listProductionTemplates(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.requireProjectClient(w, r)
	if !ok {
		return
	}
	templates, err := listProductionTemplateViews(r.Context(), c)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}
