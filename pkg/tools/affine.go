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

// AffineClient handles MCP communication with Affine API
type AffineClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// MCP request/response structures
type mcpRequest struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
}

type mcpResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newAffineClient(baseURL, apiKey string, timeout time.Duration) *AffineClient {
	return &AffineClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *AffineClient) call(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	reqBody := mcpRequest{
		Method: method,
		Params: params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
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

	var mcpResp mcpResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return mcpResp.Result, nil
}

// AffineTool provides access to Affine workspace operations
type AffineTool struct {
	client      *AffineClient
	workspaceID string
}

// AffineToolOptions configures the Affine tool
type AffineToolOptions struct {
	APIURL         string
	APIKey         string
	WorkspaceID    string
	TimeoutSeconds int
}

// NewAffineTool creates a new Affine tool instance
func NewAffineTool(opts AffineToolOptions) *AffineTool {
	timeout := time.Duration(opts.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &AffineTool{
		client:      newAffineClient(opts.APIURL, opts.APIKey, timeout),
		workspaceID: opts.WorkspaceID,
	}
}

func (t *AffineTool) Name() string {
	return "affine"
}

func (t *AffineTool) Description() string {
	return "Interact with Affine workspace via MCP: search pages, read page content, list documents. Use this to access your knowledge base in Affine."
}

func (t *AffineTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"search",
					"read_doc",
					"list_docs",
				},
				"description": "Action to perform (MCP server supports: search, read_doc, list_docs)",
			},
			"workspace_id": map[string]any{
				"type":        "string",
				"description": "Workspace ID (optional, uses default if not specified)",
			},
			"doc_id": map[string]any{
				"type":        "string",
				"description": "Document ID (required for read_doc)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search action)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results (default: 10)",
				"minimum":     1.0,
				"maximum":     50.0,
			},
		},
		"required": []string{"action"},
	}
}

func (t *AffineTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	// Get workspace ID (use default if not specified)
	workspaceID := t.workspaceID
	if wsID, ok := args["workspace_id"].(string); ok && wsID != "" {
		workspaceID = wsID
	}

	switch action {
	case "search":
		query, ok := args["query"].(string)
		if !ok {
			return ErrorResult("query is required for search")
		}
		return t.searchDocs(ctx, workspaceID, query, args)
	case "read_doc":
		docID, ok := args["doc_id"].(string)
		if !ok {
			return ErrorResult("doc_id is required for read_doc")
		}
		return t.readDoc(ctx, workspaceID, docID)
	case "list_docs":
		return t.listDocs(ctx, workspaceID, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (supported: search, read_doc, list_docs)", action))
	}
}

func (t *AffineTool) searchDocs(ctx context.Context, workspaceID, query string, args map[string]any) *ToolResult {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	params := map[string]interface{}{
		"query": query,
		"limit": limit,
	}

	data, err := t.client.call(ctx, "doc-keyword-search", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to search: %v", err))
	}

	var results []struct {
		DocID   string `json:"docId"`
		Title   string `json:"title"`
		Snippet string `json:"snippet"`
	}

	if err := json.Unmarshal(data, &results); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	if len(results) == 0 {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("No results found for: %s", query),
			ForUser: fmt.Sprintf("No results found for: %s", query),
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Search results for '%s' (%d found):", query, len(results)))
	for i, item := range results {
		lines = append(lines, fmt.Sprintf("%d. %s (ID: %s)", i+1, item.Title, item.DocID))
		if item.Snippet != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Snippet))
		}
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) readDoc(ctx context.Context, workspaceID, docID string) *ToolResult {
	params := map[string]interface{}{
		"docId": docID,
	}

	data, err := t.client.call(ctx, "doc-read", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read document: %v", err))
	}

	var doc struct {
		DocID   string `json:"docId"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Title: %s", doc.Title))
	lines = append(lines, fmt.Sprintf("ID: %s", doc.DocID))
	lines = append(lines, "")
	lines = append(lines, "Content:")
	lines = append(lines, doc.Content)

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) listDocs(ctx context.Context, workspaceID string, args map[string]any) *ToolResult {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	params := map[string]interface{}{
		"limit": limit,
	}

	data, err := t.client.call(ctx, "doc-keyword-search", params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list documents: %v", err))
	}

	var docs []struct {
		DocID string `json:"docId"`
		Title string `json:"title"`
	}

	if err := json.Unmarshal(data, &docs); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	if len(docs) == 0 {
		return &ToolResult{
			ForLLM:  "No documents found in workspace",
			ForUser: "No documents found in workspace",
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Documents in workspace (showing %d):", len(docs)))
	for i, doc := range docs {
		lines = append(lines, fmt.Sprintf("%d. %s (ID: %s)", i+1, doc.Title, doc.DocID))
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}
	query := `
		query ListWorkspaces {
			workspaces {
				id
				name
				createdAt
				memberCount
			}
		}
	`

	data, err := t.client.query(ctx, query, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list workspaces: %v", err))
	}

	var result struct {
		Workspaces []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			CreatedAt   string `json:"createdAt"`
			MemberCount int    `json:"memberCount"`
		} `json:"workspaces"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	if len(result.Workspaces) == 0 {
		return &ToolResult{
			ForLLM:  "No workspaces found",
			ForUser: "No workspaces found",
		}
	}

	var lines []string
	lines = append(lines, "Available Workspaces:")
	for _, ws := range result.Workspaces {
		lines = append(lines, fmt.Sprintf("- %s (ID: %s, Members: %d)", ws.Name, ws.ID, ws.MemberCount))
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) listPages(ctx context.Context, workspaceID string, args map[string]any) *ToolResult {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	query := `
		query ListPages($workspaceId: ID!, $limit: Int!) {
			workspace(id: $workspaceId) {
				pages(limit: $limit) {
					id
					title
					createdAt
					updatedAt
					tags
				}
			}
		}
	`

	variables := map[string]interface{}{
		"workspaceId": workspaceID,
		"limit":       limit,
	}

	data, err := t.client.query(ctx, query, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list pages: %v", err))
	}

	var result struct {
		Workspace struct {
			Pages []struct {
				ID        string   `json:"id"`
				Title     string   `json:"title"`
				CreatedAt string   `json:"createdAt"`
				UpdatedAt string   `json:"updatedAt"`
				Tags      []string `json:"tags"`
			} `json:"pages"`
		} `json:"workspace"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	pages := result.Workspace.Pages
	if len(pages) == 0 {
		return &ToolResult{
			ForLLM:  "No pages found in workspace",
			ForUser: "No pages found in workspace",
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Pages in workspace (showing %d):", len(pages)))
	for i, page := range pages {
		tagStr := ""
		if len(page.Tags) > 0 {
			tagStr = fmt.Sprintf(" [Tags: %s]", strings.Join(page.Tags, ", "))
		}
		lines = append(lines, fmt.Sprintf("%d. %s (ID: %s)%s", i+1, page.Title, page.ID, tagStr))
		lines = append(lines, fmt.Sprintf("   Updated: %s", page.UpdatedAt))
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) searchPages(ctx context.Context, workspaceID, query string, args map[string]any) *ToolResult {
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	gqlQuery := `
		query SearchPages($workspaceId: ID!, $query: String!, $limit: Int!) {
			workspace(id: $workspaceId) {
				search(query: $query, limit: $limit) {
					id
					title
					snippet
					tags
					updatedAt
				}
			}
		}
	`

	variables := map[string]interface{}{
		"workspaceId": workspaceID,
		"query":       query,
		"limit":       limit,
	}

	data, err := t.client.query(ctx, gqlQuery, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to search: %v", err))
	}

	var result struct {
		Workspace struct {
			Search []struct {
				ID        string   `json:"id"`
				Title     string   `json:"title"`
				Snippet   string   `json:"snippet"`
				Tags      []string `json:"tags"`
				UpdatedAt string   `json:"updatedAt"`
			} `json:"search"`
		} `json:"workspace"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	results := result.Workspace.Search
	if len(results) == 0 {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("No results found for: %s", query),
			ForUser: fmt.Sprintf("No results found for: %s", query),
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Search results for '%s' (%d found):", query, len(results)))
	for i, item := range results {
		tagStr := ""
		if len(item.Tags) > 0 {
			tagStr = fmt.Sprintf(" [%s]", strings.Join(item.Tags, ", "))
		}
		lines = append(lines, fmt.Sprintf("%d. %s (ID: %s)%s", i+1, item.Title, item.ID, tagStr))
		if item.Snippet != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Snippet))
		}
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) readPage(ctx context.Context, workspaceID, pageID string) *ToolResult {
	query := `
		query ReadPage($workspaceId: ID!, $pageId: ID!) {
			workspace(id: $workspaceId) {
				page(id: $pageId) {
					id
					title
					content
					tags
					createdAt
					updatedAt
					parent {
						id
						title
					}
				}
			}
		}
	`

	variables := map[string]interface{}{
		"workspaceId": workspaceID,
		"pageId":      pageID,
	}

	data, err := t.client.query(ctx, query, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read page: %v", err))
	}

	var result struct {
		Workspace struct {
			Page struct {
				ID        string   `json:"id"`
				Title     string   `json:"title"`
				Content   string   `json:"content"`
				Tags      []string `json:"tags"`
				CreatedAt string   `json:"createdAt"`
				UpdatedAt string   `json:"updatedAt"`
				Parent    *struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"parent"`
			} `json:"page"`
		} `json:"workspace"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	page := result.Workspace.Page
	var lines []string
	lines = append(lines, fmt.Sprintf("Title: %s", page.Title))
	lines = append(lines, fmt.Sprintf("ID: %s", page.ID))

	if len(page.Tags) > 0 {
		lines = append(lines, fmt.Sprintf("Tags: %s", strings.Join(page.Tags, ", ")))
	}

	if page.Parent != nil {
		lines = append(lines, fmt.Sprintf("Parent: %s (ID: %s)", page.Parent.Title, page.Parent.ID))
	}

	lines = append(lines, fmt.Sprintf("Updated: %s", page.UpdatedAt))
	lines = append(lines, "")
	lines = append(lines, "Content:")
	lines = append(lines, page.Content)

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) createPage(ctx context.Context, workspaceID, title string, args map[string]any) *ToolResult {
	content, _ := args["content"].(string)
	var tags []string
	if tagsRaw, ok := args["tags"].([]interface{}); ok {
		for _, tag := range tagsRaw {
			if tagStr, ok := tag.(string); ok {
				tags = append(tags, tagStr)
			}
		}
	}

	query := `
		mutation CreatePage($workspaceId: ID!, $title: String!, $content: String, $tags: [String!]) {
			createPage(workspaceId: $workspaceId, title: $title, content: $content, tags: $tags) {
				id
				title
				tags
			}
		}
	`

	variables := map[string]interface{}{
		"workspaceId": workspaceID,
		"title":       title,
		"content":     content,
		"tags":        tags,
	}

	data, err := t.client.query(ctx, query, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create page: %v", err))
	}

	var result struct {
		CreatePage struct {
			ID    string   `json:"id"`
			Title string   `json:"title"`
			Tags  []string `json:"tags"`
		} `json:"createPage"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	page := result.CreatePage
	tagStr := ""
	if len(page.Tags) > 0 {
		tagStr = fmt.Sprintf(" with tags [%s]", strings.Join(page.Tags, ", "))
	}

	output := fmt.Sprintf("Created page '%s' (ID: %s)%s", page.Title, page.ID, tagStr)
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) updatePage(ctx context.Context, workspaceID, pageID string, args map[string]any) *ToolResult {
	var updates []string
	variables := map[string]interface{}{
		"workspaceId": workspaceID,
		"pageId":      pageID,
	}

	if title, ok := args["title"].(string); ok && title != "" {
		variables["title"] = title
		updates = append(updates, "title")
	}

	if content, ok := args["content"].(string); ok && content != "" {
		variables["content"] = content
		updates = append(updates, "content")
	}

	if tagsRaw, ok := args["tags"].([]interface{}); ok {
		var tags []string
		for _, tag := range tagsRaw {
			if tagStr, ok := tag.(string); ok {
				tags = append(tags, tagStr)
			}
		}
		variables["tags"] = tags
		updates = append(updates, "tags")
	}

	if len(updates) == 0 {
		return ErrorResult("no updates specified (provide title, content, or tags)")
	}

	query := `
		mutation UpdatePage($workspaceId: ID!, $pageId: ID!, $title: String, $content: String, $tags: [String!]) {
			updatePage(workspaceId: $workspaceId, pageId: $pageId, title: $title, content: $content, tags: $tags) {
				id
				title
				tags
				updatedAt
			}
		}
	`

	data, err := t.client.query(ctx, query, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to update page: %v", err))
	}

	var result struct {
		UpdatePage struct {
			ID        string   `json:"id"`
			Title     string   `json:"title"`
			Tags      []string `json:"tags"`
			UpdatedAt string   `json:"updatedAt"`
		} `json:"updatePage"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	page := result.UpdatePage
	output := fmt.Sprintf("Updated page '%s' (ID: %s) - changed: %s", page.Title, page.ID, strings.Join(updates, ", "))

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}

func (t *AffineTool) getStructure(ctx context.Context, workspaceID string) *ToolResult {
	query := `
		query GetStructure($workspaceId: ID!) {
			workspace(id: $workspaceId) {
				id
				name
				structure {
					categories {
						name
						pageCount
					}
					tags {
						name
						count
					}
					totalPages
				}
			}
		}
	`

	variables := map[string]interface{}{
		"workspaceId": workspaceID,
	}

	data, err := t.client.query(ctx, query, variables)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get structure: %v", err))
	}

	var result struct {
		Workspace struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Structure struct {
				Categories []struct {
					Name      string `json:"name"`
					PageCount int    `json:"pageCount"`
				} `json:"categories"`
				Tags []struct {
					Name  string `json:"name"`
					Count int    `json:"count"`
				} `json:"tags"`
				TotalPages int `json:"totalPages"`
			} `json:"structure"`
		} `json:"workspace"`
	}

	if err := json.Unmarshal(data, &result); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse response: %v", err))
	}

	ws := result.Workspace
	var lines []string
	lines = append(lines, fmt.Sprintf("Workspace: %s (ID: %s)", ws.Name, ws.ID))
	lines = append(lines, fmt.Sprintf("Total Pages: %d", ws.Structure.TotalPages))

	if len(ws.Structure.Categories) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Categories:")
		for _, cat := range ws.Structure.Categories {
			lines = append(lines, fmt.Sprintf("  - %s (%d pages)", cat.Name, cat.PageCount))
		}
	}

	if len(ws.Structure.Tags) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Tags:")
		for _, tag := range ws.Structure.Tags {
			lines = append(lines, fmt.Sprintf("  - %s (%d pages)", tag.Name, tag.Count))
		}
	}

	output := strings.Join(lines, "\n")
	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
	}
}
