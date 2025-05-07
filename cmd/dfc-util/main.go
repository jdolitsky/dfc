/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/clog/slag"
	"github.com/chainguard-dev/dfc/pkg/dfc"
	"github.com/spf13/cobra"
)

var (
	// Version is the semantic version (added at compile time via -X main.Version=$VERSION)
	Version string

	// Revision is the git commit id (added at compile time via -X main.Revision=$REVISION)
	Revision string
)

func main() {
	ctx := context.Background()
	if err := mainE(ctx); err != nil {
		clog.FromContext(ctx).Fatal(err.Error())
	}
}

func mainE(ctx context.Context) error {
	ctx, done := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer done()
	return cli().ExecuteContext(ctx)
}

func cli() *cobra.Command {
	// Default log level is info
	var level = slag.Level(slog.LevelInfo)

	v := "dev"
	if Version != "" {
		v = Version
		if Revision != "" {
			v += fmt.Sprintf(" (%s)", Revision)
		}
	}

	rootCmd := &cobra.Command{
		Use:     "dfc-util",
		Short:   "Utility for managing DFC database files",
		Version: v,
	}

	// Setup global flags
	rootCmd.PersistentFlags().Var(&level, "log-level", "log level (e.g. debug, info, warn, error)")

	// Add the gen-db command
	genDBCmd := &cobra.Command{
		Use:     "gen-db",
		Short:   "Generate SQLite database from YAML file",
		Args:    cobra.NoArgs,
		PreRunE: setupLogging(&level),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := clog.FromContext(ctx)

			// Get the input and output flags
			input, _ := cmd.Flags().GetString("input")
			output, _ := cmd.Flags().GetString("output")

			// If input is not specified, use the default builtin-mappings.yaml
			if input == "" {
				input = "pkg/dfc/builtin-mappings.yaml"
				log.Info("Using default input file", "path", input)
			}

			// If output is not specified, use the default builtin-mappings.db
			if output == "" {
				output = "pkg/dfc/builtin-mappings.db"
				log.Info("Using default output file", "path", output)
			}

			// Check if the input file exists
			if _, err := os.Stat(input); os.IsNotExist(err) {
				return fmt.Errorf("input file does not exist: %s", input)
			}

			// Create the output directory if it does not exist
			outputDir := filepath.Dir(output)
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				return fmt.Errorf("creating output directory: %w", err)
			}

			// Generate the database
			if err := dfc.CreateDBFromYAML(ctx, input, output); err != nil {
				return fmt.Errorf("generating database: %w", err)
			}

			log.Info("Database generated successfully", "output", output)
			return nil
		},
	}

	// Setup flags for gen-db command
	genDBCmd.Flags().StringP("input", "i", "", "Input YAML file path (default: pkg/dfc/builtin-mappings.yaml)")
	genDBCmd.Flags().StringP("output", "o", "", "Output SQLite database path (default: pkg/dfc/builtin-mappings.db)")

	// Add the verify-db command
	verifyDBCmd := &cobra.Command{
		Use:     "verify-db",
		Short:   "Verify SQLite database matches the expected state",
		Args:    cobra.NoArgs,
		PreRunE: setupLogging(&level),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := clog.FromContext(ctx)

			// Get the flags
			reference, _ := cmd.Flags().GetString("reference")
			check, _ := cmd.Flags().GetString("check")

			// If reference is not specified, use the default builtin-mappings.db
			if reference == "" {
				reference = "pkg/dfc/builtin-mappings.db"
				log.Info("Using default reference file", "path", reference)
			}

			// If check is not specified, generate a temp file from the current YAML
			var tempFile string
			var cleanupFunc func()
			if check == "" {
				// Create a temporary file
				tempFile = filepath.Join(os.TempDir(), "dfc-verify-db.db")
				log.Info("Using temporary file for check", "path", tempFile)
				cleanupFunc = func() {
					os.Remove(tempFile)
				}
				defer cleanupFunc()

				// Generate the database from the current YAML
				if err := dfc.CreateDBFromYAML(ctx, "pkg/dfc/builtin-mappings.yaml", tempFile); err != nil {
					return fmt.Errorf("generating temporary database: %w", err)
				}
				check = tempFile
			}

			// Compare the databases
			match, err := dfc.CompareDBFiles(ctx, reference, check)
			if err != nil {
				return fmt.Errorf("comparing database files: %w", err)
			}

			if match {
				log.Info("Database files match")
				return nil
			} else {
				log.Error("Database files do not match")
				fmt.Println("\nThe SQLite database is out-of-date.")
				fmt.Println("To fix this, run: go run cmd/dfc-util/main.go gen-db")
				fmt.Println("Then check in the updated builtin-mappings.db file.")
				return fmt.Errorf("database files do not match")
			}
		},
	}

	// Setup flags for verify-db command
	verifyDBCmd.Flags().String("reference", "", "Reference database file (default: pkg/dfc/builtin-mappings.db)")
	verifyDBCmd.Flags().String("check", "", "Database file to check (default: generate from current YAML)")

	// Add commands to root
	rootCmd.AddCommand(genDBCmd)
	rootCmd.AddCommand(verifyDBCmd)

	return rootCmd
}

// setupLogging configures the logging level
func setupLogging(level *slag.Level) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		log := clog.New(slog.Default().Handler())
		cmd.SetContext(clog.WithLogger(cmd.Context(), log))
		return nil
	}
}
