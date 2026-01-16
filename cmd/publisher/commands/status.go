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

	apiv0 "github.com/modelcontextprotocol/registry/pkg/api/v0"
)

func StatusCommand(args []string) error {
	// Parse command flags
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	status := fs.String("status", "", "New status: active, deprecated, or yanked (required)")
	message := fs.String("message", "", "Optional status message explaining the change")
	alternativeURL := fs.String("alternative-url", "", "Optional URL to alternative/replacement server")
	newName := fs.String("new-name", "", "Optional new server name when server has been renamed")
	allVersions := fs.Bool("all-versions", false, "Apply status change to all versions of the server")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required arguments
	if *status == "" {
		return errors.New("--status flag is required (active, deprecated, or yanked)")
	}

	// Validate status value
	validStatuses := map[string]bool{"active": true, "deprecated": true, "yanked": true}
	if !validStatuses[*status] {
		return fmt.Errorf("invalid status '%s'. Must be one of: active, deprecated, yanked", *status)
	}

	// Get server name from positional args
	remainingArgs := fs.Args()
	if len(remainingArgs) < 1 {
		return errors.New("server name is required\n\nUsage: mcp-publisher status <server-name> [version] --status <active|deprecated|yanked> [flags]")
	}

	serverName := remainingArgs[0]
	var version string

	// Get version if provided (required unless --all-versions is set)
	if !*allVersions {
		if len(remainingArgs) < 2 {
			return errors.New("version is required unless --all-versions flag is set\n\nUsage: mcp-publisher status <server-name> <version> --status <active|deprecated|yanked> [flags]")
		}
		version = remainingArgs[1]
	}

	// Validate new-name parameter constraints
	if *newName != "" {
		// Validation: new-name requires deprecated or yanked status
		if *status != "deprecated" && *status != "yanked" {
			return errors.New("--new-name can only be used with --status deprecated or --status yanked")
		}
		// Validation: new-name requires --all-versions flag
		if !*allVersions {
			return errors.New("--new-name requires --all-versions flag")
		}
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
		return updateAllVersionsStatus(registryURL, serverName, *status, *message, *alternativeURL, *newName, token)
	}
	return updateVersionStatus(registryURL, serverName, version, *status, *message, *alternativeURL, *newName, token)
}

func updateVersionStatus(registryURL, serverName, version, status, statusMessage, alternativeURL, newName, token string) error {
	_, _ = fmt.Fprintf(os.Stdout, "Updating %s version %s to status: %s\n", serverName, version, status)

	if err := updateServerStatus(registryURL, serverName, version, status, statusMessage, alternativeURL, newName, token); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	_, _ = fmt.Fprintln(os.Stdout, "✓ Successfully updated status")
	return nil
}

func updateAllVersionsStatus(registryURL, serverName, status, statusMessage, alternativeURL, newName, token string) error {
	_, _ = fmt.Fprintf(os.Stdout, "Fetching all versions of %s...\n", serverName)

	// Get all versions of the server
	versions, err := getAllServerVersions(registryURL, serverName)
	if err != nil {
		return fmt.Errorf("failed to get server versions: %w", err)
	}

	if len(versions) == 0 {
		return fmt.Errorf("no versions found for server %s", serverName)
	}

	_, _ = fmt.Fprintf(os.Stdout, "Found %d version(s). Updating all to status: %s\n", len(versions), status)

	// Update each version
	successCount := 0
	failureCount := 0
	for _, v := range versions {
		_, _ = fmt.Fprintf(os.Stdout, "  Updating version %s...", v)
		if err := updateServerStatus(registryURL, serverName, v, status, statusMessage, alternativeURL, newName, token); err != nil {
			_, _ = fmt.Fprintf(os.Stdout, " ✗ Failed: %v\n", err)
			failureCount++
		} else {
			_, _ = fmt.Fprintln(os.Stdout, " ✓")
			successCount++
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nCompleted: %d succeeded, %d failed\n", successCount, failureCount)
	if failureCount > 0 {
		return fmt.Errorf("%d version(s) failed to update", failureCount)
	}

	return nil
}

func getAllServerVersions(registryURL, serverName string) ([]string, error) {
	if !strings.HasSuffix(registryURL, "/") {
		registryURL += "/"
	}

	// URL encode the server name
	encodedServerName := url.PathEscape(serverName)
	versionsURL := registryURL + "v0/servers/" + encodedServerName + "/versions"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, versionsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, body)
	}

	var response struct {
		Servers []apiv0.ServerResponse `json:"servers"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	versions := make([]string, 0, len(response.Servers))
	for _, server := range response.Servers {
		versions = append(versions, server.Server.Version)
	}

	return versions, nil
}

func updateServerStatus(registryURL, serverName, version, status, statusMessage, alternativeURL, newName, token string) error {
	if !strings.HasSuffix(registryURL, "/") {
		registryURL += "/"
	}

	// First, get the current server details
	encodedServerName := url.PathEscape(serverName)
	encodedVersion := url.PathEscape(version)
	getURL := registryURL + "v0/servers/" + encodedServerName + "/versions/" + encodedVersion

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("error creating GET request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error getting current server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, body)
	}

	var currentServer apiv0.ServerResponse
	if err := json.Unmarshal(body, &currentServer); err != nil {
		return fmt.Errorf("error parsing current server: %w", err)
	}

	// Build the update URL with query parameters
	updateURL := registryURL + "v0/servers/" + encodedServerName + "/versions/" + encodedVersion
	params := url.Values{}
	params.Add("status", status)
	if statusMessage != "" {
		params.Add("status_message", statusMessage)
	}
	if alternativeURL != "" {
		params.Add("alternative_url", alternativeURL)
	}
	if newName != "" {
		params.Add("new_name", newName)
	}
	updateURL += "?" + params.Encode()

	jsonData, err := json.Marshal(currentServer.Server)
	if err != nil {
		return fmt.Errorf("error serializing server: %w", err)
	}

	req, err = http.NewRequestWithContext(context.Background(), http.MethodPut, updateURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error creating PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending update request: %w", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading update response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, body)
	}

	return nil
}
