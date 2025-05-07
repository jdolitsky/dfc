/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/chainguard-dev/clog"
)

const (
	// defaultMappingsURL is the default URL for fetching mappings
	defaultMappingsURL = "https://raw.githubusercontent.com/chainguard-dev/dfc/refs/heads/main/pkg/dfc/builtin-mappings.yaml"

	// orgName is the organization name used in XDG paths
	orgName = "dev.chainguard.dfc"
)

// UpdateOptions configures the update behavior
type UpdateOptions struct {
	// UserAgent is the user agent string to use for update requests
	UserAgent string

	// MappingsURL is the URL to fetch the latest mappings from
	MappingsURL string

	// UseDB indicates whether to use the .db file instead of .yaml
	UseDB bool
}

// ociLayout represents the oci-layout file
type ociLayout struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

// ociIndex represents the index.json file
type ociIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
}

// ociDescriptor represents a descriptor in the index
type ociDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// getCacheDirFunc is a variable that holds the function to get the cache directory
// This allows it to be mocked in tests
var getCacheDirFunc = func() string {
	// Use xdg library to get the cache directory path
	return filepath.Join(xdg.CacheHome, orgName, "mappings")
}

// getCacheDir returns the XDG cache directory for dfc using the xdg library
func getCacheDir() string {
	return getCacheDirFunc()
}

// getConfigDirFunc is a variable that holds the function to get the config directory
// This allows it to be mocked in tests
var getConfigDirFunc = func() string {
	// Use xdg library to get the config directory
	return xdg.ConfigHome
}

// getConfigDir returns the XDG config directory for dfc using the xdg library
func getConfigDir() string {
	return getConfigDirFunc()
}

// GetMappingsConfigPath returns the path to the builtin-mappings.yaml file in XDG_CONFIG_HOME
func getMappingsConfigPath() (string, error) {
	// Use xdg library's ConfigFile to get the proper location
	mappingsPath, err := xdg.ConfigFile(filepath.Join(orgName, "builtin-mappings.yaml"))
	if err != nil {
		return "", fmt.Errorf("getting mappings config path: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(mappingsPath), 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}

	return mappingsPath, nil
}

// getMappingsConfig reads and returns the contents of the builtin-mappings.yaml file
func getMappingsConfig() ([]byte, error) {
	mappingsPath, err := getMappingsConfigPath()
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
	// Create the blobs/sha256 directory
	blobsDir := filepath.Join(cacheDir, "blobs", "sha256")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		return fmt.Errorf("creating blobs directory: %w", err)
	}

	// Create the oci-layout file
	layout := ociLayout{ImageLayoutVersion: "1.0.0"}
	layoutData, err := json.Marshal(layout)
	if err != nil {
		return fmt.Errorf("marshalling oci-layout: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cacheDir, "oci-layout"), layoutData, 0600); err != nil {
		return fmt.Errorf("writing oci-layout file: %w", err)
	}

	// Create an empty index.json file
	index := ociIndex{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests:     []ociDescriptor{},
	}

	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling index.json: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cacheDir, "index.json"), indexData, 0600); err != nil {
		return fmt.Errorf("writing index.json file: %w", err)
	}

	return nil
}

// updateIndexJSON updates the index.json file with the new mapping blob
func updateIndexJSON(cacheDir, digest string, size int64, fileType string) error {
	// Read the current index.json
	indexPath := filepath.Join(cacheDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading index.json: %w", err)
	}

	var index ociIndex
	if len(indexData) > 0 {
		if err := json.Unmarshal(indexData, &index); err != nil {
			return fmt.Errorf("unmarshalling index.json: %w", err)
		}
	} else {
		// Initialize a new index
		index = ociIndex{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.index.v1+json",
			Manifests:     []ociDescriptor{},
		}
	}

	// Remove any existing entries with this digest
	filteredManifests := []ociDescriptor{}
	for _, manifest := range index.Manifests {
		// Skip if it has the same digest
		if manifest.Digest == digest {
			continue
		}
		filteredManifests = append(filteredManifests, manifest)
	}

	// Create a new descriptor for the mapping
	now := time.Now().UTC().Format(time.RFC3339)

	var mediaType string
	switch fileType {
	case "yaml":
		mediaType = "application/yaml"
	case "db":
		mediaType = "application/vnd.sqlite3"
	default:
		mediaType = "application/octet-stream"
	}

	descriptor := ociDescriptor{
		MediaType: mediaType,
		Digest:    digest,
		Size:      size,
		Annotations: map[string]string{
			"vnd.chainguard.dfc.mappings.downloadedAt": now,
			"vnd.chainguard.dfc.mappings.type":         fileType,
		},
	}

	// Add the new descriptor
	filteredManifests = append(filteredManifests, descriptor)
	index.Manifests = filteredManifests

	// Write the updated index.json
	updatedIndexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling updated index.json: %w", err)
	}

	if err := os.WriteFile(indexPath, updatedIndexData, 0600); err != nil {
		return fmt.Errorf("writing updated index.json: %w", err)
	}

	return nil
}

// Update checks for available updates to the dfc tool
func Update(ctx context.Context, opts UpdateOptions) error {
	log := clog.FromContext(ctx)
	log.Info("Checking for mappings update...")

	// Set default MappingsURL if not provided
	mappingsURL := opts.MappingsURL
	if mappingsURL == "" {
		mappingsURL = defaultMappingsURL
	}

	// Update the YAML file first
	if err := updateMappingsFile(ctx, mappingsURL, opts.UserAgent); err != nil {
		return fmt.Errorf("updating YAML mappings: %w", err)
	}

	// If UseDB flag is true, also update the DB file
	if opts.UseDB {
		// Derive the DB URL from the YAML URL (replace .yaml with .db)
		dbURL := strings.TrimSuffix(mappingsURL, ".yaml") + ".db"
		if err := updateDatabaseFile(ctx, dbURL, opts.UserAgent); err != nil {
			log.Warn("Failed to update database file, will try to generate from YAML", "error", err)

			// Fall back to generating the DB from the updated YAML
			yamlPath, err := getMappingsConfigPath()
			if err != nil {
				return fmt.Errorf("getting YAML path: %w", err)
			}

			dbPath, err := getDBPath()
			if err != nil {
				return fmt.Errorf("getting DB path: %w", err)
			}

			log.Info("Generating database from updated YAML file")
			if err := CreateDBFromYAML(ctx, yamlPath, dbPath); err != nil {
				log.Warn("Failed to generate database from YAML", "error", err)
				// Continue without error, will fall back to YAML
			}
		}
	}

	return nil
}

// updateMappingsFile downloads and processes the YAML mapping file
func updateMappingsFile(ctx context.Context, mappingsURL, userAgent string) error {
	log := clog.FromContext(ctx)
	log.Info("Updating YAML mappings file", "url", mappingsURL)

	// Create a new HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mappingsURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Set the User-Agent header
	setUserAgent := userAgent
	if setUserAgent == "" {
		setUserAgent = "dfc/dev"
	}
	req.Header.Set("User-Agent", setUserAgent)

	// Send the request
	log.Debug("Fetching mappings", "url", mappingsURL)
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

	// Check if XDG cache directory exists, create it if not
	cacheDir := getCacheDir()
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		log.Debug("Creating cache directory", "path", cacheDir)
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("creating cache directory: %w", err)
		}
		if err := initOCILayout(cacheDir); err != nil {
			return fmt.Errorf("initializing OCI layout: %w", err)
		}
	}

	// Calculate SHA-256 hash of the mapping file
	hash := sha256.New()
	hash.Write(body)
	hashBytes := hash.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)
	digestString := "sha256:" + hashString

	// Create the path to store the mapping file
	blobPath := filepath.Join(cacheDir, "blobs", "sha256", hashString)

	// Check if the file already exists
	if _, err := os.Stat(blobPath); err == nil {
		// Get the XDG config directory for the symlink
		configDir := getConfigDir()

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, orgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Check if the symlink exists and points to the correct file
		symlinkPath := filepath.Join(nestedConfigDir, "builtin-mappings.yaml")
		currentTarget, err := os.Readlink(symlinkPath)
		if err != nil || currentTarget != blobPath {
			// Remove existing symlink if it exists
			_ = os.Remove(symlinkPath)
			// Create new symlink
			if err := os.Symlink(blobPath, symlinkPath); err != nil {
				return fmt.Errorf("creating symlink: %w", err)
			}
		}

		log.Info("Already have latest YAML mappings", "location", symlinkPath)
	} else {
		log.Info("Saving latest version of YAML mappings", "location", blobPath)

		// Save the mapping file
		blobsDir := filepath.Join(cacheDir, "blobs", "sha256")
		if err := os.MkdirAll(blobsDir, 0755); err != nil {
			return fmt.Errorf("creating blobs directory: %w", err)
		}

		if err := os.WriteFile(blobPath, body, 0600); err != nil {
			return fmt.Errorf("writing mapping file: %w", err)
		}

		// Update the index.json file
		if err := updateIndexJSON(cacheDir, digestString, int64(len(body)), "yaml"); err != nil {
			return fmt.Errorf("updating index.json: %w", err)
		}

		// Get the XDG config directory for the symlink
		configDir := getConfigDir()

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, orgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Create or update the symlink to point to the latest mappings file
		symlinkPath := filepath.Join(nestedConfigDir, "builtin-mappings.yaml")
		log.Info("Created symlink to latest YAML mappings", "location", symlinkPath)

		// Remove existing symlink if it exists
		_ = os.Remove(symlinkPath)
		// Create new symlink
		if err := os.Symlink(blobPath, symlinkPath); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
	}

	log.Info("YAML mappings checksum", "sha256", hashString)

	return nil
}

// updateDatabaseFile downloads and processes the SQLite database file
func updateDatabaseFile(ctx context.Context, dbURL, userAgent string) error {
	log := clog.FromContext(ctx)
	log.Info("Updating SQLite database file", "url", dbURL)

	// Create a new HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dbURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Set the User-Agent header
	setUserAgent := userAgent
	if setUserAgent == "" {
		setUserAgent = "dfc/dev"
	}
	req.Header.Set("User-Agent", setUserAgent)

	// Send the request
	log.Debug("Fetching database", "url", dbURL)
	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching database: %w", err)
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

	// Check if XDG cache directory exists, create it if not
	cacheDir := getCacheDir()
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		log.Debug("Creating cache directory", "path", cacheDir)
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return fmt.Errorf("creating cache directory: %w", err)
		}
		if err := initOCILayout(cacheDir); err != nil {
			return fmt.Errorf("initializing OCI layout: %w", err)
		}
	}

	// Calculate SHA-256 hash of the database file
	hash := sha256.New()
	hash.Write(body)
	hashBytes := hash.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)
	digestString := "sha256:" + hashString

	// Create the path to store the database file
	blobPath := filepath.Join(cacheDir, "blobs", "sha256", hashString)

	// Check if the file already exists
	if _, err := os.Stat(blobPath); err == nil {
		// Get the XDG config directory for the symlink
		configDir := getConfigDir()

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, orgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Check if the symlink exists and points to the correct file
		symlinkPath := filepath.Join(nestedConfigDir, "builtin-mappings.db")
		currentTarget, err := os.Readlink(symlinkPath)
		if err != nil || currentTarget != blobPath {
			// Remove existing symlink if it exists
			_ = os.Remove(symlinkPath)
			// Create new symlink
			if err := os.Symlink(blobPath, symlinkPath); err != nil {
				return fmt.Errorf("creating symlink: %w", err)
			}
		}

		log.Info("Already have latest database", "location", symlinkPath)
	} else {
		log.Info("Saving latest version of database", "location", blobPath)

		// Save the database file
		blobsDir := filepath.Join(cacheDir, "blobs", "sha256")
		if err := os.MkdirAll(blobsDir, 0755); err != nil {
			return fmt.Errorf("creating blobs directory: %w", err)
		}

		if err := os.WriteFile(blobPath, body, 0600); err != nil {
			return fmt.Errorf("writing database file: %w", err)
		}

		// Update the index.json file
		if err := updateIndexJSON(cacheDir, digestString, int64(len(body)), "db"); err != nil {
			return fmt.Errorf("updating index.json: %w", err)
		}

		// Get the XDG config directory for the symlink
		configDir := getConfigDir()

		// Ensure the nested config directory exists
		nestedConfigDir := filepath.Join(configDir, orgName)
		if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
			return fmt.Errorf("creating nested config directory: %w", err)
		}

		// Create or update the symlink to point to the latest database file
		symlinkPath := filepath.Join(nestedConfigDir, "builtin-mappings.db")
		log.Info("Created symlink to latest database", "location", symlinkPath)

		// Remove existing symlink if it exists
		_ = os.Remove(symlinkPath)
		// Create new symlink
		if err := os.Symlink(blobPath, symlinkPath); err != nil {
			return fmt.Errorf("creating symlink: %w", err)
		}
	}

	log.Info("Database checksum", "sha256", hashString)

	return nil
}
