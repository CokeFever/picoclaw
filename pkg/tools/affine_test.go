package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAffineTool_Name(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	assert.Equal(t, "affine", tool.Name())
}

func TestAffineTool_Description(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	desc := tool.Description()
	assert.Contains(t, desc, "Affine")
	assert.Contains(t, desc, "workspace")
}

func TestAffineTool_Parameters(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	params := tool.Parameters()
	assert.NotNil(t, params)

	// Check required fields
	assert.Equal(t, "object", params["type"])
	props, ok := params["properties"].(map[string]any)
	assert.True(t, ok)
	assert.NotNil(t, props["action"])

	// Check action enum
	actionProp, ok := props["action"].(map[string]any)
	assert.True(t, ok)
	enum, ok := actionProp["enum"].([]string)
	assert.True(t, ok)
	assert.Contains(t, enum, "list_workspaces")
	assert.Contains(t, enum, "list_pages")
	assert.Contains(t, enum, "search")
	assert.Contains(t, enum, "read_page")
	assert.Contains(t, enum, "create_page")
	assert.Contains(t, enum, "update_page")
	assert.Contains(t, enum, "get_structure")
}

func TestAffineTool_Execute_MissingAction(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "action is required")
}

func TestAffineTool_Execute_UnknownAction(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action": "invalid_action",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "unknown action")
}

func TestAffineTool_Execute_SearchMissingQuery(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action": "search",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "query is required")
}

func TestAffineTool_Execute_ReadPageMissingID(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action": "read_page",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "page_id is required")
}

func TestAffineTool_Execute_CreatePageMissingTitle(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action": "create_page",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "title is required")
}

func TestAffineTool_Execute_UpdatePageMissingID(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action": "update_page",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "page_id is required")
}

func TestAffineTool_Execute_UpdatePageNoUpdates(t *testing.T) {
	tool := NewAffineTool(AffineToolOptions{
		APIURL:      "https://app.affine.pro/graphql",
		APIKey:      "test-key",
		WorkspaceID: "test-workspace",
	})

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "update_page",
		"page_id": "test-page-id",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "no updates specified")
}

// Note: Integration tests against a real Affine instance would go here
// They would require a test Affine instance and valid credentials
