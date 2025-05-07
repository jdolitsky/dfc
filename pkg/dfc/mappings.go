/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
)

// defaultGetDefaultMappings is the real implementation of GetDefaultMappings
func defaultGetDefaultMappings(ctx context.Context, update bool) (MappingsConfig, error) {
	log := clog.FromContext(ctx)
	var mappings MappingsConfig

	// If update is requested, try to update the mappings first
	if update {
		// Set up update options
		updateOpts := UpdateOptions{}
		// Use the default URL
		updateOpts.MappingsURL = defaultMappingsURL

		if err := Update(ctx, updateOpts); err != nil {
			log.Warn("Failed to update mappings, will try to use existing mappings", "error", err)
		}
	}

	// Try to use SQLite database
	dbPath, err := getDBPath()
	if err == nil && fileExists(dbPath) {
		log.Debug("Using SQLite database from config directory")
		db, err := OpenDB(ctx)
		if err == nil {
			defer db.Close()
			mappings, err := LoadMappingsFromDB(ctx, db)
			if err == nil {
				return mappings, nil
			}
			log.Warn("Failed to load mappings from database", "error", err)
		} else {
			log.Warn("Failed to open database", "error", err)
		}
	} else {
		// In test mode, try to load the embedded database directly
		log.Debug("Trying to load embedded database directly")
		dbBytes, err := getEmbeddedDBBytes()
		if err != nil {
			return mappings, fmt.Errorf("failed to load embedded database: %w", err)
		}

		// Create a temporary file for the database
		tmpFile, err := os.CreateTemp("", "dfc-embedded-db-*.db")
		if err != nil {
			return mappings, fmt.Errorf("failed to create temp file for database: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		// Write the embedded database to the temporary file
		if _, err := tmpFile.Write(dbBytes); err != nil {
			return mappings, fmt.Errorf("failed to write embedded database to temp file: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			return mappings, fmt.Errorf("failed to close temp file: %w", err)
		}

		// Open the database
		db, err := OpenDB(ctx, tmpFile.Name())
		if err != nil {
			return mappings, fmt.Errorf("failed to open embedded database: %w", err)
		}
		defer db.Close()

		// Load the mappings
		mappings, err = LoadMappingsFromDB(ctx, db)
		if err != nil {
			return mappings, fmt.Errorf("failed to load mappings from embedded database: %w", err)
		}

		return mappings, nil
	}

	// If we get here, we couldn't load from database, which is a critical error
	// since YAML is no longer embedded
	return mappings, fmt.Errorf("failed to load mappings from database, and YAML fallback is no longer supported")
}

// fileExists returns true if the file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MergeMappings merges the base and overlay mappings
// Any values in the overlay take precedence over the base
func MergeMappings(base, overlay MappingsConfig) MappingsConfig {
	result := MappingsConfig{
		Images:   make(map[string]string),
		Packages: make(PackageMap),
	}

	// Copy base images
	for k, v := range base.Images {
		result.Images[k] = v
	}

	// Overlay with extra images
	for k, v := range overlay.Images {
		result.Images[k] = v
	}

	// Copy base packages for each distro
	for distro, packages := range base.Packages {
		if result.Packages[distro] == nil {
			result.Packages[distro] = make(map[string][]string)
		}
		for pkg, mappings := range packages {
			result.Packages[distro][pkg] = mappings
		}
	}

	// Overlay with extra packages
	for distro, packages := range overlay.Packages {
		if result.Packages[distro] == nil {
			result.Packages[distro] = make(map[string][]string)
		}
		for pkg, mappings := range packages {
			result.Packages[distro][pkg] = mappings
		}
	}

	return result
}
