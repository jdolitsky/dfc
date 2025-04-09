/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/chainguard-dev/dfc/pkg/dfc"
	"github.com/chainguard-dev/dfc/pkg/update"
)

//go:embed mappings.yaml
var mappingsYamlBytes []byte

var (
	// Version is the semantic version (added at compile time via -X main.Version=$VERSION)
	Version string

	// Revision is the git commit id (added at compile time via -X main.Revision=$REVISION)
	Revision string
)

func main() {
	// inspired by https://github.com/jonjohnsonjr/apkrane/blob/main/main.go
	if err := cli().ExecuteContext(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func cli() *cobra.Command {
	var j bool
	var inPlace bool
	var org string
	var registry string
	var mappingsFile string
	var updateFlag bool

	v := "dev"
	if Version != "" {
		v = Version
		if Revision != "" {
			v += fmt.Sprintf(" (%s)", Revision)
		}
	}

	cmd := &cobra.Command{
		Use:     "dfc",
		Example: "dfc <path_to_dockerfile>",
		Args:    cobra.MaximumNArgs(1),
		Version: v,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Handle update flag
			if updateFlag {
				// Set up update options
				updateOpts := update.Options{}

				// Set UserAgent if version info is available
				if Version != "" {
					updateOpts.UserAgent = "dfc/" + Version
				}

				// If custom mappings file provided, don't use the update URL
				if mappingsFile != "" {
					fmt.Printf("Using local mappings file %s instead of remote URL\n", mappingsFile)
				} else {
					// Otherwise use the default mappings URL
					updateOpts.MappingsURL = update.DefaultMappingsURL
				}

				if err := update.Update(ctx, updateOpts); err != nil {
					return fmt.Errorf("failed to update: %w", err)
				}

				// If no args provided, exit after update
				if len(args) == 0 {
					return nil
				}
				// Otherwise continue with normal flow
			} else if len(args) == 0 {
				// If no update flag and no args, require an argument
				return fmt.Errorf("requires at least 1 arg(s), only received 0")
			}

			// Allow for piping into the CLI if first arg is "-"
			input := cmd.InOrStdin()
			isFile := args[0] != "-"
			var path string
			if isFile {
				path = args[0]
				file, err := os.Open(filepath.Clean(path))
				if err != nil {
					return fmt.Errorf("failed open file: %s: %w", path, err)
				}
				defer file.Close()
				input = file
			}
			buf := new(bytes.Buffer)
			if _, err := buf.ReadFrom(input); err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}
			raw := buf.Bytes()

			// Use dfc2 to parse the Dockerfile
			dockerfile, err := dfc.ParseDockerfile(ctx, raw)
			if err != nil {
				return fmt.Errorf("unable to parse dockerfile: %w", err)
			}

			// Try to parse and merge additional mappings from packages.yaml or custom mappings file
			var mappings dfc.MappingsConfig
			var mappingsBytes []byte

			// Use custom mappings file if provided
			if mappingsFile != "" {
				var err error
				mappingsBytes, err = os.ReadFile(mappingsFile)
				if err != nil {
					return fmt.Errorf("reading mappings file %s: %w", mappingsFile, err)
				}
				log.Printf("using custom mappings file: %s", mappingsFile)
			} else {
				// Try to use XDG config mappings file if available
				xdgMappings, err := update.GetMappingsConfig()
				if err != nil {
					return fmt.Errorf("checking XDG config mappings: %w", err)
				}

				if xdgMappings != nil {
					log.Printf("using mappings from XDG config directory")
					mappingsBytes = xdgMappings
				} else {
					// Fall back to embedded mappings.yaml
					log.Printf("using embedded mappings.yaml")
					mappingsBytes = mappingsYamlBytes
				}
			}

			if err := yaml.Unmarshal(mappingsBytes, &mappings); err != nil {
				return fmt.Errorf("unmarshalling package mappings: %w", err)
			}

			// Setup conversion options
			opts := dfc.Options{
				Organization: org,
				Registry:     registry,
				Mappings:     mappings,
			}

			// Convert the Dockerfile
			convertedDockerfile, err := dockerfile.Convert(ctx, opts)
			if err != nil {
				return fmt.Errorf("converting dockerfile: %w", err)
			}

			// Output the Dockerfile as JSON
			if j {
				if inPlace {
					return fmt.Errorf("unable to use --in-place and --json flag at same time")
				}

				// Output the Dockerfile as JSON
				b, err := json.Marshal(convertedDockerfile)
				if err != nil {
					return fmt.Errorf("marshalling dockerfile to json: %w", err)
				}
				fmt.Println(string(b))
				return nil
			}

			// Get the string representation
			result := convertedDockerfile.String()

			// modify file in place
			if inPlace {
				if !isFile {
					return fmt.Errorf("unable to use --in-place flag when processing stdin")
				}

				// Get original file info to preserve permissions
				fileInfo, err := os.Stat(path)
				if err != nil {
					return fmt.Errorf("getting file info for %s: %w", path, err)
				}
				originalMode := fileInfo.Mode().Perm()

				backupPath := path + ".bak"
				log.Printf("saving dockerfile backup to %s", backupPath)
				if err := os.WriteFile(backupPath, raw, originalMode); err != nil {
					return fmt.Errorf("saving dockerfile backup to %s: %w", backupPath, err)
				}
				log.Printf("overwriting %s", path)
				if err := os.WriteFile(path, []byte(result), originalMode); err != nil {
					return fmt.Errorf("overwriting %s: %w", path, err)
				}
				return nil
			}

			// Print to stdout
			fmt.Print(result)

			return nil
		},
	}

	cmd.Flags().StringVar(&org, "org", dfc.DefaultOrg, "the organization for cgr.dev/<org>/<image> (defaults to ORG)")
	cmd.Flags().StringVar(&registry, "registry", "", "an alternate registry and root namepace (e.g. r.example.com/cg-mirror)")
	cmd.Flags().BoolVarP(&inPlace, "in-place", "i", false, "modified the Dockerfile in place (vs. stdout), saving original in a .bak file")
	cmd.Flags().BoolVarP(&j, "json", "j", false, "print dockerfile as json (before conversion)")
	cmd.Flags().StringVarP(&mappingsFile, "mappings", "m", "", "path to a custom package mappings YAML file (instead of the default)")
	cmd.Flags().BoolVar(&updateFlag, "update", false, "check for and apply available updates")

	return cmd
}
