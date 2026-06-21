/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
	"charm.land/log/v2"
	"github.com/gorilla/websocket"
)

func runSetup(initialConfigPath string) {
	var c Config
	var enableWebhooks bool
	var enableSync bool
	var autoProvision bool

	homeDir, _ := os.UserHomeDir()
	defaultConfigPath := filepath.Join(homeDir, ".config", "umbilical", "config.json")
	var configPath = defaultConfigPath

	if initialConfigPath != "" {
		configPath = initialConfigPath
	}

	if c.Role == "" {
		c.Role = "standalone"
	}

	if data, err := os.ReadFile(configPath); err == nil { //nolint:gosec // configPath is from user input or a known default
		if err := json.Unmarshal(data, &c); err == nil {
			log.Infof("Pre-filling wizard with existing config from %s", configPath)
			if c.Role == "" {
				c.Role = "standalone"
			}
			if c.WebhookPort != 0 || c.WebhookSecret != "" {
				enableWebhooks = true
			}
			if c.SyncRemote != "" || c.SyncIntervalMinutes != 0 {
				enableSync = true
			}
		}
	}

	var webhookPortStr = "8080"
	if c.WebhookPort != 0 {
		webhookPortStr = strconv.Itoa(c.WebhookPort)
	}
	var syncIntervalStr = "15"
	if c.SyncIntervalMinutes != 0 {
		syncIntervalStr = strconv.Itoa(c.SyncIntervalMinutes)
	}

	log.Print("Welcome to the Umbilical Setup Wizard!")

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Deployment Role").
				Description("Select how this bot instance will run").
				Options(
					huh.NewOption("Standalone (Both Webhooks + Executor)", "standalone"),
					huh.NewOption("Cloud Ingestor (Webhooks to SimpleX Relay)", "ingestor"),
					huh.NewOption("Local Executor (SimpleX to Vault)", "executor"),
				).
				Value(&c.Role),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Auto-Provision SimpleX Chat Background Daemon?").
				Description("I can automatically launch simplex-chat and generate a profile + address.").
				Value(&autoProvision),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("SimpleX Bot Profile Name").
				Description("Profile directory name for the bot (e.g. 'mybot')").
				Value(&c.BotProfile).
				Validate(func(s string) error {
					if len(s) < 2 || len(s) > 15 {
						return fmt.Errorf("profile must be between 2 and 15 characters")
					}
					if !regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(s) {
						return fmt.Errorf("profile can only contain letters, numbers, and underscores")
					}
					return nil
				}),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Allowed Sender Display Name").
				Description("SimpleX sender allowed to trigger commands (your phone)").
				Value(&c.AllowedSender).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("allowed sender cannot be empty")
					}
					return nil
				}),
		).WithHideFunc(func() bool {
			return c.Role == "executor"
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("Executor SimpleX Address").
				Description("The contact address of the executor (https://smp... or https://simplex.chat/contact...)").
				Value(&c.ExecutorAddress).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "https://") {
						return fmt.Errorf("address must be a SimpleX contact link starting with https://")
					}
					return nil
				}),
		).WithHideFunc(func() bool {
			return c.Role == "standalone" || c.Role == "executor"
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("Obsidian Vault Path").
				Description("Absolute path to your Obsidian vault").
				Value(&c.VaultPath).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("vault path cannot be empty")
					}
					return nil
				}),
		).WithHideFunc(func() bool {
			return c.Role == "ingestor"
		}),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable Webhooks?").
				Description("This allows tools like Feedly to send links to your vault").
				Value(&enableWebhooks),
		).WithHideFunc(func() bool {
			return c.Role == "executor" // Executors don't do webhooks
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("Webhook Port").
				Description("Port to listen for webhooks").
				Value(&webhookPortStr).
				Validate(func(s string) error {
					_, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("must be a valid number")
					}
					return nil
				}),
			huh.NewInput().
				Title("Webhook Secret").
				Description("A Bearer token for authenticating webhook requests").
				Value(&c.WebhookSecret),
		).WithHideFunc(func() bool {
			return c.Role == "executor" || !enableWebhooks
		}),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable ngrok Tunnel?").
				Description("Expose your local webhook server to the internet using an embedded ngrok tunnel").
				Value(&c.TunnelEnabled),
		).WithHideFunc(func() bool {
			return c.Role == "executor" || !enableWebhooks
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("ngrok Auth Token").
				Description("Your ngrok authtoken from the ngrok dashboard").
				Value(&c.NgrokAuthToken).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("ngrok auth token cannot be empty if tunnel is enabled")
					}
					return nil
				}),
			huh.NewInput().
				Title("ngrok Custom Domain").
				Description("Optional: custom ngrok domain (e.g., your-app.ngrok-free.app)").
				Value(&c.NgrokDomain),
		).WithHideFunc(func() bool {
			return c.Role == "executor" || !enableWebhooks || !c.TunnelEnabled
		}),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable Google Drive Sync?").
				Description("Automatically sync a Research folder via rclone").
				Value(&enableSync),
		).WithHideFunc(func() bool {
			return c.Role == "ingestor" // Ingestors don't do sync or vault
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("Sync Remote").
				Description("rclone remote path (e.g. gdrive:Research)").
				Value(&c.SyncRemote),
			huh.NewInput().
				Title("Sync Interval (Minutes)").
				Value(&syncIntervalStr).
				Validate(func(s string) error {
					_, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("must be a valid number")
					}
					return nil
				}),
		).WithHideFunc(func() bool {
			return c.Role == "ingestor" || !enableSync
		}),
		huh.NewGroup(
			huh.NewInput().
				Title("Config Save Path").
				Description("Where should we save this config file?").
				Value(&configPath),
		),
	)

	err := form.Run()
	if err != nil {
		log.Fatalf("Setup aborted: %v", err)
	}

	if enableWebhooks {
		port, _ := strconv.Atoi(webhookPortStr)
		c.WebhookPort = port
	}
	if enableSync {
		interval, _ := strconv.Atoi(syncIntervalStr)
		c.SyncIntervalMinutes = interval
	}

	if c.Role == "ingestor" || c.Role == "executor" || c.Role == "standalone" {
		if c.SimplexPort == 0 {
			c.SimplexPort = 5225
		}
	}

	if autoProvision {
		log.Infof("Auto-provisioning SimpleX Chat background daemon...")
		err := provisionSimpleXBot(&c)
		if err != nil {
			log.Fatalf("Failed to provision bot: %v", err)
		}
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal config: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0750); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}
	err = os.WriteFile(configPath, data, 0600)
	if err != nil {
		log.Fatalf("Failed to write config file to %s: %v", configPath, err)
	}

	log.Infof("Successfully saved config to %s!", configPath)
	log.Infof("You can now run the bot using: ./umbilical -config=%s", configPath)
	os.Exit(0)
}

func provisionSimpleXBot(c *Config) error {
	log.Infof("Checking if simplex-chat is installed...")
	if _, err := exec.LookPath("simplex-chat"); err != nil {
		return fmt.Errorf("simplex-chat CLI not found. Please install: curl -o- https://raw.githubusercontent.com/simplex-chat/simplex-chat/stable/install.sh | bash")
	}

	homeDir, _ := os.UserHomeDir()
	profileDir := filepath.Join(homeDir, ".config", "umbilical", "profiles", c.BotProfile)

	log.Infof("Starting SimpleX daemon for profile %s on port %d...", c.BotProfile, c.SimplexPort)

	// Use maintenance mode (-m) so the WebSocket server is ready before chat is started.
	// This avoids a race between the WebSocket binding and initial profile setup.
	cmd := exec.CommandContext(context.Background(), "simplex-chat", //nolint:gosec // arguments are validated config values
		"-d", profileDir,
		"-p", strconv.Itoa(c.SimplexPort),
		"--create-bot-display-name", c.BotProfile,
		"-m", // maintenance mode: /_start required to actually boot chat layer
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start simplex-chat daemon: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil {
				log.Warnf("simplex-chat process kill: %v", err)
			}
		}
	}()

	dialer := websocket.DefaultDialer
	var conn *websocket.Conn
	var err error
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", c.SimplexPort)

	log.Infof("Waiting for WebSocket server to become ready...")
	for i := 0; i < 15; i++ {
		var resp *http.Response
		conn, resp, err = dialer.Dial(wsURL, nil)
		if resp != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				log.Debugf("provisioner: dial response body close: %v", closeErr)
			}
		}
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("failed to connect to daemon websocket after 15s: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Warnf("websocket close: %v", err)
		}
	}()

	sendCmd := func(corrId, cmd string) error {
		return conn.WriteJSON(map[string]interface{}{
			"corrId": corrId,
			"cmd":    cmd,
		})
	}

	// 1. Boot the chat layer (required after -m maintenance mode).
	log.Infof("Starting chat layer...")
	if err := sendCmd("start", "/_start"); err != nil {
		return fmt.Errorf("failed to send /_start: %v", err)
	}
	// Give chat layer a moment to fully initialize.
	time.Sleep(3 * time.Second)

	if c.Role == "standalone" || c.Role == "executor" {
		// 2. Show the existing long-term contact address.
		log.Infof("Requesting long-term contact address (/sa)...")
		if err := sendCmd("getaddr", "/sa"); err != nil {
			return fmt.Errorf("failed to send /sa: %v", err)
		}

		// Match both short (https://smp...) and long (https://simplex.chat/contact...) address formats.
		smpRegex := regexp.MustCompile(`https://(?:smp[^\s"'<>]+|simplex\.chat/contact[^\s"'<>]+)`)
		addressFound := false

		// Read a stream of events; ignore non-matching noise, stop on address or deadline.
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}
		for !addressFound {
			var resp map[string]interface{}
			if err := conn.ReadJSON(&resp); err != nil {
				// Deadline exceeded or connection closed — no address was found.
				break
			}

			raw, _ := json.Marshal(resp)
			if match := smpRegex.FindString(string(raw)); match != "" {
				addressFound = true
				fmt.Println("\n============================================================")
				fmt.Println("🚀 Executor SimpleX Address generated:")
				fmt.Printf("\n  %s\n\n", match)
				fmt.Println("Save this address — you will need it to configure ingestors")
				fmt.Println("and to add this bot from your Android SimpleX client.")
				fmt.Println("============================================================")
			}
		}

		if !addressFound {
			// Fall back to manual instructions using the interactive (no -p) mode.
			log.Warn("Could not extract address automatically.")
			log.Warnf("Run manually to get your address:")
			log.Warnf("  simplex-chat -d %s", profileDir)
			log.Warnf("  Then type: /sa")
		}
	} else {
		log.Infof("SimpleX daemon initialized successfully for ingestor role.")
	}

	log.Infof("Bot auto-provisioning completed successfully!")
	return nil
}
