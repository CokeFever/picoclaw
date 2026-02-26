package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AffineSimpleTool provides access to Affine workspace via HTTP MCP endpoint
type AffineSimpleTool struct {
	mcpEndpoint string
	apiKey      string
	workspaceID string
	httpClient  *http.Client
}

// AffineSimpleToolOptions configures the Affine simple tool
type AffineSimpleToolOptions struct {
	MCPEndpoint    string
	APIKey         string
	WorkspaceID    string
	TimeoutSeconds int
}

// NewAffineSimpleTool creates a new Affine simple tool instance
func NewAffineSimpleTool(opts AffineSimpleToolOptions) *AffineSimpleTool {
	timeout := time.Duration(opts.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &AffineSimpleTool{
		mcpEndpoint: opts.MCPEndpoint,
		apiKey:      opts.APIKey,
		workspaceID: opts.WorkspaceID,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (t *AffineSimpleTool) Name() string {
	return "affine"
}

func (t *AffineSimpleTool) Description() string {
	return "Search and read documents from your Affine workspace. Use this to access your knowledge base."
}

func (t *AffineSimpleTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"search",
					"read",
				},
				"description": "Action: 'search' to find documents, 'read' to get full content",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search action) or document ID (for read action)",
			},
		},
		"required": []string{"action", "query"},
	}
}

func (t *AffineSimpleTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	query, ok := args["query"].(string)
	if !ok {
		return ErrorResult("query is required")
	}

	switch action {
	case "search":
		return t.search(ctx, query)
	case "read":
		return t.read(ctx, query)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (use 'search' or 'read')", action))
	}
}

func (t *AffineSimpleTool) search(ctx context.Context, query string) *ToolResult {
	// Call MCP endpoint with search request
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "doc-keyword-search",
			"arguments": map[string]interface{}{
				"query": query,
			},
		},
	}

	result, err := t.callMCP(ctx, reqBody)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	// Parse search results
	var searchResults []struct {
		DocID   string `json:"docId"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}

	// Try to extract from result
	if resultMap, ok := result.(map[string]interface{}); ok {
		if content, ok := resultMap["content"].([]interface{}); ok {
			for _, item := range content {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if text, ok := itemMap["text"].(string); ok {
						// Parse the text as JSON
						var docs []struct {
							DocID   string `json:"docId"`
							Title   string `json:"title"`
							Snippet string `json:"snippet"`
						}
						if err := json.Unmarshal([]byte(text), &docs); err == nil {
							searchResults = append(searchResults, docs...)
						}
					}
				}
			}
		}
	}

	if len(searchResults) == 0 {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("No results found for: %s", query),
			ForUser: fmt.Sprintf("No results found for: %s", query),
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Found %d results for '%s':", len(searchResults), query))
	for i, doc := range searchResults {
		lines = append(lines, fmt.Sprintf("%d. %s (ID: %s)", i+1, doc.Title, doc.DocID))
		if doc.Snippet != "" {
			lines = append(lines, fmt.Sprintf("   %s", doc.Snippet))
		}
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineSimpleTool) read(ctx context.Context, docID string) *ToolResult {
	// Call MCP endpoint with read request
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "doc-read",
			"arguments": map[string]interface{}{
				"docId": docID,
			},
		},
	}

	result, err := t.callMCP(ctx, reqBody)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read failed: %v", err))
	}

	// Extract content from result
	var content string
	var title string

	if resultMap, ok := result.(map[string]interface{}); ok {
		if contentArray, ok := resultMap["content"].([]interface{}); ok {
			for _, item := range contentArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if text, ok := itemMap["text"].(string); ok {
						content += text + "\n"
					}
				}
			}
		}
	}

	if content == "" {
		content = fmt.Sprintf("Document ID: %s\n(Content could not be extracted)", docID)
	}

	output := fmt.Sprintf("Document: %s\n\n%s", title, content)
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineSimpleTool) callMCP(ctx context.Context, reqBody map[string]interface{}) (interface{}, error) {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.mcpEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var mcpResp struct {
		Result interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return mcpResp.Result, nil
}
