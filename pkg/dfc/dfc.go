/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/chainguard-dev/clog"
)

// Distro represents a Linux distribution
type Distro string

// Manager represents a package manager
type Manager string

// Supported distributions
const (
	DistroDebian Distro = "debian"
	DistroFedora Distro = "fedora"
	DistroAlpine Distro = "alpine"
)

// Supported package managers
const (
	ManagerAptGet   Manager = "apt-get"
	ManagerApk      Manager = "apk"
	ManagerYum      Manager = "yum"
	ManagerDnf      Manager = "dnf"
	ManagerMicrodnf Manager = "microdnf"
	ManagerApt      Manager = "apt"
)

// User management commands and packages
const (
	CommandUserAdd  = "useradd"
	CommandAddUser  = "adduser"
	CommandGroupAdd = "groupadd"
	CommandAddGroup = "addgroup"
	PackageShadow   = "shadow"
)

// Install subcommands
const (
	SubcommandInstall = "install"
	SubcommandAdd     = "add"
)

// Dockerfile directives
const (
	DirectiveFrom = "FROM"
	DirectiveRun  = "RUN"
	DirectiveUser = "USER"
	DirectiveArg  = "ARG"
	KeywordAs     = "AS"
)

// Default values
const (
	DefaultRegistryDomain = "cgr.dev"
	DefaultImageTag       = "latest-dev"
	DefaultUser           = "root"
	DefaultOrg            = "ORG"
	DefaultChainguardBase = "chainguard-base"
)

// Other
const (
	ApkNoCacheFlag = "--no-cache"
)

// PackageManagerInfo holds metadata about a package manager
type PackageManagerInfo struct {
	Distro         Distro
	InstallKeyword string
}

// PackageManagerInfoMap maps package managers to their metadata
var PackageManagerInfoMap = map[Manager]PackageManagerInfo{
	ManagerAptGet: {Distro: DistroDebian, InstallKeyword: SubcommandInstall},
	ManagerApt:    {Distro: DistroDebian, InstallKeyword: SubcommandInstall},

	ManagerYum:      {Distro: DistroFedora, InstallKeyword: SubcommandInstall},
	ManagerDnf:      {Distro: DistroFedora, InstallKeyword: SubcommandInstall},
	ManagerMicrodnf: {Distro: DistroFedora, InstallKeyword: SubcommandInstall},

	ManagerApk: {Distro: DistroAlpine, InstallKeyword: SubcommandAdd},
}

// DockerfileLine represents a single line in a Dockerfile
type DockerfileLine struct {
	Raw       string       `json:"raw"`
	Converted string       `json:"converted,omitempty"`
	Extra     string       `json:"extra,omitempty"` // Comments and whitespace that appear before this line
	Stage     int          `json:"stage,omitempty"`
	From      *FromDetails `json:"from,omitempty"`
	Run       *RunDetails  `json:"run,omitempty"`
	Arg       *ArgDetails  `json:"arg,omitempty"`
}

// ArgDetails holds details about an ARG directive
type ArgDetails struct {
	Name         string `json:"name,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
	UsedAsBase   bool   `json:"usedAsBase,omitempty"`
}

// FromDetails holds details about a FROM directive
type FromDetails struct {
	Base        string `json:"base,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Digest      string `json:"digest,omitempty"`
	Alias       string `json:"alias,omitempty"`
	Parent      int    `json:"parent,omitempty"`
	BaseDynamic bool   `json:"baseDynamic,omitempty"`
	TagDynamic  bool   `json:"tagDynamic,omitempty"`
	Orig        string `json:"orig,omitempty"` // Original full image reference
}

// RunDetails holds details about a RUN directive
type RunDetails struct {
	Distro   Distro           `json:"distro,omitempty"`
	Manager  Manager          `json:"manager,omitempty"`
	Packages []string         `json:"packages,omitempty"`
	Shell    *RunDetailsShell `json:"-"`
}

type RunDetailsShell struct {
	Before *ShellCommand
	After  *ShellCommand
}

// Dockerfile represents a parsed Dockerfile
type Dockerfile struct {
	Lines []*DockerfileLine `json:"lines"`
}

// String returns the Dockerfile content as a string
func (d *Dockerfile) String() string {
	var builder strings.Builder

	for i, line := range d.Lines {
		// Add the Extra content (comments, whitespace)
		if line.Extra != "" {
			builder.WriteString(line.Extra)
		}

		// If the line has been converted, use the converted content
		if line.Converted != "" {
			builder.WriteString(line.Converted)
			builder.WriteString("\n")
		} else if line.Raw != "" {
			// If this is a normal content line
			builder.WriteString(line.Raw)

			// If this is the last line, don't add a newline
			if i < len(d.Lines)-1 {
				builder.WriteString("\n")
			}
		}
	}

	return builder.String()
}

// ParseDockerfile parses a Dockerfile into a structured representation
func ParseDockerfile(_ context.Context, content []byte) (*Dockerfile, error) {
	// Create a new Dockerfile
	dockerfile := &Dockerfile{
		Lines: []*DockerfileLine{},
	}

	// Split into lines while preserving original structure
	lines := strings.Split(string(content), "\n")

	var extraContent strings.Builder
	var currentInstruction strings.Builder
	var inMultilineInstruction bool
	currentStage := 0
	stageAliases := make(map[string]int) // Maps stage aliases to their index

	processCurrentInstruction := func() {
		if currentInstruction.Len() == 0 {
			return
		}

		instruction := currentInstruction.String()
		trimmedInstruction := strings.TrimSpace(instruction)
		upperInstruction := strings.ToUpper(trimmedInstruction)

		// Create a new Dockerfile line
		dockerfileLine := &DockerfileLine{
			Raw:   instruction,
			Extra: extraContent.String(),
			Stage: currentStage,
		}

		// Handle FROM instructions (case-insensitive)
		if strings.HasPrefix(upperInstruction, DirectiveFrom+" ") {
			currentStage++
			dockerfileLine.Stage = currentStage

			// Extract the FROM details
			fromPartIdx := len(DirectiveFrom + " ")
			fromPart := strings.TrimSpace(trimmedInstruction[fromPartIdx:])

			// Check for AS clause which defines an alias (case-insensitive)
			var alias string
			// Capture space + AS + space to get exact length
			asKeywordWithSpaces := " " + KeywordAs + " "

			// Save the original image reference before any parsing
			var origImageRef string

			// Split by case-insensitive " AS " pattern
			asParts := strings.Split(strings.ToUpper(fromPart), asKeywordWithSpaces)
			if len(asParts) > 1 {
				// Find the position of the case-insensitive " AS " to preserve case in the base part
				asIndex := strings.Index(strings.ToUpper(fromPart), asKeywordWithSpaces)
				if asIndex != -1 {
					// Use the original case for the base and alias
					basePart := strings.TrimSpace(fromPart[:asIndex])
					aliasPart := strings.TrimSpace(fromPart[asIndex+len(asKeywordWithSpaces):])
					fromPart = basePart
					origImageRef = basePart // Capture only the image reference part
					alias = aliasPart

					// Store this alias for parent references
					stageAliases[strings.ToLower(alias)] = currentStage
				}
			} else {
				origImageRef = fromPart
			}

			// Parse the image reference
			var base, tag, digest string

			// Check for digest
			if digestParts := strings.Split(fromPart, "@"); len(digestParts) > 1 {
				fromPart = digestParts[0]
				digest = digestParts[1]
			}

			// Check for tag
			if tagParts := strings.Split(fromPart, ":"); len(tagParts) > 1 {
				base = tagParts[0]
				tag = tagParts[1]
			} else {
				base = fromPart
			}

			// Check for parent reference (case-insensitive)
			var parent int
			if parentStage, exists := stageAliases[strings.ToLower(base)]; exists {
				parent = parentStage
			}

			// Create the FromDetails
			dockerfileLine.From = &FromDetails{
				Base:        base,
				Tag:         tag,
				Digest:      digest,
				Alias:       alias,
				Parent:      parent,
				BaseDynamic: strings.Contains(base, "$"),
				TagDynamic:  strings.Contains(tag, "$"),
				Orig:        origImageRef,
			}
		}

		// Handle ARG instructions (case-insensitive)
		if strings.HasPrefix(upperInstruction, DirectiveArg+" ") {
			// Extract the ARG part (everything after "ARG ")
			argPartIdx := len(DirectiveArg + " ")
			argPart := strings.TrimSpace(trimmedInstruction[argPartIdx:])

			// Parse the ARG name and default value if present
			var name, defaultValue string
			if parts := strings.SplitN(argPart, "=", 2); len(parts) > 1 {
				name = strings.TrimSpace(parts[0])
				defaultValue = strings.TrimSpace(parts[1])
			} else {
				name = argPart
			}

			// Store the ARG details
			dockerfileLine.Arg = &ArgDetails{
				Name:         name,
				DefaultValue: defaultValue,
			}
		}

		// Handle RUN instructions (case-insensitive)
		if strings.HasPrefix(upperInstruction, DirectiveRun+" ") {
			// Extract the command part (everything after "RUN ")
			cmdPartIdx := len(DirectiveRun + " ")
			cmdPart := strings.TrimSpace(trimmedInstruction[cmdPartIdx:])

			// Parse the shell command
			shellCmd := ParseMultilineShell(cmdPart)

			// Store the shell command in Run.Shell.Before
			if shellCmd != nil {
				dockerfileLine.Run = &RunDetails{
					Shell: &RunDetailsShell{
						Before: shellCmd,
					},
				}
			}
		}

		// Add the line to the Dockerfile
		dockerfile.Lines = append(dockerfile.Lines, dockerfileLine)

		// Reset
		currentInstruction.Reset()
		extraContent.Reset()
	}

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Handle empty lines
		if trimmedLine == "" {
			if !inMultilineInstruction {
				extraContent.WriteString(line)
				extraContent.WriteString("\n")
			}
			continue
		}

		// Handle comments
		if strings.HasPrefix(trimmedLine, "#") {
			if !inMultilineInstruction {
				extraContent.WriteString(line)
				extraContent.WriteString("\n")
			}
			continue
		}

		// Check if this is the start of a new instruction or continuation
		if !inMultilineInstruction {
			// Check for continuation character
			if strings.HasSuffix(trimmedLine, "\\") {
				inMultilineInstruction = true
				currentInstruction.WriteString(line)
				currentInstruction.WriteString("\n")
			} else {
				// Single line instruction
				currentInstruction.WriteString(line)
				processCurrentInstruction()
			}
		} else {
			// Continuation of a multi-line instruction
			currentInstruction.WriteString(line)

			// Check if this is the end of the multi-line instruction
			if !strings.HasSuffix(trimmedLine, "\\") {
				inMultilineInstruction = false

				// We don't need to add a newline at the end of a completed multiline instruction
				// This prevents the extra newline that appears at the end of RUN commands
				// Only add newlines between individual lines, not at the end

				processCurrentInstruction()
			} else {
				// Not the end yet, add a newline
				currentInstruction.WriteString("\n")
			}
		}
	}

	// Process any remaining instruction
	if inMultilineInstruction {
		processCurrentInstruction()
	}

	// Capture any trailing whitespace or comments after the last directive
	if extraContent.Len() > 0 {
		// Remove trailing newline if present to avoid double newlines when generating output
		trailingContent := strings.TrimSuffix(extraContent.String(), "\n")
		dockerfile.Lines = append(dockerfile.Lines, &DockerfileLine{
			Raw: trailingContent,
		})
		extraContent.Reset()
	}

	return dockerfile, nil
}

// PackageMap maps distros to package mappings
type PackageMap map[Distro]map[string][]string

// FromLineConverter is a function type for custom image reference conversion in FROM directives.
// It takes a FromDetails struct containing information about the original image and the
// string that would be produced by the default Chainguard conversion, and allows for customizing
// the final image reference.
// The stageHasRun parameter indicates whether the current build stage has at least one RUN directive,
// which is useful for determining whether to add a "-dev" suffix to the image tag.
// If an error is returned, the original image reference will be used instead.
// The converter is only responsible for returning the image reference part (e.g., "cgr.dev/chainguard/node:latest"),
// not the full FROM line with directives like "AS" - those will be handled by the calling code.
//
// Example usage of a custom converter:
//
//	myConverter := func(from *FromDetails, converted string, stageHasRun bool) (string, error) {
//	    // For most images, just use the default Chainguard conversion
//	    if from.Base != "python" {
//	        return converted, nil
//	    }
//
//	    // Special handling for python images
//	    tag := from.Tag
//	    if stageHasRun && !strings.HasSuffix(tag, "-dev") {
//	        tag += "-dev"
//	    }
//	    return "myregistry.example.com/python:" + tag, nil
//	}
//
//	// Use the custom converter with DFC
//	dockerFile.Convert(ctx, dfc.Options{
//	    Organization: "myorg",
//	    FromLineConverter: myConverter,
//	})
type FromLineConverter func(from *FromDetails, converted string, stageHasRun bool) (string, error)

// Options configures the conversion
type Options struct {
	Organization      string
	Registry          string
	ExtraMappings     MappingsConfig
	Update            bool              // When true, update cached mappings before conversion
	NoBuiltIn         bool              // When true, don't use built-in mappings, only ExtraMappings
	FromLineConverter FromLineConverter // Optional custom converter for FROM lines
	MappingProvider   MappingProvider   // Provider for image/package mappings (overrides ExtraMappings if provided)
}

// MappingsConfig represents the structure of builtin-mappings.yaml
type MappingsConfig struct {
	Images   map[string]string `yaml:"images"`
	Packages PackageMap        `yaml:"packages"`
}

// parseImageReference extracts base and tag from an image reference
func parseImageReference(imageRef string) (base, tag string) {
	// Check for tag
	if tagParts := strings.Split(imageRef, ":"); len(tagParts) > 1 {
		base = tagParts[0]
		tag = tagParts[1]
	} else {
		base = imageRef
	}
	return base, tag
}

// Convert applies the conversion to the Dockerfile and returns a new converted Dockerfile
func (d *Dockerfile) Convert(ctx context.Context, opts Options) (*Dockerfile, error) {
	log := clog.FromContext(ctx)

	// Set up the MappingProvider
	var provider MappingProvider

	// If a provider is already set, use it directly
	if opts.MappingProvider != nil {
		provider = opts.MappingProvider
	} else {
		// Otherwise, create providers based on options
		var providers []MappingProvider

		// Handle mappings based on options
		if !opts.NoBuiltIn {
			// First try to get a DB connection
			var dbConnection *DBConnection

			// If update is requested, try to update the mappings first
			if opts.Update {
				updateOpts := UpdateOptions{}
				updateOpts.MappingsURL = defaultMappingsURL

				if err := Update(ctx, updateOpts); err != nil {
					log.Warn("Failed to update mappings, will try to use existing mappings", "error", err)
				}
			}

			// Try to open the database from XDG config directory
			dbPath, err := getDBPath()
			if err == nil && fileExists(dbPath) {
				log.Debug("Using SQLite database from config directory")
				db, err := OpenDB(ctx)
				if err == nil {
					// Keep the DB connection open for the duration of the conversion
					dbConnection = db
					// Add the DB provider first (higher priority)
					providers = append(providers, NewDBMappingProvider(db))
				} else {
					log.Warn("Failed to open database from config directory", "error", err)
				}
			}

			// If we couldn't open the DB from config, try the embedded DB
			if dbConnection == nil {
				log.Debug("Trying to load embedded database")
				dbBytes, err := getEmbeddedDBBytes()
				if err != nil {
					log.Warn("Failed to load embedded database", "error", err)
				} else {
					// Create a temporary file for the database
					tmpFile, err := os.CreateTemp("", "dfc-embedded-db-*.db")
					if err != nil {
						log.Warn("Failed to create temp file for database", "error", err)
					} else {
						tempPath := tmpFile.Name()
						// Make sure to clean up when we're done
						defer os.Remove(tempPath)

						// Write the embedded database to the temporary file
						if _, err := tmpFile.Write(dbBytes); err != nil {
							log.Warn("Failed to write embedded database to temp file", "error", err)
						} else if err := tmpFile.Close(); err != nil {
							log.Warn("Failed to close temp file", "error", err)
						} else {
							// Open the database
							db, err := OpenDB(ctx, tempPath)
							if err != nil {
								log.Warn("Failed to open embedded database", "error", err)
							} else {
								// Keep the connection open for the duration of the conversion
								dbConnection = db
								// Add the DB provider first (higher priority)
								providers = append(providers, NewDBMappingProvider(db))
							}
						}
					}
				}
			}
		}

		// If extra mappings are provided, add them as a provider
		if len(opts.ExtraMappings.Images) > 0 || len(opts.ExtraMappings.Packages) > 0 {
			// Add the in-memory provider (lower priority if we also have a DB provider)
			providers = append(providers, NewInMemoryMappingProvider(opts.ExtraMappings))
		}

		// Create the chained provider from all available providers
		if len(providers) > 0 {
			provider = NewChainedMappingProvider(providers...)
		} else if !opts.NoBuiltIn {
			// This is unexpected - we should have at least the embedded DB
			return nil, fmt.Errorf("failed to initialize any mapping providers")
		}
	}

	// Create a new Dockerfile for the converted content
	converted := &Dockerfile{
		Lines: make([]*DockerfileLine, len(d.Lines)),
	}

	// Track packages installed per stage
	stagePackages := make(map[int][]string)

	// Track ARGs that are used as base images
	argNameToDockerfileLine := make(map[string]*DockerfileLine)
	argsUsedAsBase := make(map[string]bool)

	// Track stages with RUN commands for determining if we need -dev suffix
	stagesWithRunCommands := detectStagesWithRunCommands(d.Lines)

	// First pass: collect all ARG definitions and identify which ones are used as base images
	identifyArgsUsedAsBaseImages(d.Lines, argNameToDockerfileLine, argsUsedAsBase)

	// Convert each line
	for i, line := range d.Lines {
		// Create a deep copy of the line
		newLine := &DockerfileLine{
			Raw:   line.Raw,
			Extra: line.Extra,
			Stage: line.Stage,
		}

		if line.From != nil {
			newLine.From = copyFromDetails(line.From)

			// Apply FROM line conversion only for non-dynamic bases
			if shouldConvertFromLine(line.From) {
				// Use the provider for conversion if available
				optsWithProvider := Options{
					Organization:      opts.Organization,
					Registry:          opts.Registry,
					MappingProvider:   provider,
					FromLineConverter: opts.FromLineConverter,
				}
				newLine.Converted = convertFromLine(ctx, line.From, line.Stage, stagesWithRunCommands, optsWithProvider)
			}
		}

		// Handle ARG lines that are used as base images
		if line.Arg != nil && line.Arg.UsedAsBase && line.Arg.DefaultValue != "" {
			// Use the provider for conversion
			optsWithProvider := Options{
				Organization:      opts.Organization,
				Registry:          opts.Registry,
				MappingProvider:   provider,
				FromLineConverter: opts.FromLineConverter,
			}
			argLine, argDetails := convertArgLine(ctx, line.Arg, d.Lines, stagesWithRunCommands, optsWithProvider)
			newLine.Converted = argLine
			newLine.Arg = argDetails
		}

		// Process RUN commands
		if line.Run != nil && line.Run.Shell != nil && line.Run.Shell.Before != nil {
			processRunLine(ctx, newLine, line, stagePackages, provider)
		}

		// Add the converted line to the result
		converted.Lines[i] = newLine
	}

	// Second pass: add USER root directives where needed
	addUserRootDirectives(converted.Lines)

	return converted, nil
}

// detectStagesWithRunCommands identifies which stages contain RUN commands
func detectStagesWithRunCommands(lines []*DockerfileLine) map[int]bool {
	stagesWithRunCommands := make(map[int]bool)

	for _, line := range lines {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line.Raw)), DirectiveRun+" ") {
			stagesWithRunCommands[line.Stage] = true
		}
	}

	return stagesWithRunCommands
}

// identifyArgsUsedAsBaseImages identifies ARGs that are used as base images
func identifyArgsUsedAsBaseImages(lines []*DockerfileLine, argNameToLine map[string]*DockerfileLine, argsUsedAsBase map[string]bool) {
	for _, line := range lines {
		if line.Arg != nil && line.Arg.Name != "" {
			argNameToLine[line.Arg.Name] = line
		}

		if line.From != nil && line.From.BaseDynamic {
			// Check if the base contains a reference to an ARG
			baseName := line.From.Base
			if strings.HasPrefix(baseName, "$") {
				// Handle both ${VAR} and $VAR formats
				argName := baseName[1:] // Remove the '$'
				if strings.HasPrefix(argName, "{") && strings.HasSuffix(argName, "}") {
					argName = argName[1 : len(argName)-1] // Remove the '{}' brackets
				}
				argsUsedAsBase[argName] = true
			}
		}
	}

	// Mark the ARGs used as base
	for argName := range argsUsedAsBase {
		if line, exists := argNameToLine[argName]; exists && line.Arg != nil {
			line.Arg.UsedAsBase = true
		}
	}
}

// copyFromDetails creates a deep copy of FromDetails
func copyFromDetails(from *FromDetails) *FromDetails {
	return &FromDetails{
		Base:        from.Base,
		Tag:         from.Tag,
		Digest:      from.Digest,
		Alias:       from.Alias,
		Parent:      from.Parent,
		BaseDynamic: from.BaseDynamic,
		TagDynamic:  from.TagDynamic,
		Orig:        from.Orig,
	}
}

// convertFromLine handles converting a FROM line
func convertFromLine(ctx context.Context, from *FromDetails, stage int, stagesWithRunCommands map[int]bool, opts Options) string {
	log := clog.FromContext(ctx)

	// First, always do the default Chainguard conversion
	// Determine if we need the -dev suffix
	needsDevSuffix := stagesWithRunCommands[stage]

	// Get the converted base without tag
	base := from.Base
	tag := from.Tag

	// Handle the basename
	baseFilename := filepath.Base(base)

	// Get the appropriate Chainguard image name using mappings
	targetImage := baseFilename
	var convertedTag string

	// Use the mapping provider if available
	if opts.MappingProvider != nil {
		// Try to find mappings for this image
		variants := generateDockerHubVariants(base)
		variants = append([]string{base}, variants...)

		for _, variant := range variants {
			if variant == "" {
				continue
			}

			target, found, err := opts.MappingProvider.GetImageMapping(ctx, variant)
			if err != nil {
				log.Warn("Error looking up image mapping", "source", variant, "error", err)
				continue
			}

			if found {
				log.Debug("Found image mapping", "source", variant, "target", target)
				targetImage = target
				break
			}
		}
	} else if opts.ExtraMappings.Images != nil {
		// Check for exact match in ExtraMappings
		if target, ok := opts.ExtraMappings.Images[base]; ok {
			log.Debug("Found exact image mapping in legacy mappings", "source", base, "target", target)
			targetImage = target
		} else {
			// Try normalize name (Docker Hub variants)
			for _, variant := range generateDockerHubVariants(base) {
				if variant == "" {
					continue
				}

				if target, ok := opts.ExtraMappings.Images[variant]; ok {
					log.Debug("Found normalized image mapping in legacy mappings", "source", variant, "target", target)
					targetImage = target
					break
				}
			}

			// Try wildcard match
			for mappingBase, mappingTarget := range opts.ExtraMappings.Images {
				if strings.Contains(mappingBase, "*") {
					pattern := strings.ReplaceAll(mappingBase, "*", ".*")
					matched, _ := regexp.MatchString("^"+pattern+"$", base)
					if matched {
						log.Debug("Found wildcard image mapping in legacy mappings", "pattern", mappingBase, "source", base, "target", mappingTarget)
						targetImage = mappingTarget
						break
					}
				}
			}
		}
	}

	// Calculate the tag if needed
	if tag != "" && !from.TagDynamic {
		convertedTag = calculateConvertedTag(baseFilename, tag, from.TagDynamic, needsDevSuffix)
	} else if needsDevSuffix {
		convertedTag = "latest-dev"
	} else {
		convertedTag = "latest"
	}

	// Build the full image reference
	imageRef := buildImageReference(targetImage, convertedTag, opts)

	// Add the digest if present
	if from.Digest != "" {
		imageRef += "@" + from.Digest
	}

	// Add the alias if present
	if from.Alias != "" {
		imageRef += " AS " + from.Alias
	}

	// Apply custom FROM line conversion if provided
	if opts.FromLineConverter != nil {
		converted, err := opts.FromLineConverter(from, imageRef, needsDevSuffix)
		if err != nil {
			log.Warn("Error applying custom FROM line conversion", "error", err)
		} else if converted != "" {
			log.Debug("Applied custom FROM line conversion", "original", imageRef, "converted", converted)
			return converted
		}
	}

	return imageRef
}

// convertArgLine converts an ARG line that is used as a base image
func convertArgLine(ctx context.Context, arg *ArgDetails, lines []*DockerfileLine, stagesWithRunCommands map[int]bool, opts Options) (string, *ArgDetails) {
	// Create a copy of the arg
	newArg := &ArgDetails{
		Name:         arg.Name,
		DefaultValue: arg.DefaultValue,
		UsedAsBase:   arg.UsedAsBase,
	}

	// Only convert the default value if it's used as a base
	if arg.UsedAsBase && arg.DefaultValue != "" {
		// Find which stages use this ARG as their base
		needsDevSuffix := determineIfArgNeedsDevSuffix(arg.Name, lines, stagesWithRunCommands)

		// Parse the default value as an image reference
		base, tag := parseImageReference(arg.DefaultValue)

		// Create a fake FROM details for conversion
		from := &FromDetails{
			Base: base,
			Tag:  tag,
			Orig: arg.DefaultValue,
		}

		// Convert using the FROM line converter
		convertedImage := convertFromLine(ctx, from, -1, stagesWithRunCommands, opts)

		// Update the ARG details
		newArg.DefaultValue = convertedImage
	}

	// Build the converted ARG line
	if newArg.DefaultValue != "" {
		return fmt.Sprintf("ARG %s=%s", newArg.Name, newArg.DefaultValue), newArg
	}
	return fmt.Sprintf("ARG %s", newArg.Name), newArg
}

// determineIfArgNeedsDevSuffix determines if an ARG used as base needs a -dev suffix
func determineIfArgNeedsDevSuffix(argName string, lines []*DockerfileLine, stagesWithRunCommands map[int]bool) bool {
	for _, line := range lines {
		if line.From != nil && line.From.BaseDynamic &&
			(strings.Contains(line.From.Base, "${"+argName+"}") ||
				strings.Contains(line.From.Base, "$"+argName)) {
			return stagesWithRunCommands[line.Stage]
		}
	}
	return false
}

// calculateConvertedTag calculates the appropriate tag based on the base image and whether -dev is needed
func calculateConvertedTag(baseFilename string, tag string, isDynamicTag bool, needsDevSuffix bool) string {
	var convertedTag string

	// Special case for chainguard-base - always use latest
	if baseFilename == DefaultChainguardBase {
		return "latest" // Always use latest tag for chainguard-base, no -dev suffix ever
	}

	// First process the tag normally (including semantic version truncation)
	switch {
	case tag == "":
		convertedTag = "latest"
	case strings.Contains(tag, "$"):
		// For dynamic tags, preserve the original tag
		convertedTag = tag
	default:
		// Convert the tag normally for static tags
		convertedTag = convertImageTag(tag, isDynamicTag)
	}

	// Special case for JDK/JRE - prepend "openjdk-" to the tag unless it's "latest" or "latest-dev"
	if (baseFilename == "jdk" || baseFilename == "jre") && convertedTag != "latest" && convertedTag != "latest-dev" {
		convertedTag = "openjdk-" + convertedTag
	}

	// Add -dev suffix if needed
	if needsDevSuffix && convertedTag != "latest" {
		// Ensure we don't accidentally add -dev twice
		if !strings.HasSuffix(convertedTag, "-dev") {
			convertedTag += "-dev"
		}
	} else if needsDevSuffix && convertedTag == "latest" {
		convertedTag = DefaultImageTag
	}

	return convertedTag
}

// buildImageReference builds the full image reference with registry, org, and tag
func buildImageReference(baseFilename string, tag string, opts Options) string {
	var newBase string

	// If registry is specified, use registry/basename
	if opts.Registry != "" {
		newBase = opts.Registry + "/" + baseFilename
	} else {
		// Otherwise use DefaultRegistryDomain/org/basename
		org := opts.Organization
		if org == "" {
			org = DefaultOrg
		}
		newBase = DefaultRegistryDomain + "/" + org + "/" + baseFilename
	}

	// Combine into a reference
	if tag != "" {
		return newBase + ":" + tag
	}
	return newBase
}

// processRunLine handles converting a RUN line
func processRunLine(ctx context.Context, newLine *DockerfileLine, line *DockerfileLine, stagePackages map[int][]string, provider MappingProvider) {
	log := clog.FromContext(ctx)

	// Deep clone the ShellCommand to avoid modifying the original
	newShell := cloneShellCommand(line.Run.Shell.Before)

	// Check if we need to convert package manager commands
	converted, distro, manager, packages, newPackages, newShell := convertPackageManagerCommands(ctx, newShell, provider)
	if converted {
		// Get the stage
		stage := line.Stage

		// Update the stage packages
		if stagePackages[stage] == nil {
			stagePackages[stage] = make([]string, 0)
		}
		stagePackages[stage] = append(stagePackages[stage], packages...)

		// Update the Run details
		if newLine.Run == nil {
			newLine.Run = &RunDetails{}
		}
		newLine.Run.Distro = distro
		newLine.Run.Manager = manager
		newLine.Run.Packages = packages

		// Update the line's Shell
		newLine.Run.Shell = &RunDetailsShell{
			Before: newShell,
		}

		// Construct the converted line
		var convertedLine strings.Builder
		convertedLine.WriteString("RUN ")
		convertedLine.WriteString(newShell.String())
		newLine.Converted = convertedLine.String()
	} else {
		// Also try to convert Busybox commands like tar
		busyboxConverted, newBusyboxShell := convertBusyboxCommands(newShell, stagePackages[line.Stage])
		if busyboxConverted {
			// Update the line's Shell
			if newLine.Run == nil {
				newLine.Run = &RunDetails{}
			}
			newLine.Run.Shell = &RunDetailsShell{
				Before: newBusyboxShell,
			}

			// Construct the converted line
			var convertedLine strings.Builder
			convertedLine.WriteString("RUN ")
			convertedLine.WriteString(newBusyboxShell.String())
			newLine.Converted = convertedLine.String()
		}
	}
}

// convertPackageManagerCommands converts package manager commands (apt-get, yum, etc.)
func convertPackageManagerCommands(ctx context.Context, shell *ShellCommand, provider MappingProvider) (bool, Distro, Manager, []string, []string, *ShellCommand) {
	if shell == nil {
		return false, "", "", nil, nil, nil
	}

	// Create a deep copy of the shell command to avoid modifying the original
	newShell := cloneShellCommand(shell)
	if newShell == nil || len(newShell.Parts) == 0 {
		return false, "", "", nil, nil, newShell
	}

	// Check for package manager commands
	var foundPMCommand bool
	var distro Distro
	var manager Manager
	var packagesDetected []string
	var packagesToInstall []string

	// Map command parts to the Alpine equivalent (apk add)
	for i, part := range newShell.Parts {
		// Get the command from the first part
		command := part.Value

		// Check if it's a package manager command
		packageManagerInfo := getPackageManagerInfo(command)
		if packageManagerInfo.Distro != "" {
			distro = packageManagerInfo.Distro
			manager = Manager(command)

			// Look for the install keyword in this or subsequent parts
			for j := i; j < len(newShell.Parts); j++ {
				if strings.Contains(newShell.Parts[j].Value, packageManagerInfo.InstallKeyword) {
					// Found package manager install command

					// Skip the command and install keyword parts
					for k := j + 1; k < len(newShell.Parts); k++ {
						// Look for package names, skipping flags
						arg := newShell.Parts[k].Value
						if !strings.HasPrefix(arg, "-") && arg != "install" && arg != "upgrade" && arg != "add" && arg != packageManagerInfo.InstallKeyword {
							// Looks like a package name
							packagesDetected = append(packagesDetected, arg)

							// Look up package mappings using the provider
							targets, found, err := provider.GetPackageMappings(ctx, distro, arg)
							if err != nil {
								// Just log the error and continue with the original package
								continue
							}

							if found && len(targets) > 0 {
								packagesToInstall = append(packagesToInstall, targets...)
							} else {
								// If no mapping is found, keep the original package
								packagesToInstall = append(packagesToInstall, arg)
							}
						}
					}

					// We found and processed a package manager command
					foundPMCommand = true
					break
				}
			}

			// If we found a package manager command, convert it
			if foundPMCommand {
				var updatedParts []*ShellPart

				// Create the new command (apk add ...)
				updatedParts = append(updatedParts, &ShellPart{Value: "apk"})
				updatedParts = append(updatedParts, &ShellPart{Value: "add"})

				// Add each package from packagesToInstall
				uniquePackages := map[string]bool{}
				for _, pkg := range packagesToInstall {
					if pkg != "" && !uniquePackages[pkg] {
						updatedParts = append(updatedParts, &ShellPart{Value: pkg})
						uniquePackages[pkg] = true
					}
				}

				// Replace the shell parts with our updated command
				newShell.Parts = updatedParts
				break
			}
		}
	}

	return foundPMCommand, distro, manager, packagesDetected, packagesToInstall, newShell
}

// Helper function to clone a shell part
func cloneShellPart(part *ShellPart) *ShellPart {
	newPart := &ShellPart{
		ExtraPre:  part.ExtraPre,
		Command:   part.Command,
		Delimiter: part.Delimiter,
	}
	if part.Args != nil {
		newPart.Args = make([]string, len(part.Args))
		copy(newPart.Args, part.Args)
	}
	return newPart
}

// CommandConverter defines a function type for converting shell commands
type CommandConverter func(*ShellPart) *ShellPart

// CommandHandler represents a handler for a specific command conversion
type CommandHandler struct {
	Command             string
	Converter           CommandConverter
	SkipIfShadowPresent bool // If true, only convert when shadow is NOT installed
}

// convertBusyboxCommands converts useradd and groupadd commands to adduser and addgroup and modifies the tar command syntax
func convertBusyboxCommands(shell *ShellCommand, stagePackages []string) (bool, *ShellCommand) {
	if shell == nil || len(shell.Parts) == 0 {
		return false, shell
	}

	// Define command handlers
	commandHandlers := []CommandHandler{
		{
			Command:             CommandUserAdd,
			Converter:           ConvertUserAddToAddUser,
			SkipIfShadowPresent: true,
		},
		{
			Command:             CommandGroupAdd,
			Converter:           ConvertGroupAddToAddGroup,
			SkipIfShadowPresent: true,
		},
		{
			Command:   CommandGNUTar,
			Converter: ConvertGNUTarToBusyboxTar,
		},
	}

	// Create new shell command to hold the converted parts
	convertedParts := make([]*ShellPart, len(shell.Parts))
	modified := false

	// Check if shadow is installed
	hasShadow := slices.Contains(stagePackages, PackageShadow)

	// Process each shell part
	for i, part := range shell.Parts {
		converted := false

		// Try each handler in the registry
		for _, handler := range commandHandlers {
			// Skip if this handler requires shadow checking and shadow is installed
			if handler.SkipIfShadowPresent && hasShadow {
				continue
			}

			// Check if this command matches
			if part.Command == handler.Command {
				convertedPart := handler.Converter(part)
				// Check if conversion actually changed anything
				if convertedPart.Command != part.Command || !slices.Equal(convertedPart.Args, part.Args) {
					convertedParts[i] = convertedPart
					modified = true
					converted = true
					break
				}
			}
		}

		// If no conversion was applied, copy the original part
		if !converted {
			convertedParts[i] = cloneShellPart(part)
		}
	}

	if modified {
		return true, &ShellCommand{Parts: convertedParts}
	}

	return false, shell
}

// generateDockerHubVariants generates all possible Docker Hub variants for a given base
func generateDockerHubVariants(base string) []string {
	variants := []string{base}

	// Check if base already has a registry prefix
	if strings.Contains(base, "/") && strings.Contains(base, ".") {
		// It's already a fully qualified name, don't generate additional variants
		return variants
	}

	// Split the base to handle different cases
	parts := strings.Split(base, "/")
	var imageName string
	var org string

	// Handle different formats
	if len(parts) == 1 {
		// Format: "node"
		imageName = parts[0]
		variants = append(variants,
			"docker.io/"+imageName,
			"docker.io/library/"+imageName,
			"registry-1.docker.io/library/"+imageName,
			"index.docker.io/"+imageName,
			"index.docker.io/library/"+imageName)
	} else if len(parts) == 2 {
		// Format: "someorg/someimage"
		org = parts[0]
		imageName = parts[1]
		variants = append(variants,
			"docker.io/"+org+"/"+imageName,
			"registry-1.docker.io/"+org+"/"+imageName,
			"index.docker.io/"+org+"/"+imageName)
	}

	return variants
}

// normalizeImageName normalizes Docker Hub image references
func normalizeImageName(imageRef string) string {
	// Remove any trailing slashes
	imageRef = strings.TrimRight(imageRef, "/")

	// Docker Hub registry domains to strip
	dockerHubDomains := []string{
		"registry-1.docker.io/",
		"docker.io/",
		"index.docker.io/",
	}

	// Remove Docker Hub registry prefixes if present
	for _, domain := range dockerHubDomains {
		if strings.HasPrefix(imageRef, domain) {
			return strings.TrimPrefix(imageRef, domain)
		}
	}

	return imageRef
}

// MappingProvider is an interface for getting package and image mappings
type MappingProvider interface {
	// GetImageMapping returns the target image for a source image
	GetImageMapping(ctx context.Context, sourceImage string) (string, bool, error)

	// GetPackageMappings returns the target packages for a source package
	GetPackageMappings(ctx context.Context, distro Distro, sourcePackage string) ([]string, bool, error)
}

// DBMappingProvider implements MappingProvider using direct database queries
type DBMappingProvider struct {
	db *DBConnection
}

// NewDBMappingProvider creates a new DBMappingProvider
func NewDBMappingProvider(db *DBConnection) *DBMappingProvider {
	return &DBMappingProvider{db: db}
}

// GetImageMapping gets an image mapping from the database
func (p *DBMappingProvider) GetImageMapping(ctx context.Context, sourceImage string) (string, bool, error) {
	return GetImageMapping(ctx, p.db, sourceImage)
}

// GetPackageMappings gets package mappings from the database
func (p *DBMappingProvider) GetPackageMappings(ctx context.Context, distro Distro, sourcePackage string) ([]string, bool, error) {
	return GetPackageMappings(ctx, p.db, distro, sourcePackage)
}

// InMemoryMappingProvider implements MappingProvider using in-memory mappings
type InMemoryMappingProvider struct {
	mappings MappingsConfig
}

// NewInMemoryMappingProvider creates a new InMemoryMappingProvider
func NewInMemoryMappingProvider(mappings MappingsConfig) *InMemoryMappingProvider {
	return &InMemoryMappingProvider{mappings: mappings}
}

// GetImageMapping gets an image mapping from in-memory mappings
func (p *InMemoryMappingProvider) GetImageMapping(ctx context.Context, sourceImage string) (string, bool, error) {
	log := clog.FromContext(ctx)
	log.Debug("Looking up image mapping in memory", "source", sourceImage)

	// Check for exact match
	if targetImage, ok := p.mappings.Images[sourceImage]; ok {
		log.Debug("Found exact image mapping in memory", "source", sourceImage, "target", targetImage)
		return targetImage, true, nil
	}

	// Check for wildcard matches
	for source, target := range p.mappings.Images {
		if strings.Contains(source, "*") {
			// Convert wildcard pattern to regex
			pattern := strings.ReplaceAll(source, "*", ".*")
			matched, err := regexp.MatchString("^"+pattern+"$", sourceImage)
			if err != nil {
				log.Debug("Error matching pattern", "pattern", pattern, "error", err)
				continue
			}

			if matched {
				log.Debug("Found wildcard match in memory", "pattern", source, "source", sourceImage, "target", target)
				return target, true, nil
			}
		}
	}

	return "", false, nil
}

// GetPackageMappings gets package mappings from in-memory mappings
func (p *InMemoryMappingProvider) GetPackageMappings(ctx context.Context, distro Distro, sourcePackage string) ([]string, bool, error) {
	log := clog.FromContext(ctx)
	log.Debug("Looking up package mapping in memory", "distro", distro, "source", sourcePackage)

	// Check if the distro exists
	distroMappings, ok := p.mappings.Packages[distro]
	if !ok {
		return nil, false, nil
	}

	// Check if the package exists
	targetPackages, ok := distroMappings[sourcePackage]
	if !ok || len(targetPackages) == 0 {
		return nil, false, nil
	}

	log.Debug("Found package mappings in memory", "distro", distro, "source", sourcePackage, "targets", targetPackages)
	return targetPackages, true, nil
}

// ChainedMappingProvider implements MappingProvider by chaining multiple providers
type ChainedMappingProvider struct {
	providers []MappingProvider
}

// NewChainedMappingProvider creates a new ChainedMappingProvider
func NewChainedMappingProvider(providers ...MappingProvider) *ChainedMappingProvider {
	return &ChainedMappingProvider{providers: providers}
}

// GetImageMapping gets an image mapping by checking each provider in order
func (p *ChainedMappingProvider) GetImageMapping(ctx context.Context, sourceImage string) (string, bool, error) {
	for _, provider := range p.providers {
		targetImage, found, err := provider.GetImageMapping(ctx, sourceImage)
		if err != nil {
			return "", false, err
		}
		if found {
			return targetImage, true, nil
		}
	}
	return "", false, nil
}

// GetPackageMappings gets package mappings by checking each provider in order
func (p *ChainedMappingProvider) GetPackageMappings(ctx context.Context, distro Distro, sourcePackage string) ([]string, bool, error) {
	for _, provider := range p.providers {
		targetPackages, found, err := provider.GetPackageMappings(ctx, distro, sourcePackage)
		if err != nil {
			return nil, false, err
		}
		if found {
			return targetPackages, true, nil
		}
	}
	return nil, false, nil
}

// shouldConvertFromLine determines if a FROM line should be converted
func shouldConvertFromLine(from *FromDetails) bool {
	// Skip conversion for scratch, parent stages, or dynamic bases
	if from.Base == "scratch" || from.Parent > 0 || from.BaseDynamic {
		return false
	}
	return true
}

// addUserRootDirectives adds USER root directives where needed
func addUserRootDirectives(lines []*DockerfileLine) {
	// First determine which stages have converted RUN lines
	stagesWithConvertedRuns := make(map[int]bool)
	// Also keep track of stages that already have USER root directives
	stagesWithUserRoot := make(map[int]bool)

	// First pass - identify stages with converted RUN lines and existing USER root directives
	for _, line := range lines {
		// Check if this is a converted RUN line
		if line.Run != nil && line.Converted != "" {
			stagesWithConvertedRuns[line.Stage] = true
		}

		// Check if this line is a USER directive with root
		raw := line.Raw
		converted := line.Converted

		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(raw)), DirectiveUser+" ") &&
			strings.Contains(strings.ToLower(raw), DefaultUser) {
			stagesWithUserRoot[line.Stage] = true
		}

		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(converted)), DirectiveUser+" ") &&
			strings.Contains(strings.ToLower(converted), DefaultUser) {
			stagesWithUserRoot[line.Stage] = true
		}
	}

	// If we found any stages with converted RUN lines, add USER root after the FROM
	if len(stagesWithConvertedRuns) > 0 {
		for _, line := range lines {
			// Check if this is a FROM line in a stage that has converted RUN lines
			if line.From != nil && stagesWithConvertedRuns[line.Stage] {
				// If the FROM line was converted and there's no USER root directive in this stage already
				if line.Converted != "" && !stagesWithUserRoot[line.Stage] {
					// Add a USER root directive after this FROM line
					line.Converted += "\n" + DirectiveUser + " " + DefaultUser
					// Mark this stage as having a USER root directive
					stagesWithUserRoot[line.Stage] = true
				}
			}
		}
	}
}

// cloneShellCommand creates a deep copy of a ShellCommand
func cloneShellCommand(cmd *ShellCommand) *ShellCommand {
	if cmd == nil {
		return nil
	}

	result := &ShellCommand{
		Parts: make([]*ShellPart, len(cmd.Parts)),
	}

	for i, part := range cmd.Parts {
		result.Parts[i] = cloneShellPart(part)
	}

	return result
}

// convertImageTag returns the converted image tag
func convertImageTag(tag string, _ bool) string {
	if tag == "" {
		return DefaultImageTag
	}

	// Remove anything after and including the first hyphen
	if hyphenIndex := strings.Index(tag, "-"); hyphenIndex != -1 {
		tag = tag[:hyphenIndex]
	}

	// If tag has 'v' prefix for semver, remove it
	if len(tag) > 0 && tag[0] == 'v' && (len(tag) > 1 && (tag[1] >= '0' && tag[1] <= '9')) {
		tag = tag[1:]
	}

	// Check if this is a semver tag (e.g. 1.2.3)
	semverParts := strings.Split(tag, ".")
	isSemver := false

	// Consider tags that are just a number (like "9" or "18") as valid semver-like tags
	if len(semverParts) == 1 {
		_, err := strconv.Atoi(semverParts[0])
		if err == nil {
			isSemver = true
		}
	} else if len(semverParts) >= 2 {
		// Check if at least the first two parts are numeric
		major, majorErr := strconv.Atoi(semverParts[0])
		minor, minorErr := strconv.Atoi(semverParts[1])
		if majorErr == nil && minorErr == nil && major >= 0 && minor >= 0 {
			isSemver = true
			// Keep only major.minor for semver tags
			if len(semverParts) > 2 {
				tag = fmt.Sprintf("%d.%d", major, minor)
			}
		}
	}

	// If not a semver and not latest, use latest
	if !isSemver && tag != "latest" {
		return "latest"
	}

	return tag
}

// fileExists returns true if the file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
