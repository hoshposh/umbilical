/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"charm.land/log/v2"
)

// MessageDispatcher routes incoming message text to the appropriate vault destination.
type MessageDispatcher interface {
	Handle(ctx context.Context, msg string) error
}

type FeedlyEntry struct {
	Title         string `json:"title"`
	EntryURL      string `json:"entryUrl"`
	Content       string `json:"content"`
	Author        string `json:"author"`
	PublishedDate int64  `json:"publishedDate"`
}

type GenericPayload struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Source  string `json:"source"`
}

type WebhookServer struct {
	Handler MessageDispatcher
	Secret  string
}

// FeedlyAuthMiddleware ensures the request has the correct HMAC-SHA256 signature.
func (ws *WebhookServer) FeedlyAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if ws.Secret != "" {
			// 1. Get the signature from the header
			signature := r.Header.Get("X-Feedly-Signature")
			if signature == "" {
				http.Error(w, "Missing signature", http.StatusUnauthorized)
				return
			}

			// 2. Read the raw body (important: keep it for the next handler)
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read body", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// 3. Calculate expected HMAC
			h := hmac.New(sha256.New, []byte(ws.Secret))
			h.Write(bodyBytes)
			expectedSignature := hex.EncodeToString(h.Sum(nil))

			// 4. Constant-time comparison to prevent timing attacks
			if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
				http.Error(w, "Invalid signature", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	}
}

func (ws *WebhookServer) FeedlyHandler(w http.ResponseWriter, r *http.Request) {
	var entry FeedlyEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Received Feedly Article: %s", entry.Title)

	// Map Feedly JSON into an Obsidian Markdown template (simulating a !link message)
	content := fmt.Sprintf("!link ### [%s](%s)\n**Author:** %s\n\n%s\n",
		entry.Title, entry.EntryURL, entry.Author, entry.Content)

	if err := ws.Handler.Handle(context.Background(), content); err != nil {
		log.Printf("Failed to process Feedly webhook: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GenericAuthMiddleware ensures the request has the correct Bearer token.
func (ws *WebhookServer) GenericAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if ws.Secret != "" {
			expectedToken := "Bearer " + ws.Secret
			token := r.Header.Get("Authorization")
			if token != expectedToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	}
}

func (ws *WebhookServer) GenericHandler(w http.ResponseWriter, r *http.Request) {
	var payload GenericPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Received Generic Webhook: %s", payload.Title)

	source := payload.Source
	if source == "" {
		source = "Webhook"
	}

	// Map generic JSON into an Obsidian Markdown template (simulating a !link message)
	content := fmt.Sprintf("!link ### [%s](%s)\n**Source:** %s\n\n%s\n",
		payload.Title, payload.URL, source, payload.Content)

	if err := ws.Handler.Handle(context.Background(), content); err != nil {
		log.Printf("Failed to process generic webhook: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func StartWebhookServer(port int, secret string, h MessageDispatcher, tunnelListener net.Listener) *http.Server {
	ws := &WebhookServer{
		Handler: h,
		Secret:  secret,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/feedly", ws.FeedlyAuthMiddleware(ws.FeedlyHandler))
	mux.HandleFunc("/webhooks/generic", ws.GenericAuthMiddleware(ws.GenericHandler))

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if tunnelListener != nil {
			go func() {
				log.Printf("Starting webhook server on tunnel")
				if err := srv.Serve(tunnelListener); err != nil && err != http.ErrServerClosed {
					log.Errorf("Webhook server (tunnel) failed: %v", err)
				}
			}()
		}
		
		log.Printf("Starting webhook server on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Webhook server failed: %v", err)
		}
	}()

	return srv
}
