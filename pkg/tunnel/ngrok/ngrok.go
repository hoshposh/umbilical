package ngrok

import (
	"context"
	"fmt"
	"net"

	"golang.ngrok.com/ngrok/v2"

	"github.com/hoshposh/umbilical/pkg/tunnel"
)

type ngrokTunnel struct {
	authToken string
	domain    string
}

// New creates a new Tunnel implementation using ngrok.
func New(authToken, domain string) tunnel.Tunnel {
	return &ngrokTunnel{
		authToken: authToken,
		domain:    domain,
	}
}

func (t *ngrokTunnel) Start(ctx context.Context) (net.Listener, string, error) {
	agentOpts := []ngrok.AgentOption{}
	if t.authToken != "" {
		agentOpts = append(agentOpts, ngrok.WithAuthtoken(t.authToken))
	}

	agent, err := ngrok.NewAgent(agentOpts...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create ngrok agent: %w", err)
	}

	endpointOpts := []ngrok.EndpointOption{}
	if t.domain != "" {
		endpointOpts = append(endpointOpts, ngrok.WithURL("https://"+t.domain))
	}

	tun, err := agent.Listen(ctx, endpointOpts...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to start ngrok tunnel: %w", err)
	}

	return tun, tun.URL().String(), nil
}
