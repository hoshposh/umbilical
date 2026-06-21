/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// MCPClient defines the interface for calling MCP tools
type MCPClient interface {
	CallTool(ctx context.Context, name string, args map[string]any) ([]byte, error)
}

// Result carries metadata about a completed Handle operation.
type Result struct {
	// DestFile is the vault-relative path of the file that was written.
	DestFile string
}

// MessageHandler handles routing incoming messages to the appropriate Obsidian file via MCP.
type MessageHandler struct {
	VaultPath string
	Now       func() time.Time
	MCPClient MCPClient
}

// NewMessageHandler creates a new MessageHandler.
func NewMessageHandler(vaultPath string, mcpClient MCPClient) *MessageHandler {
	return &MessageHandler{
		VaultPath: vaultPath,
		Now:       time.Now,
		MCPClient: mcpClient,
	}
}

// Handle processes a message and routes it to the correct note using MCP.
// It returns a Result describing where the content was written.
func (h *MessageHandler) Handle(ctx context.Context, msg string) (Result, error) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return Result{}, nil
	}

	var destFile string
	var content string

	if after, ok := strings.CutPrefix(msg, "!note "); ok {
		content = after
		destFile = "Inbox.md"
	} else if after, ok := strings.CutPrefix(msg, "!todo "); ok {
		content = "- [ ] " + after
		destFile = "Tasks.md"
	} else if after, ok := strings.CutPrefix(msg, "!link "); ok {
		if strings.Contains(after, "\n") {
			content = after
		} else if strings.Contains(after, " ") || strings.Contains(after, "[") {
			content = " - " + after
		} else {
			content = " - <" + after + ">"
		}
		destFile = "Links.md"
	} else if (strings.HasPrefix(msg, "http://") || strings.HasPrefix(msg, "https://")) && !strings.Contains(msg, " ") {
		content = " - <" + msg + ">"
		destFile = "Links.md"
	} else {
		content = msg
		dateStr := h.Now().Format("2006-01-02")
		// Assuming the MCP server is rooted at the vault path
		destFile = filepath.Join("Daily", dateStr+".md")
	}

	// Check if this is a daily note
	isDaily := strings.HasPrefix(destFile, "Daily")
	dateStr := h.Now().Format("2006-01-02")
	headingQuery := fmt.Sprintf("## %s", dateStr)

	// Keep track of our write mode (append vs overwrite)
	mode := "append"

	// Read existing file via MCP
	readArgs := map[string]any{"path": destFile}
	fileBytes, err := h.MCPClient.CallTool(ctx, "read_note", readArgs)
	fileStr := ""
	var fileFm map[string]any

	if err == nil {
		// mcpvault's read_note returns a JSON-encoded string combining frontmatter and content
		var parsedNote struct {
			Content string         `json:"content"`
			Fm      map[string]any `json:"fm"`
		}
		if parseErr := json.Unmarshal(fileBytes, &parsedNote); parseErr == nil {
			fileStr = parsedNote.Content
			fileFm = parsedNote.Fm
		} else {
			fileStr = string(fileBytes) // fallback
		}
	}

	// 1. Deduplicate & Move logic (skip for Daily notes)
	if !isDaily && fileStr != "" && strings.Contains(fileStr, strings.TrimSpace(content)) {
		// We found the content! Clean it out from the old document lines
		lines := strings.Split(fileStr, "\n")
		var cleaned []string
		for _, line := range lines {
			if strings.TrimSpace(line) != strings.TrimSpace(content) {
				cleaned = append(cleaned, line)
			}
		}
		fileStr = strings.Join(cleaned, "\n")
		mode = "overwrite" // Since we mutated the existing history, we must overwrite
	}

	// 2. Heading Injection logic
	if !isDaily {
		if fileStr == "" || !strings.Contains(fileStr, headingQuery) {
			content = fmt.Sprintf("\n%s\n\n%s", headingQuery, content)
		}
	}

	// 3. Final MCP push
	args := map[string]any{
		"path": destFile,
	}

	if len(fileFm) > 0 {
		args["frontmatter"] = fileFm
	}

	if mode == "overwrite" {
		// Append our updated content to the cleaned file memory
		args["content"] = strings.TrimSpace(fileStr) + "\n" + content + "\n"
		args["mode"] = "overwrite"
	} else {
		// Simple atomic append to the bottom of the file
		args["content"] = content + "\n"
		args["mode"] = "append"
	}

	_, err = h.MCPClient.CallTool(ctx, "write_note", args)
	if err != nil {
		return Result{}, fmt.Errorf("failed to call MCP write_note (%s) for %s: %w", mode, destFile, err)
	}

	return Result{DestFile: destFile}, nil
}
