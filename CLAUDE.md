# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Umbilical (module `github.com/hoshposh/umbilical`) is a headless Go service that bridges SimpleX Chat
messages and Feedly/generic webhooks into a local Obsidian vault, writing via the Model Context Protocol
(MCP) rather than touching vault files directly. See `README.md` for user-facing setup/flags and
`docs/DESIGN.md` for the full architecture writeup (including a Mermaid data-flow diagram) — read
`docs/DESIGN.md` before making non-trivial changes, since the roles/components below are its summary.

## Commands

This project uses [Task](https://taskfile.dev/) (`Taskfile.yml`) as its task runner.

```bash
task build   # go build -ldflags="-w -s" -o umbilical
task test    # go test ./... -v
task lint    # golangci-lint run ./...
task clean   # rm -f umbilical
```

Run a single test package/test directly with the standard Go toolchain, e.g.:

```bash
go test ./handler/... -run TestName -v
```

Tests exist for `handler/`, `mcp/`, and `pkg/simplex/`. CI (`.github/workflows/ci.yml`) runs
`golangci-lint` and `task build` + `task test` on every push/PR to `main`.

`gofmt`/`goimports` formatting is enforced by `.golangci.yml` (local-prefix
`github.com/hoshposh/umbilical`); run `task lint` before committing non-trivial changes.

## Architecture

### Deployment roles (`-role` flag)

The binary runs in one of three modes, chosen at startup in `main.go`, enabling a split-trust
architecture where the Obsidian vault is never directly internet-reachable:

- `standalone` (default): runs everything in one process — webhook server, SimpleX listener, MCP
  writes, Drive sync.
- `ingestor`: runs on a public/cloud host. Only accepts webhooks and relays payloads to the executor
  over SimpleX; never touches MCP or the vault.
- `executor`: runs locally behind the vault. Listens on SimpleX for payloads (from the ingestor or a
  paired mobile device) and performs the actual MCP vault writes.

`main.go` is the orchestrator: it parses CLI flags or a JSON `-config` file into a `Config` struct,
then wires up the role-appropriate subset of services under one cancellable `context.Context`,
shutting down cleanly on SIGINT/SIGTERM.

### Core components (by package)

- `pkg/simplex/client.go` — manages a `simplex-chat` CLI subprocess over its WebSocket API (no
  embeddable Go SDK exists for SimpleX). Boots the chat daemon, streams incoming messages
  (`Listen`), forwards ingestor→executor payloads (`Send`), and sends chat replies (`Reply`).
- `mcp/client.go` — minimal JSON-RPC 2.0 client over stdio to an MCP server subprocess (e.g.
  `@bitbonsai/mcpvault`), used for all vault reads/writes (`tools/call`). Vault I/O is deliberately
  never done via raw `os` file operations — this is the intentional Obsidian-writing boundary.
- `server/webhook.go` — `net/http` routes: `POST /webhooks/feedly` (HMAC-SHA256 auth) and
  `POST /webhooks/generic` (Bearer-token auth for Make.com/Zapier/IFTTT/etc.), both normalizing into
  the common message format handed to the dispatcher.
- `server/ingestor_dispatcher.go` — used only in the `ingestor` role; forwards webhook payloads to
  the executor's SimpleX address instead of writing locally.
- `handler/handler.go` — the content-routing rules shared by both SimpleX and webhook inputs:
  prefix-based routing (`!note`→`Inbox.md`, `!todo`→`Tasks.md`, `!link`→`Links.md`, bare URLs→
  `Links.md`, everything else→`Daily/YYYY-MM-DD.md`), per-day `## YYYY-MM-DD` heading grouping, and
  move-to-bottom deduplication (re-writing a duplicate line at the end instead of leaving it in place).
- `sync/drive_sync.go` — background ticker that shells out to `rclone sync` to mirror the vault's
  `/Research` subdirectory to a configured remote (e.g. Google Drive, for NotebookLM grounding).
- `pkg/tunnel/` — `Tunnel` interface (`Start(ctx) (net.Listener, string, error)`) for embedded
  ingress tunnels; `pkg/tunnel/ngrok/` is the current implementation using the ngrok Go SDK to expose
  the webhook server without opening inbound ports.
- `setup.go` — interactive TUI setup wizard (`charmbracelet/huh`) invoked via `-setup`; collects
  config and can auto-provision the SimpleX daemon, writing the result to
  `~/.config/umbilical/config.json`.
- `dashboard.go` — renders the `lipgloss`-styled startup status panel (bot/vault/webhook/tunnel/sync
  info) printed to the console on launch.

### Key conventions worth preserving

- Secrets live in the JSON config file (mode `0600`), not CLI flags, to avoid leaking them via
  `ps aux`.
- Vault mutation always goes through the MCP client, never direct file I/O — keep new
  read/write features on that path.
- Long-running subprocess integrations (`simplex-chat`, `rclone`) are shelled out rather than
  reimplemented in Go — follow that pattern for similar external integrations rather than adding a
  native SDK dependency.
