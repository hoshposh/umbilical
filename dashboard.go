/*
 * Copyright (c) 2026 Lyndon Washington
 * Licensed under the MIT License. See LICENSE in the project root for license information.
 */

package main

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

func printDashboard(bot, allowed, vault string, webhookPort int, webhookSecret string, syncRemote string, tunnelURL string) {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(lipgloss.Color("#7D56F4")).
		Padding(0, 1).
		MarginBottom(1)

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true).Width(12)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D1D1"))
	offStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7D56F4")).
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	title := titleStyle.Render("Umbilical")

	var rows string
	rows += labelStyle.Render("Bot User:") + valueStyle.Render(bot) + "\n"
	rows += labelStyle.Render("Allowed:") + valueStyle.Render(allowed) + "\n"
	rows += labelStyle.Render("Vault Path:") + valueStyle.Render(vault) + "\n"

	webhookStr := fmt.Sprintf("Port %d", webhookPort)
	if webhookSecret == "" {
		webhookStr += offStyle.Render(" (No Auth Secret Set)")
	} else {
		webhookStr += " (Secured)"
	}
	rows += labelStyle.Render("Webhooks:") + valueStyle.Render(webhookStr) + "\n"

	if tunnelURL != "" {
		rows += labelStyle.Render("Tunnel URL:") + valueStyle.Render(tunnelURL) + "\n"
	}

	if syncRemote != "" {
		rows += labelStyle.Render("Drive Sync:") + valueStyle.Render(syncRemote) + "\n"
	} else {
		rows += labelStyle.Render("Drive Sync:") + offStyle.Render("Disabled") + "\n"
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, rows)
	fmt.Println(borderStyle.Render(content))
}
