package hub

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/tailnet"
)

// This listener is published only on the Docker host's loopback. The host's
// Tailscale Serve forwards to it. The web listener does not mount these routes.
func (h *Hub) startAgentIngress() (*http.Server, error) {
	listener, err := net.Listen("tcp", ":8091")
	if err != nil {
		return nil, fmt.Errorf("start dedicated Agent listener: %w", err)
	}
	server := &http.Server{
		Handler:           h.agentIngressHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.Logger().Error("Agent listener stopped", "err", err)
		}
	}()
	return server, nil
}

func (h *Hub) agentIngressHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+tailnet.ReadyPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(tailnet.ReadyHeader, "agent")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET "+tailnet.ConnectPath, func(w http.ResponseWriter, r *http.Request) {
		request := agentConnectRequest{req: r, res: w, hub: h}
		_ = request.agentConnect()
	})
	return mux
}
