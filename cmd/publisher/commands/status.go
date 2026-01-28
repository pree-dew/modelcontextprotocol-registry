package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// StatusUpdateRequest represents the request body for status update endpoints
type StatusUpdateRequest struct {
	Status        string  `json:"status"`
	StatusMessage *string `json:"statusMessage,omitempty"`
}

// AllVersionsStatusResponse represents the response from the all-versions status endpoint
type AllVersionsStatusResponse struct {
	UpdatedCount int `json:"updatedCount"`
}

func StatusCommand(args []string) error {
	// Parse command flags
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	status := fs.String("status", "", "New status: active, deprecated, or deleted (required)")
	message := fs.String("message", "", "Optional status message explaining the change")
	allVersions := fs.Bool("all-versions", false, "Apply status change to all versions of the server")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required arguments
	if *status == "" {
		return errors.New("--status flag is required (active, deprecated, or deleted)")
	}

	// Validate status value
	validStatuses := map[string]bool{"active": true, "deprecated": true, "deleted": true}
	if !validStatuses[*status] {
		return fmt.Errorf("invalid status '%s'. Must be one of: active, deprecated, deleted", *status)
	}

	// Get server name from positional args
	remainingArgs := fs.Args()
	if len(remainingArgs) < 1 {
		return errors.New("server name is required\n\nUsage: mcp-publisher status --status <active|deprecated|deleted> [flags] <server-name> [version]")
	}

	serverName := remainingArgs[0]
	var version string

	// Get version if provided (required unless --all-versions is set)
	if !*allVersions {
		if len(remainingArgs) < 2 {
			return errors.New("version is required unless --all-versions flag is set\n\nUsage: mcp-publisher status --status <active|deprecated|deleted> [flags] <server-name> <version>")
		}
		version = remainingArgs[1]
	}

	// Load saved token
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	tokenPath := filepath.Join(homeDir, TokenFileName)
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("not authenticated. Run 'mcp-publisher login <method>' first")
		}
		return fmt.Errorf("failed to read token: %w", err)
	}

	var tokenInfo map[string]string
	if err := json.Unmarshal(tokenData, &tokenInfo); err != nil {
		return fmt.Errorf("invalid token data: %w", err)
	}

	token := tokenInfo["token"]
	registryURL := tokenInfo["registry"]
	if registryURL == "" {
		registryURL = DefaultRegistryURL
	}

	// Update status
	if *allVersions {
		return updateAllVersionsStatus(registryURL, serverName, *status, *message, token)
	}
	return updateVersionStatus(registryURL, serverName, version, *status, *message, token)
}

func updateVersionStatus(registryURL, serverName, version, status, statusMessage, token string) error {
	_, _ = fmt.Fprintf(os.Stdout, "Updating %s version %s to status: %s\n", serverName, version, status)

	if err := updateServerStatus(registryURL, serverName, version, status, statusMessage, token); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stdout, "✓ Successfully updated status")
	return nil
}

func updateAllVersionsStatus(registryURL, serverName, status, statusMessage, token string) error {
	_, _ = fmt.Fprintf(os.Stdout, "Updating all versions of %s to status: %s\n", serverName, status)

	if !strings.HasSuffix(registryURL, "/") {
		registryURL += "/"
	}

	// Build the request body
	requestBody := StatusUpdateRequest{
		Status: status,
	}
	if statusMessage != "" {
		requestBody.StatusMessage = &statusMessage
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("error serializing request: %w", err)
	}

	// URL encode the server name
	encodedServerName := url.PathEscape(serverName)
	statusURL := registryURL + "v0/servers/" + encodedServerName + "/status"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, statusURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, body)
	}

	// Parse response to get updated count
	var response AllVersionsStatusResponse
	if err := json.Unmarshal(body, &response); err != nil {
		// If we can't parse the response, just report success
		_, _ = fmt.Fprintln(os.Stdout, "✓ Successfully updated all versions")
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "✓ Successfully updated %d version(s)\n", response.UpdatedCount)
	return nil
}

func updateServerStatus(registryURL, serverName, version, status, statusMessage, token string) error {
	if !strings.HasSuffix(registryURL, "/") {
		registryURL += "/"
	}

	// Build the request body
	requestBody := StatusUpdateRequest{
		Status: status,
	}
	if statusMessage != "" {
		requestBody.StatusMessage = &statusMessage
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("error serializing request: %w", err)
	}

	// URL encode the server name and version
	encodedServerName := url.PathEscape(serverName)
	encodedVersion := url.PathEscape(version)
	statusURL := registryURL + "v0/servers/" + encodedServerName + "/versions/" + encodedVersion + "/status"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, statusURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, body)
	}

	return nil
}
