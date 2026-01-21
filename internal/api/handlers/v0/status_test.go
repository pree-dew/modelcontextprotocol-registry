package v0_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v0 "github.com/modelcontextprotocol/registry/internal/api/handlers/v0"
	"github.com/modelcontextprotocol/registry/internal/auth"
	"github.com/modelcontextprotocol/registry/internal/config"
	"github.com/modelcontextprotocol/registry/internal/database"
	"github.com/modelcontextprotocol/registry/internal/service"
	apiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"github.com/modelcontextprotocol/registry/pkg/model"
)

func TestUpdateServerStatusEndpoint(t *testing.T) {
	// Create test config
	testSeed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(testSeed)
	require.NoError(t, err)
	cfg := &config.Config{
		JWTPrivateKey:            hex.EncodeToString(testSeed),
		EnableRegistryValidation: false,
	}

	// Create registry service and test data
	registryService := service.NewRegistryService(database.NewTestDB(t), cfg)

	// Create test servers for different scenarios
	testServers := map[string]*apiv0.ServerJSON{
		"active": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/active-server",
			Description: "Server in active status",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/active-server",
				Source: "github",
				ID:     "testuser/active-server",
			},
		},
		"deprecated": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/deprecated-server",
			Description: "Server in deprecated status",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/deprecated-server",
				Source: "github",
				ID:     "testuser/deprecated-server",
			},
		},
		"yanked": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/yanked-server",
			Description: "Server in yanked status",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/yanked-server",
				Source: "github",
				ID:     "testuser/yanked-server",
			},
		},
		"other": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.otheruser/other-server",
			Description: "Server owned by another user",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/otheruser/other-server",
				Source: "github",
				ID:     "otheruser/other-server",
			},
		},
		"newserver": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/new-server",
			Description: "New server for renaming tests",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/new-server",
				Source: "github",
				ID:     "testuser/new-server",
			},
		},
		"newname-active-test": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/newname-active-test",
			Description: "Server for testing newName with active status",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/newname-active-test",
				Source: "github",
				ID:     "testuser/newname-active-test",
			},
		},
		"multi-version": {
			Schema:      model.CurrentSchemaURL,
			Name:        "io.github.testuser/multi-version-server",
			Description: "Server with multiple versions for testing",
			Version:     "1.0.0",
			Repository: &model.Repository{
				URL:    "https://github.com/testuser/multi-version-server",
				Source: "github",
				ID:     "testuser/multi-version-server",
			},
		},
	}

	// Create the test servers
	for _, server := range testServers {
		_, err := registryService.CreateServer(context.Background(), server)
		require.NoError(t, err)
	}

	// Set deprecated server to deprecated status
	_, err = registryService.UpdateServerStatus(context.Background(), testServers["deprecated"].Name, testServers["deprecated"].Version, &service.StatusChangeRequest{
		NewStatus: model.StatusDeprecated,
	})
	require.NoError(t, err)

	// Set yanked server to yanked status
	_, err = registryService.UpdateServerStatus(context.Background(), testServers["yanked"].Name, testServers["yanked"].Version, &service.StatusChangeRequest{
		NewStatus: model.StatusYanked,
	})
	require.NoError(t, err)

	// Set newname-active-test server to deprecated for the newName rejection test
	_, err = registryService.UpdateServerStatus(context.Background(), testServers["newname-active-test"].Name, testServers["newname-active-test"].Version, &service.StatusChangeRequest{
		NewStatus: model.StatusDeprecated,
	})
	require.NoError(t, err)

	// Add a second version to multi-version server
	multiVersionV2 := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.testuser/multi-version-server",
		Description: "Server with multiple versions for testing",
		Version:     "2.0.0",
		Repository: &model.Repository{
			URL:    "https://github.com/testuser/multi-version-server",
			Source: "github",
			ID:     "testuser/multi-version-server",
		},
	}
	_, err = registryService.CreateServer(context.Background(), multiVersionV2)
	require.NoError(t, err)

	testCases := []struct {
		name           string
		serverName     string
		version        string
		authClaims     *auth.JWTClaims
		authHeader     string
		requestBody    v0.UpdateServerStatusBody
		expectedStatus int
		expectedError  string
		checkResult    func(*testing.T, *apiv0.ServerResponse)
	}{
		{
			name:       "successful status change from active to deprecated",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusDeprecated, resp.Meta.Official.Status)
			},
		},
		{
			name:       "successful status change with message and alternative URL",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:         "yanked",
				StatusMessage:  strPtr("Security vulnerability discovered"),
				AlternativeURL: strPtr("https://example.com/patched-version"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusYanked, resp.Meta.Official.Status)
				assert.NotNil(t, resp.Meta.Official.StatusMessage)
				assert.Equal(t, "Security vulnerability discovered", *resp.Meta.Official.StatusMessage)
				assert.NotNil(t, resp.Meta.Official.AlternativeURL)
				assert.Equal(t, "https://example.com/patched-version", *resp.Meta.Official.AlternativeURL)
			},
		},
		{
			name:       "successful unyank from yanked to active",
			serverName: "io.github.testuser/yanked-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "active",
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusActive, resp.Meta.Official.Status)
				// Status message and alternative URL should be cleared when transitioning to active
				assert.Nil(t, resp.Meta.Official.StatusMessage)
				assert.Nil(t, resp.Meta.Official.AlternativeURL)
			},
		},
		{
			name:       "successful undeprecate from deprecated to active",
			serverName: "io.github.testuser/deprecated-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "active",
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusActive, resp.Meta.Official.Status)
			},
		},
		{
			name:           "missing authorization header",
			serverName:     "io.github.testuser/active-server",
			version:        "1.0.0",
			authHeader:     "",
			requestBody:    v0.UpdateServerStatusBody{Status: "deprecated"},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  "required header parameter is missing",
		},
		{
			name:       "invalid authorization header format",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authHeader: "InvalidFormat token123",
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "Invalid Authorization header format",
		},
		{
			name:       "invalid token",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authHeader: "Bearer invalid-token",
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "Invalid or expired Registry JWT token",
		},
		{
			name:       "permission denied - no edit permissions",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "You do not have edit permissions",
		},
		{
			name:       "permission denied - wrong namespace",
			serverName: "io.github.otheruser/other-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "You do not have edit permissions",
		},
		{
			name:       "server not found",
			serverName: "io.github.testuser/non-existent",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
		{
			name:       "newName with valid deprecated status and permissions",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/new-server"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:        "deprecated",
				StatusMessage: strPtr("Moved to new server"),
				NewName:       strPtr("io.github.testuser/new-server"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusDeprecated, resp.Meta.Official.Status)
				assert.NotNil(t, resp.Meta.Official.NewName)
				assert.Equal(t, "io.github.testuser/new-server", *resp.Meta.Official.NewName)
			},
		},
		{
			name:       "newName rejected when transitioning to active",
			serverName: "io.github.testuser/newname-active-test",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "active",
				NewName: strPtr("io.github.testuser/new-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "new_name can only be used with deprecated or yanked status",
		},
		{
			name:       "newName rejected when same as current server name",
			serverName: "io.github.testuser/active-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.testuser/active-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "new_name cannot be the same as the current server name",
		},
		{
			name:       "newName with non-existent server",
			serverName: "io.github.testuser/deprecated-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "yanked",
				NewName: strPtr("io.github.testuser/non-existent-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "New server 'io.github.testuser/non-existent-server' does not exist in the registry",
		},
		{
			name:       "newName with server belonging to different user without permissions",
			serverName: "io.github.testuser/deprecated-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "yanked",
				NewName: strPtr("io.github.otheruser/other-server"),
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "You do not have permissions for the new server 'io.github.otheruser/other-server'",
		},
		{
			name:       "same status transition allowed when updating newName",
			serverName: "io.github.testuser/deprecated-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/new-server"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.testuser/new-server"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusDeprecated, resp.Meta.Official.Status)
				assert.NotNil(t, resp.Meta.Official.NewName)
				assert.Equal(t, "io.github.testuser/new-server", *resp.Meta.Official.NewName)
			},
		},
		{
			name:       "same status transition allowed when updating statusMessage",
			serverName: "io.github.testuser/deprecated-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:        "deprecated",
				StatusMessage: strPtr("Updated deprecation message"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *apiv0.ServerResponse) {
				t.Helper()
				assert.Equal(t, model.StatusDeprecated, resp.Meta.Official.Status)
				assert.NotNil(t, resp.Meta.Official.StatusMessage)
				assert.Equal(t, "Updated deprecation message", *resp.Meta.Official.StatusMessage)
			},
		},
		{
			name:       "newName rejected for single version when server has multiple versions",
			serverName: "io.github.testuser/multi-version-server",
			version:    "1.0.0",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/new-server"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.testuser/new-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "new_name cannot be used with single version endpoint when server has multiple versions",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create Huma API
			mux := http.NewServeMux()
			api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))

			// Register status endpoints
			v0.RegisterStatusEndpoints(api, "/v0", registryService, cfg)

			// Create request body
			requestBody, err := json.Marshal(tc.requestBody)
			require.NoError(t, err)

			// Create request URL with proper encoding
			encodedServerName := url.PathEscape(tc.serverName)
			encodedVersion := url.PathEscape(tc.version)
			requestURL := "/v0/servers/" + encodedServerName + "/versions/" + encodedVersion + "/status"

			req := httptest.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")

			// Set authorization header
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			} else if tc.authClaims != nil {
				// Generate valid JWT token
				jwtManager := auth.NewJWTManager(cfg)
				tokenResponse, err := jwtManager.GenerateTokenResponse(context.Background(), *tc.authClaims)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+tokenResponse.RegistryToken)
			}

			// Create response recorder and execute request
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			// Check response
			if tc.expectedStatus != w.Code {
				t.Logf("Response body: %s", w.Body.String())
			}
			assert.Equal(t, tc.expectedStatus, w.Code)

			if tc.expectedError != "" {
				assert.Contains(t, w.Body.String(), tc.expectedError)
			}

			if tc.expectedStatus == http.StatusOK && tc.checkResult != nil {
				var response apiv0.ServerResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				require.NoError(t, err)
				tc.checkResult(t, &response)
			}
		})
	}
}

func TestUpdateServerStatusEndpointSameStatusTransition(t *testing.T) {
	// Create test config
	testSeed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(testSeed)
	require.NoError(t, err)
	cfg := &config.Config{
		JWTPrivateKey:            hex.EncodeToString(testSeed),
		EnableRegistryValidation: false,
	}

	// Create registry service
	registryService := service.NewRegistryService(database.NewTestDB(t), cfg)

	// Create an active server
	activeServer := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.testuser/same-status-test",
		Description: "Server for same status transition test",
		Version:     "1.0.0",
	}
	_, err = registryService.CreateServer(context.Background(), activeServer)
	require.NoError(t, err)

	// Create Huma API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterStatusEndpoints(api, "/v0", registryService, cfg)

	// Try to transition from active to active (should fail)
	requestBody := v0.UpdateServerStatusBody{
		Status: "active",
	}
	bodyBytes, err := json.Marshal(requestBody)
	require.NoError(t, err)

	encodedName := url.PathEscape(activeServer.Name)
	requestURL := "/v0/servers/" + encodedName + "/versions/1.0.0/status"

	req := httptest.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Generate admin token
	jwtManager := auth.NewJWTManager(cfg)
	tokenResponse, err := jwtManager.GenerateTokenResponse(context.Background(), auth.JWTClaims{
		AuthMethod: auth.MethodNone,
		Permissions: []auth.Permission{
			{Action: auth.PermissionActionEdit, ResourcePattern: "*"},
		},
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tokenResponse.RegistryToken)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid status transition from active to active")
}

func TestUpdateServerStatusEndpointURLEncoding(t *testing.T) {
	// Create test config
	testSeed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(testSeed)
	require.NoError(t, err)
	cfg := &config.Config{
		JWTPrivateKey:            hex.EncodeToString(testSeed),
		EnableRegistryValidation: false,
	}

	// Create registry service
	registryService := service.NewRegistryService(database.NewTestDB(t), cfg)

	// Create a server with build metadata version
	buildMetadataServer := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.testuser/build-metadata-server",
		Description: "Server with build metadata version",
		Version:     "1.0.0+20130313144700",
	}
	_, err = registryService.CreateServer(context.Background(), buildMetadataServer)
	require.NoError(t, err)

	// Create Huma API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))
	v0.RegisterStatusEndpoints(api, "/v0", registryService, cfg)

	// Update status with URL-encoded version
	requestBody := v0.UpdateServerStatusBody{
		Status:        "deprecated",
		StatusMessage: strPtr("Testing URL encoding"),
	}
	bodyBytes, err := json.Marshal(requestBody)
	require.NoError(t, err)

	encodedName := url.PathEscape(buildMetadataServer.Name)
	encodedVersion := url.PathEscape(buildMetadataServer.Version)
	requestURL := "/v0/servers/" + encodedName + "/versions/" + encodedVersion + "/status"

	req := httptest.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Generate admin token
	jwtManager := auth.NewJWTManager(cfg)
	tokenResponse, err := jwtManager.GenerateTokenResponse(context.Background(), auth.JWTClaims{
		AuthMethod: auth.MethodNone,
		Permissions: []auth.Permission{
			{Action: auth.PermissionActionEdit, ResourcePattern: "*"},
		},
	})
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tokenResponse.RegistryToken)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response apiv0.ServerResponse
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)
	assert.Equal(t, model.StatusDeprecated, response.Meta.Official.Status)
	assert.Equal(t, "1.0.0+20130313144700", response.Server.Version)
}

func TestUpdateAllVersionsStatusEndpoint(t *testing.T) {
	// Create test config
	testSeed := make([]byte, ed25519.SeedSize)
	_, err := rand.Read(testSeed)
	require.NoError(t, err)
	cfg := &config.Config{
		JWTPrivateKey:            hex.EncodeToString(testSeed),
		EnableRegistryValidation: false,
	}

	// Create registry service and test data
	registryService := service.NewRegistryService(database.NewTestDB(t), cfg)

	// Create a server with multiple versions
	multiVersionServer := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.testuser/multi-version-server",
		Description: "Server with multiple versions",
		Version:     "1.0.0",
		Repository: &model.Repository{
			URL:    "https://github.com/testuser/multi-version-server",
			Source: "github",
			ID:     "testuser/multi-version-server",
		},
	}
	_, err = registryService.CreateServer(context.Background(), multiVersionServer)
	require.NoError(t, err)

	// Add more versions
	multiVersionServer.Version = "1.1.0"
	_, err = registryService.CreateServer(context.Background(), multiVersionServer)
	require.NoError(t, err)

	multiVersionServer.Version = "2.0.0"
	_, err = registryService.CreateServer(context.Background(), multiVersionServer)
	require.NoError(t, err)

	// Create a new server to use for newName tests
	newServer := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.testuser/new-target-server",
		Description: "New target server for renaming",
		Version:     "1.0.0",
		Repository: &model.Repository{
			URL:    "https://github.com/testuser/new-target-server",
			Source: "github",
			ID:     "testuser/new-target-server",
		},
	}
	_, err = registryService.CreateServer(context.Background(), newServer)
	require.NoError(t, err)

	// Create other user's server
	otherServer := &apiv0.ServerJSON{
		Schema:      model.CurrentSchemaURL,
		Name:        "io.github.otheruser/other-server",
		Description: "Server owned by another user",
		Version:     "1.0.0",
		Repository: &model.Repository{
			URL:    "https://github.com/otheruser/other-server",
			Source: "github",
			ID:     "otheruser/other-server",
		},
	}
	_, err = registryService.CreateServer(context.Background(), otherServer)
	require.NoError(t, err)

	testCases := []struct {
		name           string
		serverName     string
		authClaims     *auth.JWTClaims
		authHeader     string
		requestBody    v0.UpdateServerStatusBody
		expectedStatus int
		expectedError  string
		checkResult    func(*testing.T, *v0.UpdateAllVersionsStatusResponse)
	}{
		{
			name:       "successful deprecation of all versions",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:        "deprecated",
				StatusMessage: strPtr("This server is deprecated"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *v0.UpdateAllVersionsStatusResponse) {
				t.Helper()
				assert.Equal(t, 3, resp.UpdatedCount)
				assert.Len(t, resp.Servers, 3)
				for _, server := range resp.Servers {
					assert.Equal(t, model.StatusDeprecated, server.Meta.Official.Status)
					assert.NotNil(t, server.Meta.Official.StatusMessage)
					assert.Equal(t, "This server is deprecated", *server.Meta.Official.StatusMessage)
				}
			},
		},
		{
			name:       "successful yank of all versions with newName",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/new-target-server"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:        "yanked",
				StatusMessage: strPtr("Moved to new server"),
				NewName:       strPtr("io.github.testuser/new-target-server"),
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *v0.UpdateAllVersionsStatusResponse) {
				t.Helper()
				assert.Equal(t, 3, resp.UpdatedCount)
				for _, server := range resp.Servers {
					assert.Equal(t, model.StatusYanked, server.Meta.Official.Status)
					assert.NotNil(t, server.Meta.Official.NewName)
					assert.Equal(t, "io.github.testuser/new-target-server", *server.Meta.Official.NewName)
				}
			},
		},
		{
			name:       "successful reactivation of all versions",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "active",
			},
			expectedStatus: http.StatusOK,
			checkResult: func(t *testing.T, resp *v0.UpdateAllVersionsStatusResponse) {
				t.Helper()
				assert.Equal(t, 3, resp.UpdatedCount)
				for _, server := range resp.Servers {
					assert.Equal(t, model.StatusActive, server.Meta.Official.Status)
					// Status message and newName should be cleared when transitioning to active
					assert.Nil(t, server.Meta.Official.StatusMessage)
					assert.Nil(t, server.Meta.Official.NewName)
				}
			},
		},
		{
			name:           "missing authorization header",
			serverName:     "io.github.testuser/multi-version-server",
			authHeader:     "",
			requestBody:    v0.UpdateServerStatusBody{Status: "deprecated"},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedError:  "required header parameter is missing",
		},
		{
			name:       "invalid authorization header format",
			serverName: "io.github.testuser/multi-version-server",
			authHeader: "InvalidFormat token123",
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusUnauthorized,
			expectedError:  "Invalid Authorization header format",
		},
		{
			name:       "permission denied - no edit permissions",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionPublish, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "You do not have edit permissions",
		},
		{
			name:       "server not found",
			serverName: "io.github.testuser/non-existent",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status: "deprecated",
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "Server not found",
		},
		{
			name:       "newName rejected when transitioning to active",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "active",
				NewName: strPtr("io.github.testuser/new-target-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "new_name can only be used with deprecated or yanked status",
		},
		{
			name:       "newName rejected when same as current server name",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.testuser/multi-version-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "new_name cannot be the same as the current server name",
		},
		{
			name:       "newName with non-existent server",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.testuser/non-existent-server"),
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "New server 'io.github.testuser/non-existent-server' does not exist in the registry",
		},
		{
			name:       "newName with server belonging to different user without permissions",
			serverName: "io.github.testuser/multi-version-server",
			authClaims: &auth.JWTClaims{
				AuthMethod:        auth.MethodGitHubAT,
				AuthMethodSubject: "testuser",
				Permissions: []auth.Permission{
					{Action: auth.PermissionActionEdit, ResourcePattern: "io.github.testuser/*"},
				},
			},
			requestBody: v0.UpdateServerStatusBody{
				Status:  "deprecated",
				NewName: strPtr("io.github.otheruser/other-server"),
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "You do not have permissions for the new server 'io.github.otheruser/other-server'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create Huma API
			mux := http.NewServeMux()
			api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))

			// Register all-versions status endpoints
			v0.RegisterAllVersionsStatusEndpoints(api, "/v0", registryService, cfg)

			// Create request body
			requestBody, err := json.Marshal(tc.requestBody)
			require.NoError(t, err)

			// Create request URL with proper encoding
			encodedServerName := url.PathEscape(tc.serverName)
			requestURL := "/v0/servers/" + encodedServerName + "/status"

			req := httptest.NewRequest(http.MethodPatch, requestURL, bytes.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")

			// Set authorization header
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			} else if tc.authClaims != nil {
				// Generate valid JWT token
				jwtManager := auth.NewJWTManager(cfg)
				tokenResponse, err := jwtManager.GenerateTokenResponse(context.Background(), *tc.authClaims)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+tokenResponse.RegistryToken)
			}

			// Create response recorder and execute request
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			// Check response
			if tc.expectedStatus != w.Code {
				t.Logf("Response body: %s", w.Body.String())
			}
			assert.Equal(t, tc.expectedStatus, w.Code)

			if tc.expectedError != "" {
				assert.Contains(t, w.Body.String(), tc.expectedError)
			}

			if tc.expectedStatus == http.StatusOK && tc.checkResult != nil {
				var response v0.UpdateAllVersionsStatusResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				require.NoError(t, err)
				tc.checkResult(t, &response)
			}
		})
	}
}

// strPtr is a helper function to create a pointer to a string
func strPtr(s string) *string {
	return &s
}
