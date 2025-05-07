/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/clog"
)

func TestCreateDBFromYAML(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "dfc-db-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a simple YAML file
	yamlPath := filepath.Join(tempDir, "test-mappings.yaml")
	yamlContent := `
images:
  alpine: chainguard-base:latest
  debian: chainguard-base:latest
packages:
  debian:
    git:
      - git
    curl:
      - curl
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	// Create a database from the YAML
	dbPath := filepath.Join(tempDir, "test-mappings.db")
	ctx := clog.WithLogger(context.Background(), clog.New(slog.Default().Handler()))

	if err := CreateDBFromYAML(ctx, yamlPath, dbPath); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Check that the database was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("Database file was not created")
	}

	// Open the database
	db, err := OpenDB(ctx, dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Load mappings from the database
	mappings, err := LoadMappingsFromDB(ctx, db)
	if err != nil {
		t.Fatalf("Failed to load mappings from database: %v", err)
	}

	// Check the contents of the mappings
	if len(mappings.Images) != 2 {
		t.Errorf("Expected 2 images, got %d", len(mappings.Images))
	}
	if mappings.Images["alpine"] != "chainguard-base:latest" {
		t.Errorf("Expected alpine to map to chainguard-base:latest, got %s", mappings.Images["alpine"])
	}
	if mappings.Images["debian"] != "chainguard-base:latest" {
		t.Errorf("Expected debian to map to chainguard-base:latest, got %s", mappings.Images["debian"])
	}

	// Check the packages
	if len(mappings.Packages["debian"]) != 2 {
		t.Errorf("Expected 2 packages for debian, got %d", len(mappings.Packages["debian"]))
	}
	if len(mappings.Packages["debian"]["git"]) != 1 || mappings.Packages["debian"]["git"][0] != "git" {
		t.Errorf("Expected git to map to [git], got %v", mappings.Packages["debian"]["git"])
	}
	if len(mappings.Packages["debian"]["curl"]) != 1 || mappings.Packages["debian"]["curl"][0] != "curl" {
		t.Errorf("Expected curl to map to [curl], got %v", mappings.Packages["debian"]["curl"])
	}
}

func TestGetDBMetadata(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "dfc-db-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a simple YAML file
	yamlPath := filepath.Join(tempDir, "test-mappings.yaml")
	yamlContent := `
images:
  alpine: chainguard-base:latest
packages: {}
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	// Create a database from the YAML
	dbPath := filepath.Join(tempDir, "test-mappings.db")
	ctx := clog.WithLogger(context.Background(), clog.New(slog.Default().Handler()))

	if err := CreateDBFromYAML(ctx, yamlPath, dbPath); err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}

	// Open the database
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	dbConn := &DBConnection{db}

	// Get metadata
	metadata, err := GetDBMetadata(ctx, dbConn)
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}

	// Check the metadata
	if metadata["schema_version"] != "1" {
		t.Errorf("Expected schema_version to be 1, got %s", metadata["schema_version"])
	}
	if metadata["created_at"] == "" {
		t.Errorf("Expected created_at to be set")
	}
}

func TestCompareDBFiles(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "dfc-db-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create two identical YAML files
	yamlPath1 := filepath.Join(tempDir, "test-mappings1.yaml")
	yamlPath2 := filepath.Join(tempDir, "test-mappings2.yaml")
	yamlContent := `
images:
  alpine: chainguard-base:latest
packages: {}
`
	if err := os.WriteFile(yamlPath1, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write YAML file 1: %v", err)
	}
	if err := os.WriteFile(yamlPath2, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("Failed to write YAML file 2: %v", err)
	}

	// Create databases from the YAMLs
	dbPath1 := filepath.Join(tempDir, "test-mappings1.db")
	dbPath2 := filepath.Join(tempDir, "test-mappings2.db")
	ctx := clog.WithLogger(context.Background(), clog.New(slog.Default().Handler()))

	if err := CreateDBFromYAML(ctx, yamlPath1, dbPath1); err != nil {
		t.Fatalf("Failed to create database 1: %v", err)
	}
	if err := CreateDBFromYAML(ctx, yamlPath2, dbPath2); err != nil {
		t.Fatalf("Failed to create database 2: %v", err)
	}

	// Compare the two identical databases
	match, err := CompareDBFiles(ctx, dbPath1, dbPath2)
	if err != nil {
		t.Fatalf("Failed to compare databases: %v", err)
	}

	// They should match because they were created from identical YAML
	if !match {
		t.Errorf("Expected databases to match")
	}

	// Create a different YAML file
	yamlPath3 := filepath.Join(tempDir, "test-mappings3.yaml")
	yamlContent3 := `
images:
  alpine: chainguard-base:latest
  debian: chainguard-base:latest
packages: {}
`
	if err := os.WriteFile(yamlPath3, []byte(yamlContent3), 0600); err != nil {
		t.Fatalf("Failed to write YAML file 3: %v", err)
	}

	// Create a database from the different YAML
	dbPath3 := filepath.Join(tempDir, "test-mappings3.db")
	if err := CreateDBFromYAML(ctx, yamlPath3, dbPath3); err != nil {
		t.Fatalf("Failed to create database 3: %v", err)
	}

	// Compare with the different database
	match, err = CompareDBFiles(ctx, dbPath1, dbPath3)
	if err != nil {
		t.Fatalf("Failed to compare databases: %v", err)
	}

	// They should not match because they were created from different YAMLs
	if match {
		t.Errorf("Expected databases not to match")
	}
}
