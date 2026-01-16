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

// EditServerInput represents the input for editing a server
type EditServerInput struct {
	Authorization  string           `header:"Authorization" doc:"Registry JWT token with edit permissions" required:"true"`
	ServerName     string           `path:"serverName" doc:"URL-encoded server name" example:"com.example%2Fmy-server"`
	Version        string           `path:"version" doc:"URL-encoded version to edit" example:"1.0.0"`
	Status         string           `query:"status" doc:"New status for the server (active, deprecated, yanked)" required:"false" enum:"active,deprecated,yanked"`
	StatusMessage  string           `query:"status_message" doc:"Optional message explaining the status change" required:"false"`
	AlternativeURL string           `query:"alternative_url" doc:"Optional URL to alternative/replacement server or any document" required:"false"`
	NewName        string           `query:"new_name" doc:"Optional new server name when server has been renamed" required:"false"`
	Body           apiv0.ServerJSON `body:""`
}

// RegisterEditEndpoints registers the edit endpoint with a custom path prefix
func RegisterEditEndpoints(api huma.API, pathPrefix string, registry service.RegistryService, cfg *config.Config) {
	jwtManager := auth.NewJWTManager(cfg)

	// Edit server endpoint
	huma.Register(api, huma.Operation{
		OperationID: "edit-server" + strings.ReplaceAll(pathPrefix, "/", "-"),
		Method:      http.MethodPut,
		Path:        pathPrefix + "/servers/{serverName}/versions/{version}",
		Summary:     "Edit MCP server",
		Description: "Update a specific version of an existing MCP server (admin only).",
		Tags:        []string{"admin"},
		Security: []map[string][]string{
			{"bearer": {}},
		},
	}, func(ctx context.Context, input *EditServerInput) (*Response[apiv0.ServerResponse], error) {
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

		// Get current server to check permissions against existing name
		currentServer, err := registry.GetServerByNameAndVersion(ctx, serverName, version)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error500InternalServerError("Failed to get current server", err)
		}

		// Verify edit permissions for this server using the existing server name
		if !jwtManager.HasPermission(currentServer.Server.Name, auth.PermissionActionEdit, claims.Permissions) {
			return nil, huma.Error403Forbidden("You do not have edit permissions for this server")
		}

		// Prevent renaming servers
		if currentServer.Server.Name != input.Body.Name {
			return nil, huma.Error400BadRequest("Cannot rename server")
		}

		// Validate that the version in the body matches the URL parameter
		if input.Body.Version != version {
			return nil, huma.Error400BadRequest("Version in request body must match URL path parameter")
		}

		if err := validateNewName(ctx, input, registry, jwtManager, claims); err != nil {
			return nil, err
		}

		// Handle status changes with proper permission validation
		if input.Status != "" {
			newStatus := model.Status(input.Status)

			// Validate status transition is allowed
			if currentServer.Meta.Official != nil {
				currentStatus := currentServer.Meta.Official.Status
				if !isValidStatusTransition(currentStatus, newStatus) {
					return nil, huma.Error400BadRequest(fmt.Sprintf("Invalid status transition from %s to %s", currentStatus, newStatus))
				}
			}

			// For now, only allow status changes for admins
			// Future: Implement logic to allow server authors to change active <-> deprecated
			// but only admins can set to yanked
		}

		// Update the server using the service
		statusChange := buildStatusChangeRequest(input)
		updatedServer, err := registry.UpdateServer(ctx, serverName, version, &input.Body, statusChange)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				return nil, huma.Error404NotFound("Server not found")
			}
			return nil, huma.Error400BadRequest("Failed to edit server", err)
		}

		return &Response[apiv0.ServerResponse]{
			Body: *updatedServer,
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

// validateNewName validates the new_name parameter for server renaming
func validateNewName(ctx context.Context, input *EditServerInput, registry service.RegistryService, jwtManager *auth.JWTManager, claims *auth.JWTClaims) error {
	if input.NewName == "" {
		return nil
	}

	// Validation: new_name can only be used with deprecated or yanked status
	if input.Status != string(model.StatusDeprecated) && input.Status != string(model.StatusYanked) {
		return huma.Error400BadRequest("new_name can only be used with deprecated or yanked status")
	}

	// Validation: Check that the new server exists
	newServer, err := registry.GetServerByName(ctx, input.NewName)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return huma.Error400BadRequest(fmt.Sprintf("New server '%s' does not exist in the registry", input.NewName))
		}
		return huma.Error500InternalServerError("Failed to validate new server name", err)
	}

	// Validation: Check that the user has publish permissions for the new server
	if !jwtManager.HasPermission(newServer.Server.Name, auth.PermissionActionPublish, claims.Permissions) {
		return huma.Error403Forbidden(fmt.Sprintf("You do not have permissions for the new server '%s'", input.NewName))
	}

	return nil
}

// buildStatusChangeRequest constructs a StatusChangeRequest from input parameters
func buildStatusChangeRequest(input *EditServerInput) *service.StatusChangeRequest {
	if input.Status == "" {
		return nil
	}

	var statusMessage *string
	var alternativeURL *string
	var newName *string

	newStatus := model.Status(input.Status)

	// When transitioning to active status, clear status_message, alternative_url, and new_name
	if newStatus != model.StatusActive {
		if input.StatusMessage != "" {
			statusMessage = &input.StatusMessage
		}
		if input.AlternativeURL != "" {
			alternativeURL = &input.AlternativeURL
		}
		if input.NewName != "" {
			newName = &input.NewName
		}
	}

	return &service.StatusChangeRequest{
		NewStatus:      newStatus,
		StatusMessage:  statusMessage,
		AlternativeURL: alternativeURL,
		NewName:        newName,
	}
}
