package v0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/modelcontextprotocol/registry/internal/auth"
	"github.com/modelcontextprotocol/registry/internal/config"
	"github.com/modelcontextprotocol/registry/internal/database"
	"github.com/modelcontextprotocol/registry/internal/service"
	apiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
	"github.com/modelcontextprotocol/registry/pkg/model"
)

// UpdateServerStatusBody represents the request body for updating server status
type UpdateServerStatusBody struct {
	Status         string  `json:"status" required:"true" enum:"active,deprecated,yanked" doc:"New server lifecycle status"`
	StatusMessage  *string `json:"statusMessage,omitempty" maxLength:"500" doc:"Optional message explaining the status change (e.g., reason for deprecation)"`
	AlternativeURL *string `json:"alternativeUrl,omitempty" format:"uri" doc:"Optional URL to an alternative/replacement server for deprecated or yanked servers"`
	NewName        *string `json:"newName,omitempty" doc:"Optional new server name when server has been renamed/moved"`
}

// UpdateServerStatusInput represents the input for updating server status
type UpdateServerStatusInput struct {
	Authorization string                 `header:"Authorization" doc:"Registry JWT token with edit permissions" required:"true"`
	ServerName    string                 `path:"serverName" doc:"URL-encoded server name" example:"com.example%2Fmy-server"`
	Version       string                 `path:"version" doc:"URL-encoded version to update" example:"1.0.0"`
	Body          UpdateServerStatusBody `body:""`
}

// RegisterStatusEndpoints registers the status update endpoint with a custom path prefix
func RegisterStatusEndpoints(api huma.API, pathPrefix string, registry service.RegistryService, cfg *config.Config) {
	jwtManager := auth.NewJWTManager(cfg)

	// Update server status endpoint
	huma.Register(api, huma.Operation{
		OperationID: "update-server-status" + strings.ReplaceAll(pathPrefix, "/", "-"),
		Method:      http.MethodPatch,
		Path:        pathPrefix + "/servers/{serverName}/versions/{version}/status",
		Summary:     "Update MCP server status",
		Description: "Update the status metadata of a specific version of an MCP server. Requires edit permission for the server. This endpoint allows changing status, status message, alternative URL, and new name without requiring the full server configuration.",
		Tags:        []string{"servers"},
		Security: []map[string][]string{
			{"bearer": {}},
		},
	}, func(ctx context.Context, input *UpdateServerStatusInput) (*Response[apiv0.ServerResponse], error) {
		// Extract bearer token
		const bearerPrefix = "Bearer "
		authHeader := input.Authorization
		if len(authHeader) < len(bearerPrefix) || !strings.EqualFold(authHeader[:len(bearerPrefix)], bearerPrefix) {
			return nil, huma.Error401Unauthorized("Invalid Authorization header format. Expected 'Bearer <token>'")
		}
		token := authHeader[len(bearerPrefix):]

		// Validate Registry JWT token
		claims, err := jwtManager.ValidateToken(ctx, token)
		if err != nil {
			return nil, huma.Error401Unauthorized("Invalid or expired Registry JWT token", err)
		}

		// URL-decode the server name
		serverName, err := url.PathUnescape(input.ServerName)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid server name encoding", err)
		}

		// URL-decode the version
		version, err := url.PathUnescape(input.Version)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid version encoding", err)
		}

		newStatus := model.Status(input.Body.Status)

		// Get all versions - gives us both the server data and version count in one DB call
		allVersions, err := registry.GetAllVersionsByServerName(ctx, serverName)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error500InternalServerError("Failed to get server versions", err)
		}

		// Check newName validation first (before version check) for better error messages
		hasNewName := input.Body.NewName != nil && *input.Body.NewName != ""
		if hasNewName && len(allVersions) > 1 {
			return nil, huma.Error400BadRequest("new_name cannot be used with single version endpoint when server has multiple versions. Use the all-versions endpoint instead: PATCH /servers/{serverName}/status")
		}

		// Find the requested version
		var currentServer *apiv0.ServerResponse
		for _, v := range allVersions {
			if v.Server.Version == version {
				currentServer = v
				break
			}
		}
		if currentServer == nil {
			return nil, huma.Error404NotFound("Server version not found")
		}

		// Verify edit permissions for this server
		if !jwtManager.HasPermission(currentServer.Server.Name, auth.PermissionActionEdit, claims.Permissions) {
			return nil, huma.Error403Forbidden("You do not have edit permissions for this server")
		}

		// Validate newName if provided
		if hasNewName {
			if err := validateNewNameForStatus(ctx, input.Body, serverName, registry, jwtManager, claims); err != nil {
				return nil, err
			}
		}

		// Validate status transition is allowed
		if currentServer.Meta.Official != nil {
			currentStatus := currentServer.Meta.Official.Status
			isSameStatus := currentStatus == newStatus

			// Reject same-status requests with no metadata updates (pointless no-op)
			if isSameStatus && !hasMetadataFieldsToUpdate(input.Body) {
				return nil, huma.Error400BadRequest(fmt.Sprintf("No changes to apply: status is already %s", currentStatus))
			}

			// Reject invalid status transitions (e.g., invalid status values)
			if !isSameStatus && !isValidStatusTransition(currentStatus, newStatus) {
				return nil, huma.Error400BadRequest(fmt.Sprintf("Invalid status transition from %s to %s", currentStatus, newStatus))
			}
		}

		// Build status change request
		statusChange := buildStatusChangeRequestFromBody(input.Body)

		// Update the server status using the service
		updatedServer, err := registry.UpdateServerStatus(ctx, serverName, version, statusChange)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error400BadRequest("Failed to update server status", err)
		}

		return &Response[apiv0.ServerResponse]{
			Body: *updatedServer,
		}, nil
	})
}

// validateNewNameForStatus validates the new_name parameter for the status endpoint
func validateNewNameForStatus(ctx context.Context, body UpdateServerStatusBody, currentServerName string, registry service.RegistryService, jwtManager *auth.JWTManager, claims *auth.JWTClaims) error {
	newName := *body.NewName

	// Validation: new_name cannot be the same as the current server name
	if newName == currentServerName {
		return huma.Error400BadRequest("new_name cannot be the same as the current server name")
	}

	// Validation: new_name can only be used with deprecated or yanked status
	if body.Status != string(model.StatusDeprecated) && body.Status != string(model.StatusYanked) {
		return huma.Error400BadRequest("new_name can only be used with deprecated or yanked status")
	}

	// Validation: Check that the new server exists
	newServer, err := registry.GetServerByName(ctx, newName)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return huma.Error400BadRequest(fmt.Sprintf("New server '%s' does not exist in the registry", newName))
		}
		return huma.Error500InternalServerError("Failed to validate new server name", err)
	}

	// Validation: Check that the user has publish permissions for the new server
	if !jwtManager.HasPermission(newServer.Server.Name, auth.PermissionActionPublish, claims.Permissions) {
		return huma.Error403Forbidden(fmt.Sprintf("You do not have permissions for the new server '%s'", newName))
	}

	return nil
}

// buildStatusChangeRequestFromBody constructs a StatusChangeRequest from the request body
func buildStatusChangeRequestFromBody(body UpdateServerStatusBody) *service.StatusChangeRequest {
	var statusMessage *string
	var alternativeURL *string
	var newName *string

	newStatus := model.Status(body.Status)

	// When transitioning to active status, clear status_message, alternative_url, and new_name
	if newStatus != model.StatusActive {
		statusMessage = body.StatusMessage
		alternativeURL = body.AlternativeURL
		newName = body.NewName
	}

	return &service.StatusChangeRequest{
		NewStatus:      newStatus,
		StatusMessage:  statusMessage,
		AlternativeURL: alternativeURL,
		NewName:        newName,
	}
}

// hasMetadataFieldsToUpdate checks if any metadata fields (statusMessage, alternativeURL, newName) are being updated
func hasMetadataFieldsToUpdate(body UpdateServerStatusBody) bool {
	return (body.StatusMessage != nil && *body.StatusMessage != "") ||
		(body.AlternativeURL != nil && *body.AlternativeURL != "") ||
		(body.NewName != nil && *body.NewName != "")
}

// UpdateAllVersionsStatusInput represents the input for updating all versions' status
type UpdateAllVersionsStatusInput struct {
	Authorization string                 `header:"Authorization" doc:"Registry JWT token with edit permissions" required:"true"`
	ServerName    string                 `path:"serverName" doc:"URL-encoded server name" example:"com.example%2Fmy-server"`
	Body          UpdateServerStatusBody `body:""`
}

// UpdateAllVersionsStatusResponse represents the response for updating all versions' status
type UpdateAllVersionsStatusResponse struct {
	UpdatedCount int                    `json:"updatedCount" doc:"Number of versions updated"`
	Servers      []apiv0.ServerResponse `json:"servers" doc:"List of all updated server versions"`
}

// RegisterAllVersionsStatusEndpoints registers the all-versions status update endpoint
func RegisterAllVersionsStatusEndpoints(api huma.API, pathPrefix string, registry service.RegistryService, cfg *config.Config) {
	jwtManager := auth.NewJWTManager(cfg)

	// Update all versions status endpoint
	huma.Register(api, huma.Operation{
		OperationID: "update-server-all-versions-status" + strings.ReplaceAll(pathPrefix, "/", "-"),
		Method:      http.MethodPatch,
		Path:        pathPrefix + "/servers/{serverName}/status",
		Summary:     "Update status for all versions of an MCP server",
		Description: "Update the status metadata of all versions of an MCP server in a single transaction. Requires edit permission for the server. Either all versions are updated or none on failure.",
		Tags:        []string{"servers"},
		Security: []map[string][]string{
			{"bearer": {}},
		},
	}, func(ctx context.Context, input *UpdateAllVersionsStatusInput) (*Response[UpdateAllVersionsStatusResponse], error) {
		// Extract bearer token
		const bearerPrefix = "Bearer "
		authHeader := input.Authorization
		if len(authHeader) < len(bearerPrefix) || !strings.EqualFold(authHeader[:len(bearerPrefix)], bearerPrefix) {
			return nil, huma.Error401Unauthorized("Invalid Authorization header format. Expected 'Bearer <token>'")
		}
		token := authHeader[len(bearerPrefix):]

		// Validate Registry JWT token
		claims, err := jwtManager.ValidateToken(ctx, token)
		if err != nil {
			return nil, huma.Error401Unauthorized("Invalid or expired Registry JWT token", err)
		}

		// URL-decode the server name
		serverName, err := url.PathUnescape(input.ServerName)
		if err != nil {
			return nil, huma.Error400BadRequest("Invalid server name encoding", err)
		}

		// Get any version to verify server exists and check permissions
		currentServer, err := registry.GetServerByName(ctx, serverName)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error500InternalServerError("Failed to get server", err)
		}

		// Verify edit permissions for this server
		if !jwtManager.HasPermission(currentServer.Server.Name, auth.PermissionActionEdit, claims.Permissions) {
			return nil, huma.Error403Forbidden("You do not have edit permissions for this server")
		}

		// Validate newName if provided
		if input.Body.NewName != nil && *input.Body.NewName != "" {
			if err := validateNewNameForStatus(ctx, input.Body, serverName, registry, jwtManager, claims); err != nil {
				return nil, err
			}
		}

		// Build status change request
		statusChange := buildStatusChangeRequestFromBody(input.Body)

		// Update all versions' status using the service
		updatedServers, err := registry.UpdateAllVersionsStatus(ctx, serverName, statusChange)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error400BadRequest("Failed to update server status", err)
		}

		// Convert to response format
		servers := make([]apiv0.ServerResponse, len(updatedServers))
		for i, s := range updatedServers {
			servers[i] = *s
		}

		return &Response[UpdateAllVersionsStatusResponse]{
			Body: UpdateAllVersionsStatusResponse{
				UpdatedCount: len(servers),
				Servers:      servers,
			},
		}, nil
	})
}

// isValidStatusTransition checks if a status transition is allowed
// Allowed transitions:
// - active ↔ deprecated ↔ yanked (all bidirectional transitions allowed)
// - Same status transitions are NOT allowed (no-op)
func isValidStatusTransition(currentStatus, newStatus model.Status) bool {
	// Same status transition is not allowed (no-op)
	if currentStatus == newStatus {
		return false
	}

	// All transitions between active, deprecated, and yanked are allowed
	validStatuses := map[model.Status]bool{
		model.StatusActive:     true,
		model.StatusDeprecated: true,
		model.StatusYanked:     true,
	}

	// Both current and new status must be valid
	return validStatuses[currentStatus] && validStatuses[newStatus]
}
