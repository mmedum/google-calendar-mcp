package server_test

import "github.com/modelcontextprotocol/go-sdk/mcp"

func newTransports() (mcp.Transport, mcp.Transport) { return mcp.NewInMemoryTransports() }

func newClient() *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
}
