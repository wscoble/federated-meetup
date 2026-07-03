// SPDX-License-Identifier: AGPL-3.0
//
// HTTP wiring helpers for the MCP server and discovery endpoints.
// This file provides a convenience function to register all discovery
// + MCP endpoints on an existing http.ServeMux.
package mcp

import (
	"net/http"

	"github.com/sscoble/federated-meetup/internal/host"
)

// RegisterEndpoints registers all AIEO discovery endpoints and the MCP
// server on the given http.ServeMux. This is the single call a host
// daemon makes to wire in the discovery layer alongside ConnectRPC.
//
// Endpoints registered:
//   - POST /mcp                       — MCP streamable-http transport
//   - GET  /.well-known/federation    — host discovery JSON
//   - GET  /llms.txt                  — LLM-readable host summary
//   - GET  /openapi.json              — OpenAPI 3.1 spec
//   - GET  /robots.txt                — robots.txt with AI crawler allowlist
func RegisterEndpoints(mux *http.ServeMux, svc *host.Service, cfg HostConfig) *Server {
	// Create the MCP server.
	mcpServer := NewServer(svc, cfg)

	// Register the MCP streamable-http handler at /mcp.
	httpHandler := mcpServer.HTTPHandler()
	mux.Handle("/mcp", &httpHandler)

	// Register discovery endpoints.
	disc := NewDiscoveryHandler(svc, cfg)
	mux.Handle("/.well-known/federation", disc)
	mux.Handle("/llms.txt", disc)
	mux.Handle("/openapi.json", disc)
	mux.Handle("/robots.txt", disc)

	return mcpServer
}