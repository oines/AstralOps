// Package api owns daemon HTTP and WebSocket delivery adapters.
//
// This package is intentionally kept behind command facades: it may parse
// transport requests and encode transport responses, but business decisions and
// state mutation must flow through daemon/internal/ports interfaces.
package api
