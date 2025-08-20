package v0_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/modelcontextprotocol/registry/internal/api/router"
	"github.com/modelcontextprotocol/registry/internal/config"
	"github.com/modelcontextprotocol/registry/internal/model"
	"github.com/modelcontextprotocol/registry/internal/telemetry"
)

func mockServerEndpoint(registry *MockRegistryService) {
	servers := []model.Server{
		{
			ID:          "550e8400-e29b-41d4-a716-446655440001",
			Name:        "test-server-1",
			Description: "First test server",
			Repository: model.Repository{
				URL:    "https://github.com/example/test-server-1",
				Source: "github",
				ID:     "example/test-server-1",
			},
			VersionDetail: model.VersionDetail{
				Version:     "1.0.0",
				ReleaseDate: "2025-05-25T00:00:00Z",
				IsLatest:    true,
			},
		},
		{
			ID:          "550e8400-e29b-41d4-a716-446655440002",
			Name:        "test-server-2",
			Description: "Second test server",
			Repository: model.Repository{
				URL:    "https://github.com/example/test-server-2",
				Source: "github",
				ID:     "example/test-server-2",
			},
			VersionDetail: model.VersionDetail{
				Version:     "2.0.0",
				ReleaseDate: "2025-05-26T00:00:00Z",
				IsLatest:    true,
			},
		},
	}
	registry.Mock.On("List", "", 30).Return(servers, "", nil)
}

func TestPromtheusHandler(t *testing.T) {
	mockRegistry := new(MockRegistryService)
	mockServerEndpoint(mockRegistry)

	cfg := config.NewConfig()
	shutdownTelemetry, metrics, _ := telemetry.InitMetrics("dev")
	mux := http.NewServeMux()
	_ = router.NewHumaAPI(cfg, mockRegistry, mux, metrics)

	// Create request
	url := "/v0/servers"
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

	// Check if the response body contains expected metrics
	assert.Contains(t, w.Body.String(), "# HELP mcp_registry_http_requests_total Total number of HTTP requests")
}
