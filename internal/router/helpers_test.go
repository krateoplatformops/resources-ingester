package router

import (
	"testing"
)

func TestGetRoutersFromStatic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []map[string]string
	}{
		{
			name:  "resource with group",
			input: "apiextensions.k8s.io/v1/customresourcedefinitions/CustomResourceDefinition",
			expected: []map[string]string{
				{
					"group":    "apiextensions.k8s.io",
					"version":  "v1",
					"resource": "customresourcedefinitions",
					"kind":     "CustomResourceDefinition",
				},
			},
		},
		{
			name:  "resource without group (core API)",
			input: "/v1/deployments/Deployment",
			expected: []map[string]string{
				{
					"group":    "",
					"version":  "v1",
					"resource": "deployments",
					"kind":     "Deployment",
				},
			},
		},
		{
			name:  "multiple resources",
			input: "apiextensions.k8s.io/v1/customresourcedefinitions/CustomResourceDefinition\n/v1/deployments/Deployment",
			expected: []map[string]string{
				{
					"group":    "apiextensions.k8s.io",
					"version":  "v1",
					"resource": "customresourcedefinitions",
					"kind":     "CustomResourceDefinition",
				},
				{
					"group":    "",
					"version":  "v1",
					"resource": "deployments",
					"kind":     "Deployment",
				},
			},
		},
		{
			name:     "malformed line is skipped",
			input:    "invalid/line",
			expected: []map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getRoutersFromStatic(tt.input)

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d.\nResult: %+v", len(tt.expected), len(result), result)
			}

			for i, row := range result {
				for _, key := range []string{"group", "version", "resource", "kind"} {
					if row[key] != tt.expected[i][key] {
						t.Errorf("row %d: expected %s=%q, got %q", i, key, tt.expected[i][key], row[key])
					}
				}
			}
		})
	}
}
