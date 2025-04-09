/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package update

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const (
	// DefaultMappingsURL is the default URL for fetching mappings
	DefaultMappingsURL = "https://raw.githubusercontent.com/chainguard-dev/dfc/refs/heads/main/mappings.yaml"

	// OrgName is the organization name used in XDG paths
	OrgName = "dev.chainguard.dfc"
)

// Options configures the update behavior
type Options struct {
	// UserAgent is the user agent string to use for update requests
	UserAgent string

	// MappingsURL is the URL to fetch the latest mappings from
	MappingsURL string
}

// OCILayout represents the oci-layout file
type OCILayout struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

// OCIIndex represents the index.json file
type OCIIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []OCIDescriptor `json:"manifests"`
}

// OCIDescriptor represents a descriptor in the index
type OCIDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// GetCacheDir returns the XDG cache directory for dfc using the xdg library
func GetCacheDir() (string, error) {
	// Use xdg library to get the cache directory path
	cacheDir := filepath.Join(xdg.CacheHome, OrgName, "mappings")

	// Ensure the directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("creating cache directory: %w", err)
	}

	return cacheDir, nil
}

// GetConfigDir returns the XDG config directory for dfc using the xdg library
func GetConfigDir() (string, error) {
	// Use xdg library to get the config directory
	return xdg.ConfigHome, nil
}

// Variables for dependency injection in tests
var (
	jsonMarshal       = json.Marshal
	jsonMarshalIndent = json.MarshalIndent
	xdgConfigFile     = xdg.ConfigFile
)

// GetMappingsConfigPath returns the path to the mappings.yaml file in XDG_CONFIG_HOME
func GetMappingsConfigPath() (string, error) {
	// Use xdg library's ConfigFile to get the proper location
	mappingsPath, err := xdgConfigFile(filepath.Join(OrgName, "mappings.yaml"))
	if err != nil {
		return "", fmt.Errorf("getting mappings config path: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(mappingsPath), 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}

	return mappingsPath, nil
}

// GetMappingsConfigPathFunc is the function type for GetMappingsConfigPath
type GetMappingsConfigPathFunc func() (string, error)

// Default implementation for use in tests
var getMappingsConfigPathFn GetMappingsConfigPathFunc = GetMappingsConfigPath

// GetMappingsConfig reads and returns the contents of the mappings.yaml file
func GetMappingsConfig() ([]byte, error) {
	mappingsPath, err := getMappingsConfigPathFn()
	if err != nil {
		return nil, err
	}

	// Check if the file exists
	if _, err := os.Stat(mappingsPath); err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return nil with no error
			return nil, nil
		}
		return nil, fmt.Errorf("checking mappings file: %w", err)
	}

	// Read the mappings file
	data, err := os.ReadFile(mappingsPath)
	if err != nil {
		return nil, fmt.Errorf("reading mappings file: %w", err)
	}

	return data, nil
}

// initOCILayout initializes the OCI layout in the cache directory
func initOCILayout(cacheDir string) error {
	// Create the blobs/sha512 directory
	blobsDir := filepath.Join(cacheDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		return fmt.Errorf("creating blobs directory: %w", err)
	}

	// Create the oci-layout file
	layout := OCILayout{ImageLayoutVersion: "1.0.0"}
	layoutData, err := jsonMarshal(layout)
	if err != nil {
		return fmt.Errorf("marshalling oci-layout: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cacheDir, "oci-layout"), layoutData, 0644); err != nil {
		return fmt.Errorf("writing oci-layout file: %w", err)
	}

	// Create an empty index.json file
	index := OCIIndex{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests:     []OCIDescriptor{},
	}

	indexData, err := jsonMarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling index.json: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cacheDir, "index.json"), indexData, 0644); err != nil {
		return fmt.Errorf("writing index.json file: %w", err)
	}

	return nil
}

// updateIndexJSON updates the index.json file with the new mapping blob
func updateIndexJSON(cacheDir, digest string, size int64) error {
	// Read the current index.json
	indexPath := filepath.Join(cacheDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading index.json: %w", err)
	}

	var index OCIIndex
	if len(indexData) > 0 {
		if err := json.Unmarshal(indexData, &index); err != nil {
			return fmt.Errorf("unmarshalling index.json: %w", err)
		}
	} else {
		// Initialize a new index
		index = OCIIndex{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.index.v1+json",
			Manifests:     []OCIDescriptor{},
		}
	}

	// Remove any existing entries with this digest
	filteredManifests := []OCIDescriptor{}
	for _, manifest := range index.Manifests {
		// Skip if it has the same digest
		if manifest.Digest == digest {
			continue
		}
		filteredManifests = append(filteredManifests, manifest)
	}

	// Create a new descriptor for the mapping
	now := time.Now().UTC().Format(time.RFC3339)

	descriptor := OCIDescriptor{
		MediaType: "application/yaml",
		Digest:    digest,
		Size:      size,
		Annotations: map[string]string{
			"vnd.chainguard.dfc.mappings.downloadedAt": now,
		},
	}

	// Add the new descriptor
	filteredManifests = append(filteredManifests, descriptor)
	index.Manifests = filteredManifests

	// Write the updated index.json
	updatedIndexData, err := jsonMarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling updated index.json: %w", err)
	}

	if err := os.WriteFile(indexPath, updatedIndexData, 0644); err != nil {
		return fmt.Errorf("writing updated index.json: %w", err)
	}

	return nil
}

// UpdateIndexJSONFunc is the function type for updateIndexJSON
type UpdateIndexJSONFunc func(cacheDir, digest string, size int64) error

// Default implementation for use in tests
var updateIndexJSONFn UpdateIndexJSONFunc = updateIndexJSON

// Update checks for available updates to the dfc tool
func Update(ctx context.Context, opts Options) error {
	fmt.Fprintf(os.Stderr, "Checking for mappings update...\n")

	// Set default MappingsURL if not provided
	mappingsURL := opts.MappingsURL
	if mappingsURL == "" {
		mappingsURL = DefaultMappingsURL
	}

	// Create a new HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mappingsURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Set the User-Agent header
	userAgent := opts.UserAgent
	if userAgent == "" {
		userAgent = "dfc/dev"
	}
	req.Header.Set("User-Agent", userAgent)

	// Send the request
	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching mappings: %w", err)
	}
	defer resp.Body.Close()

	// Check the response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	// Calculate SHA512 hash
	hash := sha512.Sum512(body)
	hashString := hex.EncodeToString(hash[:])
	digestString := "sha512:" + hashString

	// Get the XDG cache directory
	cacheDir, err := GetCacheDir()
	if err != nil {
		return fmt.Errorf("getting XDG cache directory: %w", err)
	}

	// Check if the cache directory exists, if not initialize the OCI layout
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {

		// Create the directory structure
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("creating cache directory structure: %w", err)
		}

		if err := initOCILayout(cacheDir); err != nil {
			return fmt.Errorf("initializing OCI layout: %w", err)
		}
	}

	// Check if we already have this mapping file
	blobPath := filepath.Join(cacheDir, "blobs", "sha512", hashString)
	if _, err := os.Stat(blobPath); err == nil {

		// Get the XDG config directory for the symlink
		configDir, err := GetConfigDir()
		if err != nil {
			return fmt.Errorf("getting XDG config directory: %w", err)
		}

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, OrgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Check if the symlink exists and points to the correct file
		symlinkPath := filepath.Join(nestedConfigDir, "mappings.yaml")
		currentTarget, err := os.Readlink(symlinkPath)
		if err != nil || currentTarget != blobPath {
			// Remove existing symlink if it exists
			_ = os.Remove(symlinkPath)
			// Create new symlink
			if err := os.Symlink(blobPath, symlinkPath); err != nil {
				return fmt.Errorf("creating symlink: %w", err)
			}
		}

		fmt.Fprintf(os.Stderr, "Already have latest mappings in %s\n", symlinkPath)

	} else {

		fmt.Fprintf(os.Stderr, "Saving latest version of mappings to %s\n", blobPath)

		// Save the mapping file
		blobsDir := filepath.Join(cacheDir, "blobs", "sha512")
		if err := os.MkdirAll(blobsDir, 0755); err != nil {
			return fmt.Errorf("creating blobs directory: %w", err)
		}

		if err := os.WriteFile(blobPath, body, 0644); err != nil {
			return fmt.Errorf("writing mapping file: %w", err)
		}

		// Update the index.json file
		if err := updateIndexJSONFn(cacheDir, digestString, int64(len(body))); err != nil {
			return fmt.Errorf("updating index.json: %w", err)
		}

		// Get the XDG config directory for the symlink
		configDir, err := GetConfigDir()
		if err != nil {
			return fmt.Errorf("getting XDG config directory: %w", err)
		}

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, OrgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Create or update the symlink to point to the latest mappings file
		symlinkPath := filepath.Join(nestedConfigDir, "mappings.yaml")
		fmt.Fprintf(os.Stderr, "Created symlink to latest mappings at %s\n", symlinkPath)

		// Remove existing symlink if it exists
		_ = os.Remove(symlinkPath)
		// Create new symlink
		if err := os.Symlink(blobPath, symlinkPath); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Mappings checksum: sha512:%s\n", hashString)

	return nil
}
