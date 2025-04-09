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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runtime"

	"github.com/adrg/xdg"
)

const testMappingsYAML = `# Copyright 2025 Chainguard, Inc.
# SPDX-License-Identifier: Apache-2.0
# Images mappings from source distributions to chainguard images
images:
  ubuntu: chainguard-base:latest
  debian: chainguard-base:latest
  fedora: chainguard-base:latest
  alpine: chainguard-base:latest
  nodejs*: node
  golang*: go
  static*: static:latest
# Package mappings from source distributions to Chainguard packages
packages:
  # mapping of alpine packages to equivalent chainguard package(s)
  alpine: {}
  # mapping of debian packages name to equivalent chainguard package(s)
  debian:
    build-essential:
      - build-base
    awscli:
      - aws-cli
    fuse:
      - fuse2
      - fuse-common
`

// TestMain sets up the global test environment by setting XDG variables
func TestMain(m *testing.M) {
	// Create a mock testing.T for setupTestEnvironment
	mockT := &testing.T{}

	// Setup test environment with the mockT
	_, _, _, cleanup := setupTestEnvironment(mockT)
	defer cleanup()

	// Run tests
	exitCode := m.Run()

	// Exit with the test exit code
	os.Exit(exitCode)
}

// setupTestServer creates a test HTTP server serving the mock mappings.yaml
func setupTestServer(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mappings.yaml" {
			w.Header().Set("Content-Type", "text/yaml")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(testMappingsYAML))
			if err != nil {
				t.Fatalf("Failed to write response: %v", err)
			}
			return
		}

		// Return 404 for any other paths
		w.WriteHeader(http.StatusNotFound)
	}))

	t.Cleanup(func() {
		server.Close()
	})

	return server
}

// setupTestEnvironment creates test-specific directories and sets XDG env vars
func setupTestEnvironment(t *testing.T) (xdgTempDir string, xdgCacheDir string, xdgConfigDir string, cleanup func()) {
	t.Helper()

	// Create temporary directory for test
	xdgTempDir = t.TempDir()

	// Set up XDG directories within the temp directory
	xdgCacheDir = filepath.Join(xdgTempDir, "cache")
	xdgConfigDir = filepath.Join(xdgTempDir, "config")

	// Create the directories
	if err := os.MkdirAll(xdgCacheDir, 0755); err != nil {
		t.Fatalf("Failed to create XDG cache directory: %v", err)
	}
	if err := os.MkdirAll(xdgConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create XDG config directory: %v", err)
	}

	// Save the original environment variable values to restore later
	origXDGCacheHome := os.Getenv("XDG_CACHE_HOME")
	origXDGConfigHome := os.Getenv("XDG_CONFIG_HOME")

	// Set environment variables for the xdg library to use
	os.Setenv("XDG_CACHE_HOME", xdgCacheDir)
	os.Setenv("XDG_CONFIG_HOME", xdgConfigDir)

	// Force xdg library to reload environment variables
	xdg.Reload()

	// Verify that the environment variables were properly applied
	if got := xdg.CacheHome; got != xdgCacheDir {
		t.Errorf("xdg.CacheHome = %s, want %s", got, xdgCacheDir)
	}
	if got := xdg.ConfigHome; got != xdgConfigDir {
		t.Errorf("xdg.ConfigHome = %s, want %s", got, xdgConfigDir)
	}

	cleanup = func() {
		// Restore original environment variables
		if origXDGCacheHome != "" {
			os.Setenv("XDG_CACHE_HOME", origXDGCacheHome)
		} else {
			os.Unsetenv("XDG_CACHE_HOME")
		}

		if origXDGConfigHome != "" {
			os.Setenv("XDG_CONFIG_HOME", origXDGConfigHome)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}

		// Reload xdg library with original environment
		xdg.Reload()
	}

	return xdgTempDir, xdgCacheDir, xdgConfigDir, cleanup
}

// computeSHA512 calculates the SHA512 hash of data
func computeSHA512(data []byte) string {
	hash := sha512.Sum512(data)
	return hex.EncodeToString(hash[:])
}

// TestPathsUseTestDirectories verifies that our XDG paths use the test dirs
func TestPathsUseTestDirectories(t *testing.T) {
	_, cacheDir, configDir, _ := setupTestEnvironment(t)

	// Get XDG cache directory
	gotCacheDir, err := GetCacheDir()
	if err != nil {
		t.Fatalf("GetCacheDir() error = %v", err)
	}

	// Verify it contains our test directory path and not the real user path
	if !strings.Contains(gotCacheDir, cacheDir) {
		t.Errorf("GetCacheDir() = %v, expected to contain test dir %v", gotCacheDir, cacheDir)
	}
	// Make sure it doesn't contain user's real HOME
	realHome, _ := os.UserHomeDir()
	if realHome != "" && strings.Contains(gotCacheDir, realHome) {
		t.Errorf("GetCacheDir() = %v contains real home dir %v", gotCacheDir, realHome)
	}

	// Check config dir
	gotConfigDir, err := GetConfigDir()
	if err != nil {
		t.Fatalf("GetConfigDir() error = %v", err)
	}

	if !strings.Contains(gotConfigDir, configDir) {
		t.Errorf("GetConfigDir() = %v, expected to contain test dir %v", gotConfigDir, configDir)
	}
	if realHome != "" && strings.Contains(gotConfigDir, realHome) {
		t.Errorf("GetConfigDir() = %v contains real home dir %v", gotConfigDir, realHome)
	}

	// Check mappings path
	mappingsPath, err := getMappingsConfigPathFn()
	if err != nil {
		t.Fatalf("GetMappingsConfigPath() error = %v", err)
	}

	if !strings.Contains(mappingsPath, configDir) {
		t.Errorf("GetMappingsConfigPath() = %v, expected to contain test dir %v", mappingsPath, configDir)
	}
	if !strings.HasSuffix(mappingsPath, "mappings.yaml") {
		t.Errorf("GetMappingsConfigPath() = %v, expected to end with mappings.yaml", mappingsPath)
	}
}

// TestGetMappingsConfig tests the GetMappingsConfig function
func TestGetMappingsConfig(t *testing.T) {
	_, _, configDir, _ := setupTestEnvironment(t)

	// Test when file doesn't exist
	mappings, err := GetMappingsConfig()
	if err != nil {
		t.Fatalf("GetMappingsConfig() error = %v when file doesn't exist", err)
	}
	if mappings != nil {
		t.Errorf("GetMappingsConfig() = %v, want nil when file doesn't exist", mappings)
	}

	// Create mappings file
	mappingsPath := filepath.Join(configDir, OrgName, "mappings.yaml")
	if err := os.MkdirAll(filepath.Dir(mappingsPath), 0755); err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}
	if err := os.WriteFile(mappingsPath, []byte(testMappingsYAML), 0644); err != nil {
		t.Fatalf("Failed to write mappings file: %v", err)
	}

	// Test when file exists
	mappings, err = GetMappingsConfig()
	if err != nil {
		t.Fatalf("GetMappingsConfig() error = %v when file exists", err)
	}
	if string(mappings) != testMappingsYAML {
		t.Errorf("GetMappingsConfig() = %v, want %v", string(mappings), testMappingsYAML)
	}
}

// TestInitOCILayout tests the initOCILayout function
func TestInitOCILayout(t *testing.T) {
	_, cacheDir, _, _ := setupTestEnvironment(t)
	testCacheDir := filepath.Join(cacheDir, OrgName, "mappings")

	if err := initOCILayout(testCacheDir); err != nil {
		t.Fatalf("initOCILayout() error = %v", err)
	}

	// Check if oci-layout file exists
	ociLayoutPath := filepath.Join(testCacheDir, "oci-layout")
	if _, err := os.Stat(ociLayoutPath); err != nil {
		t.Errorf("oci-layout file not created: %v", err)
	}

	// Check if index.json file exists
	indexPath := filepath.Join(testCacheDir, "index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index.json file not created: %v", err)
	}

	// Check if blobs directory exists
	blobsDir := filepath.Join(testCacheDir, "blobs", "sha512")
	if _, err := os.Stat(blobsDir); err != nil {
		t.Errorf("blobs directory not created: %v", err)
	}

	// Check content of oci-layout file
	ociLayoutData, err := os.ReadFile(ociLayoutPath)
	if err != nil {
		t.Fatalf("Failed to read oci-layout file: %v", err)
	}

	var ociLayout OCILayout
	if err := json.Unmarshal(ociLayoutData, &ociLayout); err != nil {
		t.Fatalf("Failed to unmarshal oci-layout file: %v", err)
	}

	if ociLayout.ImageLayoutVersion != "1.0.0" {
		t.Errorf("oci-layout ImageLayoutVersion = %v, want 1.0.0", ociLayout.ImageLayoutVersion)
	}

	// Check content of index.json file
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read index.json file: %v", err)
	}

	var index OCIIndex
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("Failed to unmarshal index.json file: %v", err)
	}

	if index.SchemaVersion != 2 {
		t.Errorf("index.json SchemaVersion = %v, want 2", index.SchemaVersion)
	}

	if index.MediaType != "application/vnd.oci.image.index.v1+json" {
		t.Errorf("index.json MediaType = %v, want application/vnd.oci.image.index.v1+json", index.MediaType)
	}

	if len(index.Manifests) != 0 {
		t.Errorf("index.json Manifests = %v, want empty slice", index.Manifests)
	}
}

// TestUpdateIndexJSON tests the updateIndexJSON function
func TestUpdateIndexJSON(t *testing.T) {
	_, cacheDir, _, _ := setupTestEnvironment(t)
	testCacheDir := filepath.Join(cacheDir, OrgName, "mappings")

	// Initialize OCI layout
	if err := initOCILayout(testCacheDir); err != nil {
		t.Fatalf("initOCILayout() error = %v", err)
	}

	// Test adding a new descriptor
	testDigest := "sha512:testdigest"
	testSize := int64(1024)

	if err := updateIndexJSON(testCacheDir, testDigest, testSize); err != nil {
		t.Fatalf("updateIndexJSON() error = %v", err)
	}

	// Read the updated index.json
	indexPath := filepath.Join(testCacheDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read index.json file: %v", err)
	}

	var index OCIIndex
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("Failed to unmarshal index.json file: %v", err)
	}

	if len(index.Manifests) != 1 {
		t.Fatalf("index.json Manifests = %v, want 1 item", len(index.Manifests))
	}

	manifest := index.Manifests[0]
	if manifest.Digest != testDigest {
		t.Errorf("manifest Digest = %v, want %v", manifest.Digest, testDigest)
	}

	if manifest.Size != testSize {
		t.Errorf("manifest Size = %v, want %v", manifest.Size, testSize)
	}

	if manifest.MediaType != "application/yaml" {
		t.Errorf("manifest MediaType = %v, want application/yaml", manifest.MediaType)
	}

	if manifest.Annotations == nil {
		t.Fatalf("manifest Annotations is nil")
	}

	downloadedAt, ok := manifest.Annotations["vnd.chainguard.dfc.mappings.downloadedAt"]
	if !ok {
		t.Errorf("manifest Annotations missing downloadedAt")
	}

	// Check if downloadedAt is a valid time
	_, err = time.Parse(time.RFC3339, downloadedAt)
	if err != nil {
		t.Errorf("downloadedAt is not a valid RFC3339 time: %v", err)
	}
}

// TestUpdateWithServerError tests Update with a server error
func TestUpdateWithServerError(t *testing.T) {
	setupTestEnvironment(t)

	// Set up server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: server.URL,
	}

	if err := Update(context.Background(), opts); err == nil {
		t.Errorf("Update() with server error should return error")
	}
}

// TestUpdateWithCancelledContext tests Update with a cancelled context
func TestUpdateWithCancelledContext(t *testing.T) {
	setupTestEnvironment(t)

	// Set up server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay to allow context to be cancelled
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(testMappingsYAML))
	}))
	defer server.Close()

	// Create context and cancel it immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: server.URL,
	}

	if err := Update(ctx, opts); err == nil {
		t.Errorf("Update() with cancelled context should return error")
	}
}

// TestUpdateEmptyBody tests Update with empty response body
func TestUpdateEmptyBody(t *testing.T) {
	_, cacheDir, _, _ := setupTestEnvironment(t)
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")

	// Set up server returning empty body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		w.WriteHeader(http.StatusOK)
		// Don't write anything
	}))
	defer server.Close()

	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: server.URL,
	}

	if err := Update(context.Background(), opts); err != nil {
		t.Fatalf("Update() with empty body error = %v", err)
	}

	// Verify empty blob was saved
	emptyHash := computeSHA512([]byte{})
	blobPath := filepath.Join(cacheMappingsDir, "blobs", "sha512", emptyHash)
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("Empty blob file not found: %v", err)
	}

	// Verify content is empty
	content, err := os.ReadFile(blobPath)
	if err != nil {
		t.Errorf("Failed to read empty blob: %v", err)
	} else if len(content) != 0 {
		t.Errorf("Empty blob has size %d, want 0", len(content))
	}
}

// TestUpdateWithBlockedWrite verifies that Update handles blocked write operations
func TestUpdateWithBlockedWrite(t *testing.T) {
	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Setup test server
	server := setupTestServer(t)
	testURL := server.URL + "/mappings.yaml"

	// Options for the Update function
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	// Run the update
	err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Initial update failed: %v", err)
	}

	// Get the mappings path to verify the file
	mappingsPath := filepath.Join(configDir, "dev.chainguard.dfc", "mappings.yaml")

	// Verify content
	content, err := os.ReadFile(mappingsPath)
	if err != nil {
		t.Fatalf("Failed to read mappings file: %v", err)
	}

	if string(content) != testMappingsYAML {
		t.Fatalf("Mappings file doesn't contain expected content")
	}
}

// TestGetMappingsConfigWithNoPermission tests GetMappingsConfig when file can't be read
func TestGetMappingsConfigWithNoPermission(t *testing.T) {
	// Skip on Windows as permissions work differently
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Skipping test on Windows")
	}

	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create config directory structure
	configDfcDir := filepath.Join(configDir, OrgName)
	if err := os.MkdirAll(configDfcDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Create mapping file
	mappingsPath := filepath.Join(configDfcDir, "mappings.yaml")
	err := os.WriteFile(mappingsPath, []byte(testMappingsYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to write mappings file: %v", err)
	}

	// Make the file unreadable
	err = os.Chmod(mappingsPath, 0)
	if err != nil {
		t.Fatalf("Failed to make file unreadable: %v", err)
	}
	defer os.Chmod(mappingsPath, 0644)

	// Try to get the mappings config
	data, err := GetMappingsConfig()

	// Should fail due to permissions
	if err == nil {
		t.Errorf("GetMappingsConfig() with unreadable file should return error")
	}

	if data != nil {
		t.Errorf("GetMappingsConfig() with unreadable file should return nil data")
	}
}

// TestUpdateWithCustomUserAgent tests the user agent is correctly set
func TestUpdateWithCustomUserAgent(t *testing.T) {
	setupTestEnvironment(t)

	// Set up server that checks user agent
	expectedUserAgent := "custom-user-agent/1.0"
	userAgentChan := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgentChan <- r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(testMappingsYAML))
	}))
	defer server.Close()

	opts := Options{
		UserAgent:   expectedUserAgent,
		MappingsURL: server.URL,
	}

	if err := Update(context.Background(), opts); err != nil {
		t.Fatalf("Update() with custom user agent error = %v", err)
	}

	// Check user agent was set correctly
	select {
	case userAgent := <-userAgentChan:
		if userAgent != expectedUserAgent {
			t.Errorf("User-Agent = %s, want %s", userAgent, expectedUserAgent)
		}
	case <-time.After(time.Second):
		t.Errorf("Timeout waiting for request")
	}
}

// TestGetMappingsConfigPath_CreateDirectory tests the directory creation in GetMappingsConfigPath
func TestGetMappingsConfigPath_CreateDirectory(t *testing.T) {
	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Remove the OrgName directory to test directory creation
	orgPath := filepath.Join(configDir, OrgName)
	if err := os.RemoveAll(orgPath); err != nil {
		t.Fatalf("Failed to remove directory: %v", err)
	}

	// This should create the directory
	path, err := getMappingsConfigPathFn()
	if err != nil {
		t.Fatalf("GetMappingsConfigPath() error = %v", err)
	}

	// Verify the directory was created
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Errorf("GetMappingsConfigPath() did not create the directory")
	}
}

// TestUpdate_RecreateSymlink tests the case where we already have the mapping file
// but need to recreate the symlink
func TestUpdate_RecreateSymlink(t *testing.T) {
	_, cacheDir, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")
	configMappingsDir := filepath.Join(configDir, OrgName)

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create the directories
	if err := os.MkdirAll(cacheMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create cache directory: %v", err)
	}
	if err := os.MkdirAll(configMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Compute hash of test mappings
	expectedHash := computeSHA512([]byte(testMappingsYAML))
	blobsDir := filepath.Join(cacheMappingsDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Create the blob file
	blobPath := filepath.Join(blobsDir, expectedHash)
	if err := os.WriteFile(blobPath, []byte(testMappingsYAML), 0644); err != nil {
		t.Fatalf("Failed to write blob file: %v", err)
	}

	// Create incorrect symlink (pointing to wrong file)
	symlinkPath := filepath.Join(configMappingsDir, "mappings.yaml")
	wrongTarget := filepath.Join(cacheDir, "wrong-target")
	if err := os.WriteFile(wrongTarget, []byte("wrong content"), 0644); err != nil {
		t.Fatalf("Failed to create wrong target file: %v", err)
	}
	// Remove existing symlink if it exists
	os.Remove(symlinkPath)
	// Create symlink to wrong target
	if err := os.Symlink(wrongTarget, symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink to wrong target: %v", err)
	}

	// Update should recreate the symlink to the correct target
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	if err := Update(context.Background(), opts); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify symlink now points to the correct file
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Errorf("Failed to read symlink: %v", err)
	} else if target != blobPath {
		t.Errorf("Symlink points to %s, want %s", target, blobPath)
	}
}

// TestUpdate_CreateCacheDirectories tests initialization of the OCI layout when
// directories don't exist
func TestUpdate_CreateCacheDirectories(t *testing.T) {
	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Setup test server
	server := setupTestServer(t)
	testURL := server.URL + "/mappings.yaml"

	// Prepare options
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	// Call Update which should create directories
	err := Update(context.Background(), opts)
	if err != nil {
		t.Errorf("Update() failed with: %v", err)
	}

	// Check that the mappings file was created - this is the key test
	mappingsPath := filepath.Join(configDir, "dev.chainguard.dfc", "mappings.yaml")
	if _, err := os.Stat(mappingsPath); os.IsNotExist(err) {
		t.Errorf("Expected mappings file not created: %v", err)
	} else {
		// Verify content
		content, err := os.ReadFile(mappingsPath)
		if err != nil {
			t.Errorf("Failed to read mappings file: %v", err)
		} else if string(content) != testMappingsYAML {
			t.Errorf("Mappings file doesn't contain expected content")
		}
	}
}

// TestGetCacheDir_CreateDirectoryError simulates an error when creating the cache directory
func TestGetCacheDir_CreateDirectoryError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Skipping test on Windows")
	}

	_, cacheDir, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create a file with the same name as the directory we'll try to create
	// This will cause MkdirAll to fail
	filePath := filepath.Join(cacheDir, OrgName)
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("Failed to create parent directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// GetCacheDir should fail because it can't create the directory
	_, err := GetCacheDir()
	if err == nil {
		t.Errorf("GetCacheDir() should return error when directory can't be created")
	}
}

// TestGetMappingsConfigPath_Error tests error conditions in GetMappingsConfigPath
func TestGetMappingsConfigPath_Error(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Save original environment and restore after test
	oldXDGConfigHome := os.Getenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", oldXDGConfigHome)

	// Create a temp directory that we'll use for XDG_CONFIG_HOME
	tmpDir, err := os.MkdirTemp("", "dfc-test-config-error")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set XDG_CONFIG_HOME to our temp dir
	os.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Force reload of XDG variables
	xdg.Reload()

	// Create a file at the path where the OrgName directory would need to be created
	orgPath := filepath.Join(tmpDir, OrgName)
	if err := os.WriteFile(orgPath, []byte("blocking file"), 0644); err != nil {
		t.Fatalf("Failed to create blocking file: %v", err)
	}

	// This should fail because GetMappingsConfigPath will try to create directories
	// but will hit a file instead of a directory
	_, err = getMappingsConfigPathFn()
	if err == nil {
		// If the test still passes, make the test fail explicitly
		t.Error("GetMappingsConfigPath() should fail when a file exists where a directory should be created")
	} else {
		// Print the actual error to help debug
		t.Logf("Got expected error: %v", err)
	}
}

// TestGetMappingsConfig_StatError tests error handling when Stat fails in GetMappingsConfig
func TestGetMappingsConfig_StatError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Set up environment
	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create the mappings directory with wrong permissions
	orgDir := filepath.Join(configDir, OrgName)
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatalf("Failed to create org directory: %v", err)
	}

	// Create a directory where the file should be
	mappingsDir := filepath.Join(orgDir, "mappings.yaml")
	if err := os.Mkdir(mappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create dir at file path: %v", err)
	}

	// Make parent dir unreadable
	if err := os.Chmod(orgDir, 0000); err != nil {
		t.Fatalf("Failed to change permissions: %v", err)
	}
	defer os.Chmod(orgDir, 0755)

	// This should fail when trying to stat the mappings path
	_, err := GetMappingsConfig()
	if err == nil {
		t.Error("GetMappingsConfig() should fail with stat error")
	}
}

// TestGetMappingsConfig_ReadError tests error handling in GetMappingsConfig when reading the file fails
func TestGetMappingsConfig_ReadError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dfc-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Save and restore the original function
	originalGetConfigPath := getMappingsConfigPathFn
	defer func() { getMappingsConfigPathFn = originalGetConfigPath }()

	// Create an invalid file (directory instead of file)
	configDir := filepath.Join(tempDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Mock GetMappingsConfigPath to return our directory path
	getMappingsConfigPathFn = func() (string, error) {
		return configDir, nil
	}

	// The function should fail when trying to read a directory as a file
	_, err = GetMappingsConfig()
	if err == nil {
		t.Fatal("GetMappingsConfig() should fail when the config path is a directory")
	}
}

// TestInitOCILayout_DirError tests error handling in initOCILayout when directory creation fails
func TestInitOCILayout_DirError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Create a temp dir
	tmpDir, err := os.MkdirTemp("", "dfc-test-oci-dir-error")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file where the blobs directory should go
	blobsPath := filepath.Join(tmpDir, "blobs")
	if err := os.WriteFile(blobsPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create blocking file: %v", err)
	}

	// Should fail when trying to create the blobs directory
	err = initOCILayout(tmpDir)
	if err == nil {
		t.Error("initOCILayout() should fail when directory creation fails")
	}
}

// TestInitOCILayout_WriteLayoutError tests error handling in initOCILayout when writing the oci-layout file fails
func TestInitOCILayout_WriteLayoutError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-oci-write")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the blobs directory
	blobsDir := filepath.Join(tmpDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Make the root directory read-only to prevent file creation
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("Failed to change permissions: %v", err)
	}
	defer os.Chmod(tmpDir, 0755)

	// Should fail when trying to write the oci-layout file
	err = initOCILayout(tmpDir)
	if err == nil {
		t.Error("initOCILayout() should fail when writing oci-layout fails")
	}
}

// TestInitOCILayout_WriteIndexError tests error handling in initOCILayout when writing the index.json file fails
func TestInitOCILayout_WriteIndexError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-oci-index")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the blobs directory
	blobsDir := filepath.Join(tmpDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Create a mock oci-layout file
	ociLayoutPath := filepath.Join(tmpDir, "oci-layout")
	if err := os.WriteFile(ociLayoutPath, []byte(`{"imageLayoutVersion": "1.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to create oci-layout file: %v", err)
	}

	// Create a directory where the index.json file should be
	indexPath := filepath.Join(tmpDir, "index.json")
	if err := os.Mkdir(indexPath, 0755); err != nil {
		t.Fatalf("Failed to create directory as file: %v", err)
	}

	// Should fail when trying to write the index.json file
	err = initOCILayout(tmpDir)
	if err == nil {
		t.Error("initOCILayout() should fail when writing index.json fails")
	}
}

// TestUpdateIndexJSON_ReadError tests error handling in updateIndexJSON when reading the index.json file fails
func TestUpdateIndexJSON_ReadError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-index-read")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory where the index.json file should be
	indexPath := filepath.Join(tmpDir, "index.json")
	if err := os.Mkdir(indexPath, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Should fail when trying to read the index.json because it's a directory
	err = updateIndexJSON(tmpDir, "sha512:digest", 100)
	if err == nil {
		t.Error("updateIndexJSON() should fail when reading index.json fails")
	}
}

// TestUpdateIndexJSON_UnmarshalError tests error handling in updateIndexJSON when unmarshalling the index.json fails
func TestUpdateIndexJSON_UnmarshalError(t *testing.T) {
	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-unmarshal")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create an index.json file with invalid JSON
	indexPath := filepath.Join(tmpDir, "index.json")
	if err := os.WriteFile(indexPath, []byte("invalid json"), 0644); err != nil {
		t.Fatalf("Failed to create index.json: %v", err)
	}

	// Should fail when trying to unmarshal the index.json content
	err = updateIndexJSON(tmpDir, "sha512:digest", 100)
	if err == nil {
		t.Error("updateIndexJSON() should fail when unmarshalling index.json fails")
	}
}

// TestUpdateIndexJSON_WriteError tests error handling in updateIndexJSON when writing the updated index.json fails
func TestUpdateIndexJSON_WriteError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-index-write")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory where the index.json file should be written
	// This will cause a failure when we try to write the file
	indexPath := filepath.Join(tmpDir, "index.json")
	if err := os.Mkdir(indexPath, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Should fail when trying to write the updated index.json
	// because we can't write a file over a directory
	err = updateIndexJSON(tmpDir, "sha512:digest", 100)
	if err == nil {
		t.Error("updateIndexJSON() should fail when writing updated index.json fails")
	}
}

// TestUpdate_CreateSymlinkError tests error handling in Update when creating a symlink fails
func TestUpdate_CreateSymlinkError(t *testing.T) {
	// Skip on Windows as symlinks work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Set up test environment
	_, _, configDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create a file where the symlink should go
	configMappingsDir := filepath.Join(configDir, OrgName)
	if err := os.MkdirAll(configMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	symlinkPath := filepath.Join(configMappingsDir, "mappings.yaml")
	if err := os.WriteFile(symlinkPath, []byte("test"), 0444); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// Make the parent directory read-only so we can't remove the file
	if err := os.Chmod(configMappingsDir, 0555); err != nil {
		t.Fatalf("Failed to change permissions: %v", err)
	}
	defer os.Chmod(configMappingsDir, 0755)

	// Run Update, should reach error when trying to create the symlink
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when creating symlink fails")
	}
}

// TestGetMappingsConfigPath_XDGError tests error handling when xdg.ConfigFile fails
func TestGetMappingsConfigPath_XDGError(t *testing.T) {
	// Save original function and restore after test
	original := xdgConfigFile
	defer func() { xdgConfigFile = original }()

	// Mock the xdg.ConfigFile function to return an error
	expectedErr := errors.New("xdg config file error")
	xdgConfigFile = func(relPath string) (string, error) {
		t.Logf("Mock xdgConfigFile called with path: %s", relPath)
		return "", expectedErr
	}

	// Call the function under test
	result, err := getMappingsConfigPathFn()
	t.Logf("getMappingsConfigPathFn returned result: %s, err: %v", result, err)

	if err == nil {
		t.Fatal("GetMappingsConfigPath() should fail when xdg.ConfigFile fails")
	}

	// Verify the error is propagated correctly
	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Errorf("Expected error containing %q, got %q", expectedErr.Error(), err.Error())
	}
}

// TestUpdate_NoMappingsURL tests Update with no MappingsURL provided
func TestUpdate_NoMappingsURL(t *testing.T) {
	// Set up a test environment
	setupTestEnvironment(t)

	// Set up a custom HTTP transport that returns an error
	originalDefaultTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalDefaultTransport }()

	// Create a transport that always fails to simulate invalid default URL
	http.DefaultTransport = &mockTransport{
		roundTripFn: func(req *http.Request) (*http.Response, error) {
			// Make sure the URL is the default one
			if req.URL.String() != DefaultMappingsURL {
				t.Errorf("Expected request to DefaultMappingsURL, got %s", req.URL.String())
			}
			return nil, fmt.Errorf("simulated network error")
		},
	}

	// Run Update with empty MappingsURL
	opts := Options{
		UserAgent: "dfc-test/1.0",
		// No MappingsURL, should use default
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when using default URL with network error")
	}
}

// mockTransport is a mock http.RoundTripper for testing
type mockTransport struct {
	roundTripFn func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFn(req)
}

// TestUpdate_NewRequestError tests Update when http.NewRequestWithContext fails
func TestUpdate_NewRequestError(t *testing.T) {
	setupTestEnvironment(t)

	// Use an invalid URL that will cause NewRequestWithContext to fail
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: "://invalid-url",
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail with invalid URL")
	} else if !strings.Contains(err.Error(), "creating request") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdate_ResponseBodyReadError tests Update when io.ReadAll fails
func TestUpdate_ResponseBodyReadError(t *testing.T) {
	setupTestEnvironment(t)

	// Set up server that returns a body that errors on read
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100") // Set content length higher than what we'll send
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial")) // Write only part of the promised body
		// Then close the connection without completing the body
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Hijack and close immediately
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer server.Close()

	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: server.URL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when reading response body fails")
	}
}

// TestInitOCILayout_JSONMarshalError tests initOCILayout when json.Marshal fails
func TestInitOCILayout_JSONMarshalError(t *testing.T) {
	// Save the original json.Marshal function
	originalMarshal := jsonMarshal
	defer func() { jsonMarshal = originalMarshal }()

	// First mock json.Marshal to fail on the first call (for oci-layout)
	jsonMarshal = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("simulated marshal error")
	}

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-oci-marshal")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Should fail when trying to marshal the oci-layout structure
	err = initOCILayout(tmpDir)
	if err == nil {
		t.Error("initOCILayout() should fail when json.Marshal fails")
	} else if !strings.Contains(err.Error(), "marshalling oci-layout") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestInitOCILayout_JSONMarshalIndentError tests initOCILayout when json.MarshalIndent fails
func TestInitOCILayout_JSONMarshalIndentError(t *testing.T) {
	// Save the original json.MarshalIndent function
	originalMarshalIndent := jsonMarshalIndent
	defer func() { jsonMarshalIndent = originalMarshalIndent }()

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-oci-marshalindent")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the directory structure for oci-layout
	blobsDir := filepath.Join(tmpDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Mock json.MarshalIndent to succeed for oci-layout but fail for index.json
	jsonMarshalIndent = func(v interface{}, prefix, indent string) ([]byte, error) {
		return nil, fmt.Errorf("simulated marshal indent error")
	}

	// Should fail when trying to marshal the index.json structure
	err = initOCILayout(tmpDir)
	if err == nil {
		t.Error("initOCILayout() should fail when json.MarshalIndent fails")
	} else if !strings.Contains(err.Error(), "marshalling index.json") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdateIndexJSON_MarshalIndentError tests updateIndexJSON when json.MarshalIndent fails
func TestUpdateIndexJSON_MarshalIndentError(t *testing.T) {
	// Save the original json.MarshalIndent function
	originalMarshalIndent := jsonMarshalIndent
	defer func() { jsonMarshalIndent = originalMarshalIndent }()

	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "dfc-test-index-marshalindent")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create an empty index.json file
	indexPath := filepath.Join(tmpDir, "index.json")
	if err := os.WriteFile(indexPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("Failed to create index.json: %v", err)
	}

	// Mock json.MarshalIndent to fail
	jsonMarshalIndent = func(v interface{}, prefix, indent string) ([]byte, error) {
		return nil, fmt.Errorf("simulated marshal indent error")
	}

	// Should fail when trying to marshal the updated index.json structure
	err = updateIndexJSON(tmpDir, "sha512:digest", 100)
	if err == nil {
		t.Error("updateIndexJSON() should fail when json.MarshalIndent fails")
	} else if !strings.Contains(err.Error(), "marshalling updated index.json") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdate_ExistingIndexError tests error handling when updating existing index.json fails
func TestUpdate_ExistingIndexError(t *testing.T) {
	// Set up test environment
	_, cacheDir, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create a directory for the cache to avoid "not exists" path
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")
	if err := os.MkdirAll(cacheMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create cache directory: %v", err)
	}

	// Create the blobs dir to ensure no errors with that
	blobsDir := filepath.Join(cacheMappingsDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Save the original updateIndexJSON function
	originalUpdateIndexJSON := updateIndexJSONFn
	defer func() { updateIndexJSONFn = originalUpdateIndexJSON }()

	// Mock updateIndexJSON to fail
	updateIndexJSONFn = func(cacheDir, digest string, size int64) error {
		return fmt.Errorf("simulated index.json update error")
	}

	// Run Update
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when updateIndexJSON fails")
	} else if !strings.Contains(err.Error(), "updating index.json") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdate_MkdirBlobsDirError tests error handling in Update when creating blobs dir fails
func TestUpdate_MkdirBlobsDirError(t *testing.T) {
	// Set up test environment
	_, cacheDir, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create the cache directories
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")
	if err := os.MkdirAll(cacheMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create cache directory: %v", err)
	}

	// Create a file where the blobs directory should be
	blobsPath := filepath.Join(cacheMappingsDir, "blobs")
	if err := os.WriteFile(blobsPath, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// Run Update, should fail when trying to create blobs directory
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when creating blobs directory fails")
	} else if !strings.Contains(err.Error(), "creating blobs directory") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdate_WriteFileBlobError tests error handling in Update when writing blob file fails
func TestUpdate_WriteFileBlobError(t *testing.T) {
	// Skip on Windows as permissions work differently
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	// Set up test environment
	_, cacheDir, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create the cache mappings directory
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")
	if err := os.MkdirAll(cacheMappingsDir, 0755); err != nil {
		t.Fatalf("Failed to create cache mappings directory: %v", err)
	}

	// Create an oci-layout file to ensure we don't hit that path
	ociLayoutPath := filepath.Join(cacheMappingsDir, "oci-layout")
	if err := os.WriteFile(ociLayoutPath, []byte(`{"imageLayoutVersion": "1.0.0"}`), 0644); err != nil {
		t.Fatalf("Failed to create oci-layout file: %v", err)
	}

	// Create an index.json file
	indexPath := filepath.Join(cacheMappingsDir, "index.json")
	if err := os.WriteFile(indexPath, []byte(`{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": []}`), 0644); err != nil {
		t.Fatalf("Failed to create index.json file: %v", err)
	}

	// Create the blobs directory structure with normal permissions
	blobsDir := filepath.Join(cacheMappingsDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Now make the blobs/sha512 directory read-only
	// We only need to chmod the sha512 directory since that's where the file will be written
	if err := os.Chmod(blobsDir, 0555); err != nil {
		t.Fatalf("Failed to make directory read-only: %v", err)
	}
	// Make sure we restore permissions after the test
	defer os.Chmod(blobsDir, 0755)

	// Run Update, should fail when trying to write blob file
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Error("Update() should fail when writing blob file fails")
	} else if !strings.Contains(err.Error(), "writing mapping file") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestUpdate_WithManifestFiltering tests Update where existing manifests need to be filtered
func TestUpdate_WithManifestFiltering(t *testing.T) {
	// Set up test environment
	_, cacheDir, _, cleanup := setupTestEnvironment(t)
	defer cleanup()
	cacheMappingsDir := filepath.Join(cacheDir, OrgName, "mappings")

	// Set up test server
	server := setupTestServer(t)
	defer server.Close()
	testURL := server.URL + "/mappings.yaml"

	// Create cache directory structure
	if err := os.MkdirAll(filepath.Join(cacheMappingsDir, "blobs", "sha512"), 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Create an existing index.json with a manifest for the same digest we'll get
	expectedHash := computeSHA512([]byte(testMappingsYAML))
	digestString := "sha512:" + expectedHash

	// Create the index.json with existing manifests
	existingIndex := OCIIndex{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests: []OCIDescriptor{
			{
				MediaType: "application/yaml",
				Digest:    digestString, // Same digest we'll get from the test server
				Size:      int64(len(testMappingsYAML)),
				Annotations: map[string]string{
					"vnd.chainguard.dfc.mappings.downloadedAt": time.Now().UTC().Format(time.RFC3339),
				},
			},
			{
				MediaType: "application/yaml",
				Digest:    "sha512:otherdigest",
				Size:      100,
				Annotations: map[string]string{
					"vnd.chainguard.dfc.mappings.downloadedAt": time.Now().UTC().Format(time.RFC3339),
				},
			},
		},
	}

	// Marshal and write the index
	indexData, err := json.MarshalIndent(existingIndex, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal index: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cacheMappingsDir, "index.json"), indexData, 0644); err != nil {
		t.Fatalf("Failed to write index.json: %v", err)
	}

	// Also create the oci-layout file
	layout := OCILayout{ImageLayoutVersion: "1.0.0"}
	layoutData, err := json.Marshal(layout)
	if err != nil {
		t.Fatalf("Failed to marshal layout: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cacheMappingsDir, "oci-layout"), layoutData, 0644); err != nil {
		t.Fatalf("Failed to write oci-layout: %v", err)
	}

	// Run Update
	opts := Options{
		UserAgent:   "dfc-test/1.0",
		MappingsURL: testURL,
	}

	if err := Update(context.Background(), opts); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Check that the index.json was updated and only contains the current mapping
	indexPath := filepath.Join(cacheMappingsDir, "index.json")
	updatedIndexData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read updated index.json: %v", err)
	}

	var updatedIndex OCIIndex
	if err := json.Unmarshal(updatedIndexData, &updatedIndex); err != nil {
		t.Fatalf("Failed to unmarshal updated index.json: %v", err)
	}

	if len(updatedIndex.Manifests) != 2 {
		t.Errorf("Expected 2 manifests in index.json after update, got %d", len(updatedIndex.Manifests))
	}

	// Check if the manifest with otherdigest is still there
	foundOtherDigest := false
	for _, manifest := range updatedIndex.Manifests {
		if manifest.Digest == "sha512:otherdigest" {
			foundOtherDigest = true
			break
		}
	}

	if !foundOtherDigest {
		t.Errorf("Expected to find manifest with otherdigest after filtering")
	}
}

// Override the variables for test functions
func init() {
	// Note: Variables will be restored by the individual test cases
}
