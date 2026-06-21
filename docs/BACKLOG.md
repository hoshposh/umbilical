# Umbilical Feature Backlog

This document tracks planned features, architectural improvements, and ideas for future prioritization.

## Media & Attachment Handling
- Enhance the executor to detect file and image attachments sent via SimpleX.
- Save detected attachments to the Obsidian vault's designated attachment folder.
- Inject standard Markdown embed links (e.g., `![[filename.png]]`) into the created note via the MCP client.

## AI Processing / Transcription Pipeline
- Implement an interception layer for voice notes received over SimpleX.
- Pipe audio through a local Whisper model or an API (e.g., Groq) for transcription.
- Write the resulting text transcription directly to the vault.

## Payload Transformation Engine (Template/jq)
- Replace hardcoded webhook logic (e.g., Feedly, generic) with a configuration-driven transformation layer.
- Allow users to define `jq` expressions or Go `text/template` formats in their configuration.
- Enable normalization of payloads from arbitrary external services (GitHub, Stripe, custom scripts) into Umbilical's internal message format without recompiling the binary.

## Resiliency & Buffering
- Introduce a persistent, lightweight queue (such as an embedded SQLite database using `crawshaw.io/sqlite` or a simple disk-backed queue) for the ingestor.
- Ensure rapid `200 OK` responses to webhook providers and prevent data loss during SimpleX connection drops, network blips, or throttling.

## Rate Limiting & Abuse Prevention
- Implement IP-based or token-based rate limiting at the HTTP gateway level (using libraries like `golang.org/x/time/rate`).
- Prevent potential flooding and vault spam if the webhook URL is leaked.

## ~~Embedded Zero-Trust Tunnels~~ (Completed)
- ~~Embed a zero-trust tunnel SDK (like Cloudflare's `cloudflared` SDK or ngrok's Go SDK) directly into the standalone binary.~~
- ~~Allow users to instantly expose a secure HTTPS webhook URL without the need for complex external network infrastructure or reverse proxies.~~

## Complex Formatting Templates
- Implement support for user-defined complex formatting templates.
- Standardize the formatting of ingested content (e.g., how a Feedly article is formatted with specific YAML frontmatter tags) before the payload is passed to the MCP client for writing.
