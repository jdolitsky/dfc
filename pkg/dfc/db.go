/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/chainguard-dev/clog"
	_ "github.com/glebarez/sqlite" // SQLite driver
	"gopkg.in/yaml.v3"
)

//go:embed builtin-mappings.db
var builtinMappingsDBFS embed.FS

// Get the embedded database bytes
func getEmbeddedDBBytes() ([]byte, error) {
	return builtinMappingsDBFS.ReadFile("builtin-mappings.db")
}

const (
	// dbSchema is the current schema version
	dbSchema = 1

	// dbMetadataTableSchema is the SQL for creating the metadata table
	dbMetadataTableSchema = `
	CREATE TABLE IF NOT EXISTS metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`

	// dbImagesTableSchema is the SQL for creating the images table
	dbImagesTableSchema = `
	CREATE TABLE IF NOT EXISTS images (
		source TEXT PRIMARY KEY,
		target TEXT NOT NULL
	);`

	// dbPackagesTableSchema is the SQL for creating the packages table
	dbPackagesTableSchema = `
	CREATE TABLE IF NOT EXISTS packages (
		distro TEXT NOT NULL,
		source TEXT NOT NULL,
		target TEXT NOT NULL,
		PRIMARY KEY (distro, source, target)
	);`
)

// DBConnection represents a connection to the SQLite database
type DBConnection struct {
	*sql.DB
}

// getDBPath returns the path to the builtin-mappings.db file in XDG_CONFIG_HOME
func getDBPath() (string, error) {
	dbPath, err := xdg.ConfigFile(filepath.Join(orgName, "builtin-mappings.db"))
	if err != nil {
		return "", fmt.Errorf("getting db path: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}

	return dbPath, nil
}

// getDBBytes reads and returns the contents of the builtin-mappings.db file
// from the XDG config directory if it exists, otherwise returns nil
func getDBBytes() ([]byte, error) {
	dbPath, err := getDBPath()
	if err != nil {
		return nil, err
	}

	// Check if the file exists
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return nil with no error
			return nil, nil
		}
		return nil, fmt.Errorf("checking db file: %w", err)
	}

	// Read the db file
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return nil, fmt.Errorf("reading db file: %w", err)
	}

	return data, nil
}

// OpenDB opens a connection to the SQLite database
// If the database doesn't exist, it falls back to the embedded database
// If a custom path is provided, it will open that database instead
func OpenDB(ctx context.Context, customPath ...string) (*DBConnection, error) {
	log := clog.FromContext(ctx)

	var dbPath string
	if len(customPath) > 0 && customPath[0] != "" {
		// Use the provided custom path
		dbPath = customPath[0]
		log.Debug("Using custom database path", "path", dbPath)
	} else {
		// Try to get the database from the XDG config directory
		var err error
		dbPath, err = getDBPath()
		if err != nil {
			return nil, fmt.Errorf("getting db path: %w", err)
		}

		// Check if the file exists
		if _, err := os.Stat(dbPath); err != nil {
			if os.IsNotExist(err) {
				// If it doesn't exist, create a new db file using the embedded database
				log.Debug("Creating new database from embedded db")

				// Get the embedded database bytes
				dbBytes, err := getEmbeddedDBBytes()
				if err != nil {
					return nil, fmt.Errorf("reading embedded db: %w", err)
				}

				if err := os.WriteFile(dbPath, dbBytes, 0600); err != nil {
					return nil, fmt.Errorf("writing embedded db: %w", err)
				}
			} else {
				return nil, fmt.Errorf("checking db file: %w", err)
			}
		} else {
			log.Debug("Using existing database", "path", dbPath)
		}
	}

	// Open the database
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}

	return &DBConnection{db}, nil
}

// CreateDB creates a new SQLite database from a MappingsConfig
func CreateDB(ctx context.Context, mappings MappingsConfig, outputPath string) error {
	log := clog.FromContext(ctx)
	log.Info("Creating SQLite database", "path", outputPath)

	// Create or open the database
	db, err := sql.Open("sqlite", outputPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	// Create the tables
	if _, err := db.Exec(dbMetadataTableSchema); err != nil {
		return fmt.Errorf("creating metadata table: %w", err)
	}

	if _, err := db.Exec(dbImagesTableSchema); err != nil {
		return fmt.Errorf("creating images table: %w", err)
	}

	if _, err := db.Exec(dbPackagesTableSchema); err != nil {
		return fmt.Errorf("creating packages table: %w", err)
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert metadata
	metaStmt, err := tx.Prepare("INSERT OR REPLACE INTO metadata (key, value) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("preparing metadata statement: %w", err)
	}
	defer metaStmt.Close()

	// Insert schema version
	if _, err := metaStmt.Exec("schema_version", fmt.Sprintf("%d", dbSchema)); err != nil {
		return fmt.Errorf("inserting schema version: %w", err)
	}

	// Insert creation time
	if _, err := metaStmt.Exec("created_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("inserting creation time: %w", err)
	}

	// Insert images
	imgStmt, err := tx.Prepare("INSERT OR REPLACE INTO images (source, target) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("preparing images statement: %w", err)
	}
	defer imgStmt.Close()

	for source, target := range mappings.Images {
		if _, err := imgStmt.Exec(source, target); err != nil {
			return fmt.Errorf("inserting image mapping %s → %s: %w", source, target, err)
		}
	}

	// Insert packages
	pkgStmt, err := tx.Prepare("INSERT OR REPLACE INTO packages (distro, source, target) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("preparing packages statement: %w", err)
	}
	defer pkgStmt.Close()

	for distro, packages := range mappings.Packages {
		for source, targets := range packages {
			for _, target := range targets {
				if _, err := pkgStmt.Exec(string(distro), source, target); err != nil {
					return fmt.Errorf("inserting package mapping %s:%s → %s: %w", distro, source, target, err)
				}
			}
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	log.Info("Database created successfully")
	return nil
}

// CreateDBFromYAML creates a new SQLite database from a YAML file
func CreateDBFromYAML(ctx context.Context, yamlPath, outputPath string) error {
	log := clog.FromContext(ctx)
	log.Info("Creating database from YAML file", "yaml", yamlPath)

	// Read the YAML file
	yamlData, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("reading YAML file: %w", err)
	}

	// Parse the YAML
	var mappings MappingsConfig
	if err := yaml.Unmarshal(yamlData, &mappings); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	// Create the database
	return CreateDB(ctx, mappings, outputPath)
}

// GetImageMapping gets a single image mapping from the database
func GetImageMapping(ctx context.Context, db *DBConnection, sourceImage string) (string, bool, error) {
	log := clog.FromContext(ctx)
	log.Debug("Looking up image mapping in database", "source", sourceImage)

	// Try to get the exact match first
	var targetImage string
	err := db.QueryRow("SELECT target FROM images WHERE source = ?", sourceImage).Scan(&targetImage)
	if err != nil {
		if err == sql.ErrNoRows {
			// No exact match found, try pattern matching for images with wildcards
			rows, err := db.Query("SELECT source, target FROM images WHERE source LIKE '%*%'")
			if err != nil {
				return "", false, fmt.Errorf("querying images with wildcards: %w", err)
			}
			defer rows.Close()

			for rows.Next() {
				var wildcardSource, wildcardTarget string
				if err := rows.Scan(&wildcardSource, &wildcardTarget); err != nil {
					return "", false, fmt.Errorf("scanning wildcard row: %w", err)
				}

				// Convert wildcard pattern to regex
				pattern := strings.ReplaceAll(wildcardSource, "*", ".*")
				matched, err := regexp.MatchString("^"+pattern+"$", sourceImage)
				if err != nil {
					log.Debug("Error matching pattern", "pattern", pattern, "error", err)
					continue
				}

				if matched {
					log.Debug("Found wildcard match", "pattern", wildcardSource, "source", sourceImage, "target", wildcardTarget)
					return wildcardTarget, true, nil
				}
			}

			if err := rows.Err(); err != nil {
				return "", false, fmt.Errorf("iterating wildcard rows: %w", err)
			}

			// No matches found
			return "", false, nil
		}
		return "", false, fmt.Errorf("querying image mapping: %w", err)
	}

	log.Debug("Found exact image mapping", "source", sourceImage, "target", targetImage)
	return targetImage, true, nil
}

// GetPackageMappings gets package mappings for a specific distro and source package
func GetPackageMappings(ctx context.Context, db *DBConnection, distro Distro, sourcePackage string) ([]string, bool, error) {
	log := clog.FromContext(ctx)
	log.Debug("Looking up package mapping in database", "distro", distro, "source", sourcePackage)

	// Query for package mappings
	rows, err := db.Query("SELECT target FROM packages WHERE distro = ? AND source = ?", string(distro), sourcePackage)
	if err != nil {
		return nil, false, fmt.Errorf("querying package mappings: %w", err)
	}
	defer rows.Close()

	var targetPackages []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, false, fmt.Errorf("scanning package row: %w", err)
		}
		targetPackages = append(targetPackages, target)
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterating package rows: %w", err)
	}

	if len(targetPackages) == 0 {
		// No mappings found
		return nil, false, nil
	}

	log.Debug("Found package mappings", "distro", distro, "source", sourcePackage, "targets", targetPackages)
	return targetPackages, true, nil
}

// LoadMappingsFromDB loads mappings from the SQLite database
// Note: This function loads the entire database into memory and should be used
// only when necessary. For individual lookups, prefer GetImageMapping and GetPackageMappings.
func LoadMappingsFromDB(ctx context.Context, db *DBConnection) (MappingsConfig, error) {
	log := clog.FromContext(ctx)
	var mappings MappingsConfig

	// Initialize empty maps
	mappings.Images = make(map[string]string)
	mappings.Packages = make(PackageMap)

	// Load images
	log.Debug("Loading image mappings from database")
	rows, err := db.Query("SELECT source, target FROM images")
	if err != nil {
		return mappings, fmt.Errorf("querying images: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return mappings, fmt.Errorf("scanning image row: %w", err)
		}
		mappings.Images[source] = target
	}

	if err := rows.Err(); err != nil {
		return mappings, fmt.Errorf("iterating image rows: %w", err)
	}

	// Load packages
	log.Debug("Loading package mappings from database")
	rows, err = db.Query("SELECT distro, source, target FROM packages")
	if err != nil {
		return mappings, fmt.Errorf("querying packages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var distroStr, source, target string
		if err := rows.Scan(&distroStr, &source, &target); err != nil {
			return mappings, fmt.Errorf("scanning package row: %w", err)
		}

		distro := Distro(distroStr)
		if mappings.Packages[distro] == nil {
			mappings.Packages[distro] = make(map[string][]string)
		}

		mappings.Packages[distro][source] = append(mappings.Packages[distro][source], target)
	}

	if err := rows.Err(); err != nil {
		return mappings, fmt.Errorf("iterating package rows: %w", err)
	}

	// Get the schema version and creation time for debugging
	var schemaVersion, createdAt string
	if err := db.QueryRow("SELECT value FROM metadata WHERE key = 'schema_version'").Scan(&schemaVersion); err != nil {
		log.Warn("Unable to get schema version", "error", err)
	} else {
		log.Debug("Database schema version", "version", schemaVersion)
	}

	if err := db.QueryRow("SELECT value FROM metadata WHERE key = 'created_at'").Scan(&createdAt); err != nil {
		log.Warn("Unable to get creation time", "error", err)
	} else {
		log.Debug("Database created at", "time", createdAt)
	}

	return mappings, nil
}

// GetDBMetadata returns metadata from the database
func GetDBMetadata(ctx context.Context, db *DBConnection) (map[string]string, error) {
	metadata := make(map[string]string)

	rows, err := db.Query("SELECT key, value FROM metadata")
	if err != nil {
		return metadata, fmt.Errorf("querying metadata: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return metadata, fmt.Errorf("scanning metadata row: %w", err)
		}
		metadata[key] = value
	}

	if err := rows.Err(); err != nil {
		return metadata, fmt.Errorf("iterating metadata rows: %w", err)
	}

	return metadata, nil
}

// CompareDBFiles compares two database files and returns true if they match
func CompareDBFiles(ctx context.Context, path1, path2 string) (bool, error) {
	log := clog.FromContext(ctx)
	log.Debug("Comparing database files", "path1", path1, "path2", path2)

	// Open the first database
	db1, err := sql.Open("sqlite", path1+"?_pragma=foreign_keys(1)")
	if err != nil {
		return false, fmt.Errorf("opening first db: %w", err)
	}
	defer db1.Close()

	// Open the second database
	db2, err := sql.Open("sqlite", path2+"?_pragma=foreign_keys(1)")
	if err != nil {
		return false, fmt.Errorf("opening second db: %w", err)
	}
	defer db2.Close()

	// Wrap the connections
	dbConn1 := &DBConnection{db1}
	dbConn2 := &DBConnection{db2}

	// Load mappings from both databases
	mappings1, err := LoadMappingsFromDB(ctx, dbConn1)
	if err != nil {
		return false, fmt.Errorf("loading mappings from first db: %w", err)
	}

	mappings2, err := LoadMappingsFromDB(ctx, dbConn2)
	if err != nil {
		return false, fmt.Errorf("loading mappings from second db: %w", err)
	}

	// Compare the images
	if len(mappings1.Images) != len(mappings2.Images) {
		log.Debug("Image counts differ", "db1", len(mappings1.Images), "db2", len(mappings2.Images))
		return false, nil
	}

	for source, target1 := range mappings1.Images {
		target2, ok := mappings2.Images[source]
		if !ok || target1 != target2 {
			log.Debug("Image mapping differs", "source", source, "db1Target", target1, "db2Target", target2)
			return false, nil
		}
	}

	// Compare the packages
	if len(mappings1.Packages) != len(mappings2.Packages) {
		log.Debug("Distro counts differ", "db1", len(mappings1.Packages), "db2", len(mappings2.Packages))
		return false, nil
	}

	for distro, pkgs1 := range mappings1.Packages {
		pkgs2, ok := mappings2.Packages[distro]
		if !ok {
			log.Debug("Missing distro in second db", "distro", distro)
			return false, nil
		}

		if len(pkgs1) != len(pkgs2) {
			log.Debug("Package counts differ for distro", "distro", distro, "db1", len(pkgs1), "db2", len(pkgs2))
			return false, nil
		}

		for source, targets1 := range pkgs1 {
			targets2, ok := pkgs2[source]
			if !ok {
				log.Debug("Missing package in second db", "distro", distro, "package", source)
				return false, nil
			}

			if len(targets1) != len(targets2) {
				log.Debug("Target counts differ for package", "distro", distro, "package", source, "db1", len(targets1), "db2", len(targets2))
				return false, nil
			}

			// Compare the target arrays
			for i, target1 := range targets1 {
				if i >= len(targets2) || target1 != targets2[i] {
					log.Debug("Target differs for package", "distro", distro, "package", source, "index", i, "db1Target", target1, "db2Target", "N/A")
					return false, nil
				}
			}
		}
	}

	log.Debug("Database content matches")
	return true, nil
}

// ExportDBToYAML exports the contents of a SQLite database to a YAML file
func ExportDBToYAML(ctx context.Context, dbPath, yamlPath string) error {
	log := clog.FromContext(ctx)
	log.Info("Exporting database to YAML file", "db", dbPath, "yaml", yamlPath)

	// Open the database
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("opening db: %w", err)
	}
	defer db.Close()

	// Wrap the connection
	dbConn := &DBConnection{db}

	// Load mappings from the database
	mappings, err := LoadMappingsFromDB(ctx, dbConn)
	if err != nil {
		return fmt.Errorf("loading mappings from db: %w", err)
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(mappings)
	if err != nil {
		return fmt.Errorf("marshalling to YAML: %w", err)
	}

	// Write to file
	if err := os.WriteFile(yamlPath, yamlData, 0600); err != nil {
		return fmt.Errorf("writing YAML file: %w", err)
	}

	log.Info("Database exported to YAML successfully")
	return nil
}
