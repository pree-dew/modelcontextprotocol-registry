package v0_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/modelcontextprotocol/registry/internal/api/router"
	"github.com/modelcontextprotocol/registry/internal/config"
	"github.com/modelcontextprotocol/registry/internal/model"
	"github.com/modelcontextprotocol/registry/internal/telemetry"
)

func mockServerEndpoint(registry *MockRegistryService, serverID string) {
	serverDetail := &model.ServerDetail{
		Server: model.Server{
			ID:          serverID,
			Name:        "test-server-detail",
			Description: "Test server detail",
			Repository: model.Repository{
				URL:    "https://github.com/example/test-server-detail",
				Source: "github",
				ID:     "example/test-server-detail",
			},
			VersionDetail: model.VersionDetail{
				Version:     "2.0.0",
				ReleaseDate: "2025-05-27T12:00:00Z",
				IsLatest:    true,
			},
		},
	}
	registry.Mock.On("GetByID", serverID).Return(serverDetail, nil)
}

func TestPrometheusHandler(t *testing.T) {
	mockRegistry := new(MockRegistryService)

	serverID := uuid.New().String()
	mockServerEndpoint(mockRegistry, serverID)

	cfg := config.NewConfig()
	shutdownTelemetry, metrics, _ := telemetry.InitMetrics("dev")
	mux := http.NewServeMux()
	_ = router.NewHumaAPI(cfg, mockRegistry, mux, metrics)

	// Create request
	url := "/v0/servers/" + serverID
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()

	// Serve the request
	mux.ServeHTTP(w, req)

	// Check the status code
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// shutdown metrics provider
	_ = shutdownTelemetry(context.Background())

	assert.Equal(t, http.StatusOK, w.Code, "Expected status OK for /metrics endpoint")

	body := w.Body.String()
	// Check if the response body contains expected metrics
	assert.Contains(t, body, "mcp_registry_http_request_duration_bucket")
	assert.Contains(t, body, "mcp_registry_http_requests_total")
	assert.Contains(t, body, "path=\"/v0/servers/{id}\"")
}
