package v0

import (
	"time"

	"github.com/modelcontextprotocol/registry/pkg/model"
)

// RegistryExtensions represents registry-generated metadata
type RegistryExtensions struct {
	ID          string    `json:"id"`
	PublishedAt time.Time `json:"published_at"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	IsLatest    bool      `json:"is_latest"`
	ReleaseDate string    `json:"release_date"`
}

// ServerRecord represents the unified storage and API response model
type ServerRecord struct {
	Server                          model.ServerJSON       `json:"server"`                                                    // Pure MCP server.json
	XIOModelContextProtocolRegistry RegistryExtensions     `json:"x-io.modelcontextprotocol.registry,omitempty"` // Registry-generated data
	XPublisher                      map[string]interface{} `json:"x-publisher,omitempty"`                       // x-publisher extensions
}

// ServerListResponse represents the paginated server list response
type ServerListResponse struct {
	Servers  []ServerRecord `json:"servers"`
	Metadata *Metadata      `json:"metadata,omitempty"`
}

// PublishRequest represents the API request format for publishing servers
type PublishRequest struct {
	Server     model.ServerJSON       `json:"server"`
	XPublisher map[string]interface{} `json:"x-publisher,omitempty"`
}

// Metadata represents pagination metadata
type Metadata struct {
	NextCursor string `json:"next_cursor,omitempty"`
	Count      int    `json:"count,omitempty"`
	Total      int    `json:"total,omitempty"`
}
