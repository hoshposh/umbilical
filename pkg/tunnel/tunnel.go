package tunnel

import (
	"context"
	"net"
)

// Tunnel defines the interface for an embedded ingress tunnel.
type Tunnel interface {
	// Start initializes the tunnel and returns a net.Listener and the public URL.
	Start(ctx context.Context) (net.Listener, string, error)
}
