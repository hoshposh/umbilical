/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

// Package simplex provides a client for interacting with the simplex-chat CLI daemon
// over its WebSocket API. The daemon is started as a subprocess and controlled via
// JSON messages with correlation IDs.
package simplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"charm.land/log/v2"
	"github.com/gorilla/websocket"
)

// IncomingMessage represents a chat message received from a SimpleX contact.
type IncomingMessage struct {
	// SenderName is the SimpleX display name of the sender.
	SenderName string
	// Text is the plain-text body of the message.
	Text string
}

// Client manages a simplex-chat daemon subprocess and its WebSocket connection.
type Client struct {
	conn       *websocket.Conn
	cmd        *exec.Cmd
	profileDir string
	port       int
	corrID     atomic.Uint64
}

// NewClient starts a simplex-chat daemon for the given profile directory and port,
// connects via WebSocket, and initialises the chat layer. The caller is responsible
// for calling Close() when done.
func NewClient(ctx context.Context, profileDir string, port int) (*Client, error) {
	// Expand profile dir to absolute path so the daemon and log messages are unambiguous.
	absProfileDir, err := filepath.Abs(profileDir)
	if err != nil {
		return nil, fmt.Errorf("simplex: resolve profile dir: %w", err)
	}

	log.Infof("Starting simplex-chat daemon: profile=%s port=%d", absProfileDir, port)

	cmd := exec.CommandContext(ctx, "simplex-chat", //nolint:gosec // fixed binary; arguments are config values, not user input
		"-d", absProfileDir,
		"-p", strconv.Itoa(port),
		"-m", // maintenance mode: WebSocket up before chat, prevents TTY crash
	)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("simplex: start daemon: %w", err)
	}

	c := &Client{
		cmd:        cmd,
		profileDir: absProfileDir,
		port:       port,
	}

	// Attempt to connect; the daemon needs a moment to bind the WebSocket port.
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	log.Infof("Waiting for simplex-chat WebSocket on %s...", wsURL)

	for i := 0; i < 15; i++ {
		var resp *http.Response
		c.conn, resp, err = websocket.DefaultDialer.Dial(wsURL, nil)
		if resp != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Debugf("simplex: dial response body close: %v", closeErr)
			}
		}
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			log.Warnf("simplex: kill daemon after connect failure: %v", killErr)
		}
		return nil, fmt.Errorf("simplex: connect WebSocket after 15s: %w", err)
	}

	// Boot the chat layer (required in maintenance mode).
	if err := c.sendCmd("/_start"); err != nil {
		if closeErr := c.Close(); closeErr != nil {
			log.Warnf("simplex: close after start failure: %v", closeErr)
		}
		return nil, fmt.Errorf("simplex: send /_start: %w", err)
	}

	// Allow the chat layer to fully initialise before accepting traffic.
	time.Sleep(2 * time.Second)

	// Enable auto-accept so Android/ingestor contact requests are accepted automatically.
	// Without this, incoming connection requests hang waiting for manual approval.
	if err := c.sendCmd("/auto_accept on"); err != nil {
		log.Warnf("simplex: could not enable auto-accept: %v", err)
	}

	log.Infof("simplex-chat ready (auto-accept enabled)")

	return c, nil
}

// nextCorrID returns a unique correlation ID string for a WebSocket request.
func (c *Client) nextCorrID() string {
	return strconv.FormatUint(c.corrID.Add(1), 10)
}

// sendCmd sends a raw chat command to the daemon.
func (c *Client) sendCmd(cmd string) error {
	return c.conn.WriteJSON(map[string]interface{}{
		"corrId": c.nextCorrID(),
		"cmd":    cmd,
	})
}

// rawResponse is the top-level envelope of every WebSocket message from simplex-chat.
type rawResponse struct {
	CorrID string          `json:"corrId"`
	Resp   json.RawMessage `json:"resp"`
}

// Listen reads incoming chat messages from the WebSocket in a loop, calling handler
// for each message that contains a plain-text body. It returns when ctx is cancelled
// or the connection drops.
//
// The event shape for new messages from simplex-chat v6 is:
//
//	{
//	  "corrId": null,
//	  "resp": {
//	    "type": "newChatItems",
//	    "chatItems": [{
//	      "chatInfo": { "type": "direct", "contact": { "localDisplayName": "hoshposh" } },
//	      "chatItem": {
//	        "chatDir": { "type": "directRcv" },
//	        "content": { "type": "rcvMsgContent", "msgContent": { "type": "text", "text": "hello" } }
//	      }
//	    }]
//	  }
//	}
func (c *Client) Listen(ctx context.Context, handler func(ctx context.Context, msg IncomingMessage)) error {
	log.Infof("simplex: listen loop started")
	for {
		_, msgBytes, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("simplex: read: %w", err)
		}

		log.Debugf("simplex: raw event: %s", string(msgBytes))

		var raw rawResponse
		if err := json.Unmarshal(msgBytes, &raw); err != nil {
			log.Warnf("simplex: failed to parse envelope: %v", err)
			continue
		}

		// Only process unsolicited server events (corrId is absent or empty).
		if raw.CorrID != "" {
			continue
		}

		// Decode just enough of the event to check its type and extract message data.
		var event struct {
			Type      string `json:"type"`
			ChatItems []struct {
				ChatInfo struct {
					Type    string `json:"type"`
					Contact struct {
						LocalDisplayName string `json:"localDisplayName"`
						Profile          struct {
							DisplayName string `json:"displayName"`
						} `json:"profile"`
					} `json:"contact"`
				} `json:"chatInfo"`
				ChatItem struct {
					ChatDir struct {
						Type string `json:"type"` // "directRcv" for received DMs
					} `json:"chatDir"`
					Content struct {
						Type       string `json:"type"` // "rcvMsgContent"
						MsgContent struct {
							Type string `json:"type"` // "text"
							Text string `json:"text"`
						} `json:"msgContent"`
					} `json:"content"`
				} `json:"chatItem"`
			} `json:"chatItems"`
		}

		if err := json.Unmarshal(raw.Resp, &event); err != nil {
			// Silently skip events we don't understand.
			continue
		}

		if event.Type != "newChatItems" {
			continue
		}

		for _, item := range event.ChatItems {
			dir := item.ChatItem.ChatDir.Type
			contentType := item.ChatItem.Content.Type
			msgType := item.ChatItem.Content.MsgContent.Type

			// Only handle direct received plain-text messages.
			if dir != "directRcv" || contentType != "rcvMsgContent" || msgType != "text" {
				continue
			}

			// Use profile.displayName (the user-visible name) rather than
			// localDisplayName which SimpleX deduplicates with _1, _2 suffixes.
			sender := item.ChatInfo.Contact.Profile.DisplayName
			if sender == "" {
				// Fallback to localDisplayName if profile name is missing.
				sender = item.ChatInfo.Contact.LocalDisplayName
			}
			text := item.ChatItem.Content.MsgContent.Text

			if sender == "" || text == "" {
				continue
			}

			handler(ctx, IncomingMessage{SenderName: sender, Text: text})
		}
	}
}

// Send connects to a remote SimpleX address (e.g. an Executor) and delivers a
// one-shot message. It uses an ephemeral strategy: connect, wait for acceptance,
// send, then leave the connection in place for the daemon to manage.
//
// This is intended for the Ingestor role, which forwards webhook payloads to a
// remote Executor over SimpleX's E2E encrypted transport.
func (c *Client) Send(address string, message string) error {
	log.Infof("simplex: connecting to executor at %s", address)

	if err := c.sendCmd("/connect " + address); err != nil {
		return fmt.Errorf("simplex: send /connect: %w", err)
	}

	// Wait for the contactConnected event that tells us the contact was accepted
	// and gives us the display name we can address the follow-up message to.
	if err := c.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("simplex: set read deadline: %w", err)
	}
	defer func() {
		// Clear the deadline so the shared connection is not affected after Send returns.
		_ = c.conn.SetReadDeadline(time.Time{})
	}()

	var contactName string
	for contactName == "" {
		var raw rawResponse
		if err := c.conn.ReadJSON(&raw); err != nil {
			return fmt.Errorf("simplex: waiting for contactConnected: %w", err)
		}
		var event struct {
			Type    string `json:"type"`
			Contact struct {
				LocalDisplayName string `json:"localDisplayName"`
			} `json:"contact"`
		}
		if err := json.Unmarshal(raw.Resp, &event); err != nil {
			continue
		}
		if event.Type == "contactConnected" && event.Contact.LocalDisplayName != "" {
			contactName = event.Contact.LocalDisplayName
		}
	}

	log.Infof("simplex: executor contact established as %q, sending payload", contactName)
	return c.Reply(contactName, message)
}

// Reply sends a message to an existing SimpleX contact identified by their
// local display name. It is used for both bot acknowledgements (standalone/executor
// roles) and for delivering payloads to the Executor (ingestor role via Send).
func (c *Client) Reply(contactName string, message string) error {
	cmd := fmt.Sprintf("/@ %s %s", contactName, message)
	if err := c.sendCmd(cmd); err != nil {
		return fmt.Errorf("simplex: reply to %s: %w", contactName, err)
	}
	return nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			log.Warnf("simplex: websocket close: %v", err)
		}
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}
