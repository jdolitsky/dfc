/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
	"gopkg.in/yaml.v3"
)

//go:embed builtin-mappings.yaml
var builtinMappingsYAMLBytes []byte

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

	// First try to use SQLite database if available
	dbPath, err := getDBPath()
	if err == nil {
		// Check if the database file exists
		if fileExists(dbPath) {
			log.Debug("Using SQLite database from config directory")
			db, err := OpenDB(ctx)
			if err == nil {
				defer db.Close()
				mappings, err := LoadMappingsFromDB(ctx, db)
				if err == nil {
					return mappings, nil
				}
				log.Warn("Failed to load mappings from database, will try YAML", "error", err)
			} else {
				log.Warn("Failed to open database, will try YAML", "error", err)
			}
		}
	}

	// Fall back to YAML if database is not available or has errors
	// Try to use XDG config mappings file if available
	xdgMappings, err := getMappingsConfig()
	if err != nil {
		return mappings, fmt.Errorf("checking XDG config mappings: %w", err)
	}

	var mappingsBytes []byte
	if xdgMappings != nil {
		log.Debug("Using YAML mappings from XDG config directory")
		mappingsBytes = xdgMappings
	} else {
		// Fall back to embedded mappings
		log.Debug("Using embedded builtin mappings (YAML)")
		mappingsBytes = builtinMappingsYAMLBytes
	}

	// Unmarshal the mappings
	if err := yaml.Unmarshal(mappingsBytes, &mappings); err != nil {
		return mappings, fmt.Errorf("unmarshalling mappings: %w", err)
	}

	return mappings, nil
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
