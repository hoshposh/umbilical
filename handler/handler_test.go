/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPClient records the last CallTool invocation and returns configurable content.
type mockMCPClient struct {
	calledName string
	calledArgs map[string]any
	// readContent is returned as a JSON-encoded read_note response.
	// Leave empty to simulate a file that does not yet exist.
	readContent string
}

func (m *mockMCPClient) CallTool(_ context.Context, name string, args map[string]any) ([]byte, error) {
	if name == "read_note" {
		if m.readContent == "" {
			// Simulate file-not-found: return an error so the handler treats the file as empty.
			return nil, &mockError{"file not found"}
		}
		// Return a JSON-encoded note response.
		data, _ := json.Marshal(map[string]any{"content": m.readContent, "fm": nil})
		return data, nil
	}
	// write_note: record args and succeed.
	m.calledName = name
	m.calledArgs = args
	return []byte("success"), nil
}

type mockError struct{ msg string }

func (e *mockError) Error() string { return e.msg }

func newHandler(readContent string) (*MessageHandler, *mockMCPClient) {
	m := &mockMCPClient{readContent: readContent}
	h := NewMessageHandler("/vault", m)
	h.Now = func() time.Time {
		return time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	}
	return h, m
}

const expectedHeading = "\n## 2026-04-06\n\n"

func TestHandle_Routes(t *testing.T) {
	t.Run("Note prefix routes to Inbox.md", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "!note Hello World")
		require.NoError(t, err)
		assert.Equal(t, "Inbox.md", result.DestFile)
		assert.Equal(t, "write_note", m.calledName)
		assert.Equal(t, "Inbox.md", m.calledArgs["path"])
		assert.Equal(t, expectedHeading+"Hello World\n", m.calledArgs["content"])
	})

	t.Run("Todo prefix routes to Tasks.md", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "!todo Buy milk")
		require.NoError(t, err)
		assert.Equal(t, "Tasks.md", result.DestFile)
		assert.Equal(t, "Tasks.md", m.calledArgs["path"])
		assert.Equal(t, expectedHeading+"- [ ] Buy milk\n", m.calledArgs["content"])
	})

	t.Run("Link prefix routes to Links.md", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "!link https://example.com")
		require.NoError(t, err)
		assert.Equal(t, "Links.md", result.DestFile)
		assert.Equal(t, "Links.md", m.calledArgs["path"])
		assert.Equal(t, expectedHeading+" - <https://example.com>\n", m.calledArgs["content"])
	})

	t.Run("Link prefix with spaces routes to Links.md and prepends list marker without brackets", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "!link [Go](https://go.dev)")
		require.NoError(t, err)
		assert.Equal(t, "Links.md", result.DestFile)
		assert.Equal(t, expectedHeading+" - [Go](https://go.dev)\n", m.calledArgs["content"])
	})

	t.Run("Link prefix with newline routes to Links.md as-is without brackets or markers", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "!link ### Title\nContent")
		require.NoError(t, err)
		assert.Equal(t, "Links.md", result.DestFile)
		assert.Equal(t, expectedHeading+"### Title\nContent\n", m.calledArgs["content"])
	})

	t.Run("Bare URL auto-detected as link", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "https://google.com")
		require.NoError(t, err)
		assert.Equal(t, "Links.md", result.DestFile)
		assert.Equal(t, expectedHeading+" - <https://google.com>\n", m.calledArgs["content"])
	})

	t.Run("No prefix routes to daily note", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "Just a random thought")
		require.NoError(t, err)
		assert.Equal(t, "Daily/2026-04-06.md", result.DestFile)
		assert.Equal(t, "Daily/2026-04-06.md", m.calledArgs["path"])
		assert.Equal(t, "Just a random thought\n", m.calledArgs["content"])
	})

	t.Run("Empty message is a no-op", func(t *testing.T) {
		h, m := newHandler("")
		result, err := h.Handle(context.Background(), "   ")
		require.NoError(t, err)
		assert.Empty(t, result.DestFile)
		assert.Empty(t, m.calledName)
	})
}

func TestHandle_HeadingInjection(t *testing.T) {
	t.Run("Heading injected on first write to non-daily file", func(t *testing.T) {
		h, m := newHandler("")
		_, err := h.Handle(context.Background(), "!note First note")
		require.NoError(t, err)
		content := m.calledArgs["content"].(string)
		assert.Contains(t, content, "## 2026-04-06", "date heading should be injected")
		assert.Contains(t, content, "First note")
	})

	t.Run("Heading not re-injected when already present in file", func(t *testing.T) {
		existing := "## 2026-04-06\n\nPrevious note"
		h, m := newHandler(existing)
		_, err := h.Handle(context.Background(), "!note Second note")
		require.NoError(t, err)
		// Mode should be append (no dedup occurred).
		assert.Equal(t, "append", m.calledArgs["mode"])
		// The written content fragment should NOT contain the heading — it's already in the file.
		content := m.calledArgs["content"].(string)
		assert.NotContains(t, content, "## 2026-04-06",
			"heading should not be re-injected when already present in the file")
		assert.Contains(t, content, "Second note")
	})
}

func TestHandle_Deduplication(t *testing.T) {
	t.Run("Duplicate entry is moved to bottom (overwrite mode)", func(t *testing.T) {
		// Simulate Inbox.md that already contains the todo item.
		existing := "## 2026-04-06\n\n- [ ] Buy milk\n\nOther item"
		h, m := newHandler(existing)
		_, err := h.Handle(context.Background(), "!todo Buy milk")
		require.NoError(t, err)

		// The write must be an overwrite (not append) since we modified existing content.
		assert.Equal(t, "overwrite", m.calledArgs["mode"],
			"mode should be overwrite when deduplicating")

		content := m.calledArgs["content"].(string)
		// The duplicate line should appear only once (moved to bottom).
		assert.Equal(t, 1, strings.Count(content, "- [ ] Buy milk"),
			"deduplicated item should appear exactly once")
		// The moved item should be at the end.
		idx := strings.LastIndex(content, "- [ ] Buy milk")
		idxOther := strings.Index(content, "Other item")
		assert.Greater(t, idx, idxOther, "moved item should be after remaining content")
	})
}
