/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	gotCacheDir := getCacheDir()

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
	gotConfigDir := getConfigDir()

	if !strings.Contains(gotConfigDir, configDir) {
		t.Errorf("GetConfigDir() = %v, expected to contain test dir %v", gotConfigDir, configDir)
	}
	if realHome != "" && strings.Contains(gotConfigDir, realHome) {
		t.Errorf("GetConfigDir() = %v contains real home dir %v", gotConfigDir, realHome)
	}

	// Check mappings path
	mappingsPath, err := getMappingsConfigPath()
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
	mappings, err := getMappingsConfig()
	if err != nil {
		t.Fatalf("GetMappingsConfig() error = %v when file doesn't exist", err)
	}
	if mappings != nil {
		t.Errorf("GetMappingsConfig() = %v, want nil when file doesn't exist", mappings)
	}

	// Create mappings file
	mappingsPath := filepath.Join(configDir, orgName, "mappings.yaml")
	if err := os.MkdirAll(filepath.Dir(mappingsPath), 0755); err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}
	if err := os.WriteFile(mappingsPath, []byte(testMappingsYAML), 0644); err != nil {
		t.Fatalf("Failed to write mappings file: %v", err)
	}

	// Test when file exists
	mappings, err = getMappingsConfig()
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
	testCacheDir := filepath.Join(cacheDir, orgName, "mappings")

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
}

// TestUpdateIndexJSON tests the updateIndexJSON function
func TestUpdateIndexJSON(t *testing.T) {
	_, cacheDir, _, _ := setupTestEnvironment(t)
	testCacheDir := filepath.Join(cacheDir, orgName, "mappings")

	// Create the OCI layout
	if err := initOCILayout(testCacheDir); err != nil {
		t.Fatalf("Failed to initialize OCI layout: %v", err)
	}

	// Check initial index.json
	indexPath := filepath.Join(testCacheDir, "index.json")
	initialIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read initial index.json: %v", err)
	}

	var index ociIndex
	if err := json.Unmarshal(initialIndex, &index); err != nil {
		t.Fatalf("Failed to unmarshal initial index.json: %v", err)
	}

	// Verify index is empty
	if len(index.Manifests) != 0 {
		t.Errorf("Initial index.json has manifests, expected none")
	}

	// Update index.json
	testDigest := "sha512:abcdef123456"
	testSize := int64(1024)
	if err := updateIndexJSON(testCacheDir, testDigest, testSize); err != nil {
		t.Fatalf("updateIndexJSON() error = %v", err)
	}

	// Check updated index.json
	updatedIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read updated index.json: %v", err)
	}

	var updatedIndexObj ociIndex
	if err := json.Unmarshal(updatedIndex, &updatedIndexObj); err != nil {
		t.Fatalf("Failed to unmarshal updated index.json: %v", err)
	}

	// Verify index has one manifest
	if len(updatedIndexObj.Manifests) != 1 {
		t.Fatalf("Updated index.json has %d manifests, expected 1", len(updatedIndexObj.Manifests))
	}

	manifest := updatedIndexObj.Manifests[0]
	if manifest.Digest != testDigest {
		t.Errorf("Manifest digest = %s, want %s", manifest.Digest, testDigest)
	}
	if manifest.Size != testSize {
		t.Errorf("Manifest size = %d, want %d", manifest.Size, testSize)
	}
	if manifest.MediaType != "application/yaml" {
		t.Errorf("Manifest media type = %s, want application/yaml", manifest.MediaType)
	}
	if _, ok := manifest.Annotations["vnd.chainguard.dfc.mappings.downloadedAt"]; !ok {
		t.Errorf("Manifest missing downloadedAt annotation")
	}

	// Update again with same digest to test filtering
	if err := updateIndexJSON(testCacheDir, testDigest, testSize+10); err != nil {
		t.Fatalf("updateIndexJSON() error = %v", err)
	}

	finalIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read final index.json: %v", err)
	}

	var finalIndexObj ociIndex
	if err := json.Unmarshal(finalIndex, &finalIndexObj); err != nil {
		t.Fatalf("Failed to unmarshal final index.json: %v", err)
	}

	// Verify index still has one manifest but with updated size
	if len(finalIndexObj.Manifests) != 1 {
		t.Fatalf("Final index.json has %d manifests, expected 1", len(finalIndexObj.Manifests))
	}

	finalManifest := finalIndexObj.Manifests[0]
	if finalManifest.Size != testSize+10 {
		t.Errorf("Final manifest size = %d, want %d", finalManifest.Size, testSize+10)
	}
}

// TestUpdateWithServerError tests the Update function with a server error
func TestUpdateWithServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	opts := UpdateOptions{
		MappingsURL: server.URL,
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Errorf("Update() error = nil, want error")
	}
}

// TestUpdateWithCancelledContext tests the Update function with a cancelled context
func TestUpdateWithCancelledContext(t *testing.T) {
	setupTestEnvironment(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	opts := UpdateOptions{
		MappingsURL: "https://example.com/mappings.yaml",
	}

	err := Update(ctx, opts)
	if err == nil {
		t.Errorf("Update() error = nil, want error")
	}
}

// TestUpdateEmptyBody tests updating with an empty response body
func TestUpdateEmptyBody(t *testing.T) {
	setupTestEnvironment(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Write an empty body
	}))
	defer server.Close()

	opts := UpdateOptions{
		MappingsURL: server.URL,
	}

	err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	// The update should have calculated a hash for an empty body
	hash := sha512.Sum512([]byte{})
	hashString := hex.EncodeToString(hash[:])

	// Check if blob file was created
	cacheDir := getCacheDir()
	blobPath := filepath.Join(cacheDir, "blobs", "sha512", hashString)
	if _, err := os.Stat(blobPath); err != nil {
		t.Errorf("Blob file not created: %v", err)
	}

	// Check if symlink was created
	configDir := getConfigDir()
	symlinkPath := filepath.Join(configDir, orgName, "mappings.yaml")
	if _, err := os.Stat(symlinkPath); err != nil {
		t.Errorf("Symlink not created: %v", err)
	}
}

// TestUpdateWithBlockedWrite tests updating with a blocked file write
func TestUpdateWithBlockedWrite(t *testing.T) {
	// Skip on Windows as the OS permission model is different
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows")
	}

	setupTestEnvironment(t)

	server := setupTestServer(t)

	// Create a read-only cache directory
	cacheDir := getCacheDir()
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("Failed to create cache directory: %v", err)
	}

	// Make the cache directory read-only
	if err := os.Chmod(cacheDir, 0500); err != nil {
		t.Fatalf("Failed to make cache directory read-only: %v", err)
	}
	defer os.Chmod(cacheDir, 0755) // Restore permissions

	opts := UpdateOptions{
		MappingsURL: server.URL + "/mappings.yaml",
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Errorf("Update() error = nil, want error")
	}
}

// TestUpdateWithCustomUserAgent tests updating with a custom user agent
func TestUpdateWithCustomUserAgent(t *testing.T) {
	setupTestEnvironment(t)

	var receivedUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/yaml")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(testMappingsYAML))
		if err != nil {
			t.Fatalf("Failed to write response: %v", err)
		}
	}))
	defer server.Close()

	// Update with custom UserAgent
	customUserAgent := "dfc/test-version"
	opts := UpdateOptions{
		MappingsURL: server.URL,
		UserAgent:   customUserAgent,
	}

	err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify that the custom user agent was used
	if receivedUserAgent != customUserAgent {
		t.Errorf("User-Agent = %q, want %q", receivedUserAgent, customUserAgent)
	}
}

// TestGetMappingsConfigPath_CreateDirectory tests the directory creation in GetMappingsConfigPath
func TestGetMappingsConfigPath_CreateDirectory(t *testing.T) {
	_, _, configDir, _ := setupTestEnvironment(t)

	// Delete any existing directory
	orgDir := filepath.Join(configDir, orgName)
	if err := os.RemoveAll(orgDir); err != nil {
		t.Fatalf("Failed to clean org directory: %v", err)
	}

	// GetMappingsConfigPath should create the directory
	path, err := getMappingsConfigPath()
	if err != nil {
		t.Fatalf("GetMappingsConfigPath() error = %v", err)
	}

	// Check if directory was created
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("Directory not created: %v", err)
	}
}

// TestUpdate_RecreateSymlink tests that the symlink is recreated
func TestUpdate_RecreateSymlink(t *testing.T) {
	// Create test environment
	_, cacheDir, configDir, _ := setupTestEnvironment(t)

	// Setup server
	server := setupTestServer(t)

	// Calculate hash of test mappings
	hash := sha512.Sum512([]byte(testMappingsYAML))
	hashString := hex.EncodeToString(hash[:])
	digestString := "sha512:" + hashString

	// Create cache directory
	testCacheDir := filepath.Join(cacheDir, orgName, "mappings")
	if err := os.MkdirAll(testCacheDir, 0755); err != nil {
		t.Fatalf("Failed to create cache directory: %v", err)
	}

	// Initialize OCI layout
	if err := initOCILayout(testCacheDir); err != nil {
		t.Fatalf("Failed to initialize OCI layout: %v", err)
	}

	// Create the blobs/sha512 directory
	blobsDir := filepath.Join(testCacheDir, "blobs", "sha512")
	if err := os.MkdirAll(blobsDir, 0755); err != nil {
		t.Fatalf("Failed to create blobs directory: %v", err)
	}

	// Create the mapping blob
	blobPath := filepath.Join(blobsDir, hashString)
	if err := os.WriteFile(blobPath, []byte(testMappingsYAML), 0644); err != nil {
		t.Fatalf("Failed to write blob: %v", err)
	}

	// Update index.json
	if err := updateIndexJSON(testCacheDir, digestString, int64(len(testMappingsYAML))); err != nil {
		t.Fatalf("Failed to update index.json: %v", err)
	}

	// Create config directory
	nestedConfigDir := filepath.Join(configDir, orgName)
	if err := os.MkdirAll(nestedConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Create a symlink pointing to a wrong file
	wrongFile := filepath.Join(t.TempDir(), "wrong-file")
	if err := os.WriteFile(wrongFile, []byte("wrong content"), 0644); err != nil {
		t.Fatalf("Failed to create wrong file: %v", err)
	}

	symlinkPath := filepath.Join(nestedConfigDir, "mappings.yaml")
	if err := os.Symlink(wrongFile, symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Run update
	opts := UpdateOptions{
		MappingsURL: server.URL + "/mappings.yaml",
	}

	err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify symlink now points to the correct file
	linkTarget, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}

	if linkTarget != blobPath {
		t.Errorf("Symlink points to %q, want %q", linkTarget, blobPath)
	}
}

// TestUpdate_CreateCacheDirectories tests that the cache directories are created
func TestUpdate_CreateCacheDirectories(t *testing.T) {
	// Create test environment
	_, cacheDir, _, _ := setupTestEnvironment(t)

	// Setup server
	server := setupTestServer(t)

	// Delete any existing cache directory
	testCacheDir := filepath.Join(cacheDir, orgName, "mappings")
	if err := os.RemoveAll(testCacheDir); err != nil {
		t.Fatalf("Failed to clean cache directory: %v", err)
	}

	// Run update
	opts := UpdateOptions{
		MappingsURL: server.URL + "/mappings.yaml",
	}

	err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify cache directory was created
	if _, err := os.Stat(testCacheDir); err != nil {
		t.Errorf("Cache directory not created: %v", err)
	}

	// Verify blobs directory was created
	blobsDir := filepath.Join(testCacheDir, "blobs", "sha512")
	if _, err := os.Stat(blobsDir); err != nil {
		t.Errorf("Blobs directory not created: %v", err)
	}

	// Verify oci-layout was created
	ociLayoutPath := filepath.Join(testCacheDir, "oci-layout")
	if _, err := os.Stat(ociLayoutPath); err != nil {
		t.Errorf("oci-layout not created: %v", err)
	}

	// Verify index.json was created
	indexPath := filepath.Join(testCacheDir, "index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index.json not created: %v", err)
	}
}

// Mock functions for error cases
type mockFunc struct {
	origFn func() (string, error)
}

// mockTransport is a mock http.RoundTripper
type mockTransport struct {
	roundTripFn func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFn(req)
}

// TestUpdate_NewRequestError tests handling of NewRequest errors
func TestUpdate_NewRequestError(t *testing.T) {
	setupTestEnvironment(t)

	// Use an invalid URL to trigger a NewRequest error
	opts := UpdateOptions{
		MappingsURL: "://invalid-url",
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Errorf("Update() error = nil, want error")
	}
}

// TestUpdate_ResponseBodyReadError tests handling of response body read errors
func TestUpdate_ResponseBodyReadError(t *testing.T) {
	setupTestEnvironment(t)

	// Set up a client with a mock transport
	origClient := http.DefaultClient
	defer func() { http.DefaultClient = origClient }()

	http.DefaultClient = &http.Client{
		Transport: &mockTransport{
			roundTripFn: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errorReadCloser{},
				}, nil
			},
		},
	}

	opts := UpdateOptions{
		MappingsURL: "https://example.com/mappings.yaml",
	}

	err := Update(context.Background(), opts)
	if err == nil {
		t.Errorf("Update() error = nil, want error")
	}
}

// errorReadCloser is a ReadCloser that always returns an error
type errorReadCloser struct{}

func (e errorReadCloser) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("mock read error")
}

func (e errorReadCloser) Close() error {
	return nil
}
