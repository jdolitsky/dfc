/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package dfc

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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

			// Store the original image reference before we split it up
			if origImageRef != "" {
				// We need to preserve the original reference exactly as is
				// origImageRef already has the right value at this point
			}

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
// If an error is returned, the original image reference will be used instead.
// The converter is only responsible for returning the image reference part (e.g., "cgr.dev/chainguard/node:latest"),
// not the full FROM line with directives like "AS" - those will be handled by the calling code.
//
// Example usage of a custom converter:
//
//	myConverter := func(from *FromDetails, converted string, stageHasRuns bool) (string, error) {
//	    // For most images, just use the default Chainguard conversion
//	    if from.Base != "python" {
//	        return converted, nil
//	    }
//
//	    // Special handling for python images
//	    return "myregistry.example.com/python:" + from.Tag, nil
//	}
//
//	// Use the custom converter with DFC
//	dockerFile.Convert(ctx, dfc.Options{
//	    Organization: "myorg",
//	    FromLineConverter: myConverter,
//	})
type FromLineConverter func(from *FromDetails, converted string, stageHasRuns bool) (string, error)

// Options defines the configuration options for the conversion
type Options struct {
	Organization      string
	Registry          string
	ExtraMappings     MappingsConfig
	Update            bool              // When true, update cached mappings before conversion
	NoBuiltIn         bool              // When true, don't use built-in mappings, only ExtraMappings
	FromLineConverter FromLineConverter // Optional custom converter for FROM lines
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
	// Initialize mappings
	var mappings MappingsConfig

	// Handle mappings based on options
	if !opts.NoBuiltIn {
		// Load the default mappings (unless NoBuiltIn is true)
		defaultMappings, err := defaultGetDefaultMappings(ctx, opts.Update)
		if err != nil {
			return nil, fmt.Errorf("loading default mappings: %w", err)
		}

		// Use default mappings
		mappings = defaultMappings

		// Merge with the extra mappings if provided
		if len(opts.ExtraMappings.Images) > 0 || len(opts.ExtraMappings.Packages) > 0 {
			mappings = MergeMappings(defaultMappings, opts.ExtraMappings)
		}
	} else {
		// NoBuiltIn is true, use only ExtraMappings if provided
		// Otherwise, use empty mappings
		mappings = opts.ExtraMappings
		// Initialize empty maps if they don't exist
		if mappings.Images == nil {
			mappings.Images = make(map[string]string)
		}
		if mappings.Packages == nil {
			mappings.Packages = make(PackageMap)
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
				// Use the merged mappings for conversion
				optsWithMappings := Options{
					Organization:      opts.Organization,
					Registry:          opts.Registry,
					ExtraMappings:     mappings,
					FromLineConverter: opts.FromLineConverter,
				}
				newLine.Converted = convertFromLine(line.From, line.Stage, stagesWithRunCommands, optsWithMappings)
			}
		}

		// Handle ARG lines that are used as base images
		if line.Arg != nil && line.Arg.UsedAsBase && line.Arg.DefaultValue != "" {
			// Use the merged mappings for conversion
			optsWithMappings := Options{
				Organization:      opts.Organization,
				Registry:          opts.Registry,
				ExtraMappings:     mappings,
				FromLineConverter: opts.FromLineConverter,
			}
			argLine, argDetails := convertArgLine(line.Arg, d.Lines, stagesWithRunCommands, optsWithMappings)
			newLine.Converted = argLine
			newLine.Arg = argDetails
		}

		// Process RUN commands
		if line.Run != nil && line.Run.Shell != nil && line.Run.Shell.Before != nil {
			processRunLine(newLine, line, stagePackages, mappings.Packages)
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
func convertFromLine(from *FromDetails, stage int, stagesWithRunCommands map[int]bool, opts Options) string {
	// First, always do the default Chainguard conversion
	// Determine if we need the -dev suffix
	needsDevSuffix := stagesWithRunCommands[stage]

	// Get the converted base without tag
	base := from.Base
	tag := from.Tag

	// First check for exact match on the full image reference (with tag) if tag is present
	var mappedImage string
	if tag != "" {
		// Try exact match first
		fullImageRef := base + ":" + tag
		if img, ok := opts.ExtraMappings.Images[fullImageRef]; ok {
			mappedImage = img
		} else {
			// Try normalized variants for Docker Hub images with tags

			// First try normalizing the base (removes Docker Hub prefixes)
			normalizedBase := normalizeImageName(base)
			if normalizedBase != base {
				normalizedRef := normalizedBase + ":" + tag
				if img, ok := opts.ExtraMappings.Images[normalizedRef]; ok {
					mappedImage = img
				}
			}

			// If still no match, try with Docker Hub variants
			if mappedImage == "" {
				// Generate variants like "docker.io/library/node:23.9"
				baseVariants := generateDockerHubVariants(base)
				for _, variant := range baseVariants {
					variantWithTag := variant + ":" + tag
					if img, ok := opts.ExtraMappings.Images[variantWithTag]; ok {
						mappedImage = img
						break
					}
				}
			}

			// If still no match, try removing 'library/' prefix if present
			if mappedImage == "" && strings.HasPrefix(normalizedBase, "library/") {
				simpleBase := strings.TrimPrefix(normalizedBase, "library/")
				simpleRef := simpleBase + ":" + tag
				if img, ok := opts.ExtraMappings.Images[simpleRef]; ok {
					mappedImage = img
				}
			}
		}
	}

	// If no match on full reference, continue with the existing logic
	if mappedImage == "" {
		// Handle the basename
		baseFilename := filepath.Base(base)

		// Get the appropriate Chainguard image name using mappings
		targetImage := baseFilename
		var convertedTag string

		// Check for exact match first, in specific order
		// For example, if the mapping is just node, it should match all of the following:
		// FROM registry-1.docker.io/library/node
		// FROM docker.io/node
		// FROM docker.io/library/node
		// FROM index.docker.io/node
		// FROM index.docker.io/library/node
		//
		// If the mapping is someorg/somerepo, it should match all of the following:
		// FROM registry-1.docker.io/someorg/somerepo
		// FROM docker.io/someorg/somerepo
		// FROM index.docker.io/someorg/somerepo

		// Try exact match first
		if img, ok := opts.ExtraMappings.Images[base]; ok {
			mappedImage = img
		} else if img, ok := opts.ExtraMappings.Images[baseFilename]; ok {
			mappedImage = img
		} else {
			// Generate all possible variants for the base image
			baseVariants := generateDockerHubVariants(base)

			// Check if any variant matches a key in the mappings
			for _, variant := range baseVariants {
				if img, ok := opts.ExtraMappings.Images[variant]; ok {
					mappedImage = img
					break
				}
			}

			// If still no match, try to normalize the base and check against simple keys
			if mappedImage == "" {
				normalizedBase := normalizeImageName(base)

				// Check if the normalized base matches any key
				if img, ok := opts.ExtraMappings.Images[normalizedBase]; ok {
					mappedImage = img
				} else {
					// Try removing library/ prefix if it exists
					if strings.HasPrefix(normalizedBase, "library/") {
						simpleBase := strings.TrimPrefix(normalizedBase, "library/")
						if img, ok := opts.ExtraMappings.Images[simpleBase]; ok {
							mappedImage = img
						}
					}
				}
			}

			// If still no match, check for glob patterns with asterisks
			if mappedImage == "" {
				for pattern, img := range opts.ExtraMappings.Images {
					if strings.HasSuffix(pattern, "*") {
						prefix := strings.TrimSuffix(pattern, "*")
						if strings.HasPrefix(baseFilename, prefix) {
							mappedImage = img
							break
						}
					}
				}
			}
		}

		// Process the mapped image if found
		if mappedImage != "" {
			// Check if the mapped image includes a tag
			if parts := strings.Split(mappedImage, ":"); len(parts) > 1 {
				targetImage = parts[0]
				convertedTag = parts[1]
			} else {
				targetImage = mappedImage
			}
		}

		// If targetTag is not specified in mapping, calculate it using the existing logic
		if convertedTag == "" {
			convertedTag = calculateConvertedTag(targetImage, tag, from.TagDynamic, needsDevSuffix)
		}

		// Build the image reference
		chainguardImageRef := buildImageReference(targetImage, convertedTag, opts)

		// Now, if a custom converter is provided, let it process the result
		if opts.FromLineConverter != nil {
			customImageRef, err := opts.FromLineConverter(from, chainguardImageRef, needsDevSuffix)
			if err != nil {
				// If an error occurs, still return a valid FROM line using the original image
				fromLine := DirectiveFrom + " " + from.Orig
				if from.Alias != "" {
					fromLine += " " + KeywordAs + " " + from.Alias
				}
				return fromLine
			}

			// Create the converted FROM line with the custom image
			fromLine := DirectiveFrom + " " + customImageRef
			if from.Alias != "" {
				fromLine += " " + KeywordAs + " " + from.Alias
			}
			return fromLine
		}

		// If no custom converter, use the Chainguard converted reference
		fromLine := DirectiveFrom + " " + chainguardImageRef
		if from.Alias != "" {
			fromLine += " " + KeywordAs + " " + from.Alias
		}

		return fromLine
	} else {
		// We found a direct match on base:tag in the mappings or a Docker Hub variant
		finalImageRef := handleTaggedMappedImage(mappedImage, tag, needsDevSuffix, opts)

		// Now, if a custom converter is provided, let it process the result
		if opts.FromLineConverter != nil {
			customImageRef, err := opts.FromLineConverter(from, finalImageRef, needsDevSuffix)
			if err != nil {
				// If an error occurs, still return a valid FROM line using the original image
				fromLine := DirectiveFrom + " " + from.Orig
				if from.Alias != "" {
					fromLine += " " + KeywordAs + " " + from.Alias
				}
				return fromLine
			}

			// Create the converted FROM line with the custom image
			fromLine := DirectiveFrom + " " + customImageRef
			if from.Alias != "" {
				fromLine += " " + KeywordAs + " " + from.Alias
			}
			return fromLine
		}

		// If no custom converter, use the mapped reference
		fromLine := DirectiveFrom + " " + finalImageRef
		if from.Alias != "" {
			fromLine += " " + KeywordAs + " " + from.Alias
		}

		return fromLine
	}
}

// convertArgLine handles converting an ARG line used as base image
func convertArgLine(arg *ArgDetails, lines []*DockerfileLine, stagesWithRunCommands map[int]bool, opts Options) (string, *ArgDetails) {
	// Create a FromDetails structure from the ARG default value
	base, tag := parseImageReference(arg.DefaultValue)

	// Create a FromDetails to represent this ARG value as a FROM line
	fromDetails := &FromDetails{
		Base: base,
		Tag:  tag,
		Orig: arg.DefaultValue,
	}

	// Determine if we need the -dev suffix
	needsDevSuffix := determineIfArgNeedsDevSuffix(arg.Name, lines, stagesWithRunCommands)

	// First check for exact match on the full image reference (with tag) if tag is present
	var chainguardImageRef string
	if tag != "" {
		// Try exact match first
		fullImageRef := base + ":" + tag
		if img, ok := opts.ExtraMappings.Images[fullImageRef]; ok {
			// Found a direct match on base:tag in the mappings
			chainguardImageRef = handleTaggedMappedImage(img, tag, needsDevSuffix, opts)
		} else {
			// Try normalized variants for Docker Hub images with tags

			// First try normalizing the base (removes Docker Hub prefixes)
			normalizedBase := normalizeImageName(base)
			if normalizedBase != base {
				normalizedRef := normalizedBase + ":" + tag
				if img, ok := opts.ExtraMappings.Images[normalizedRef]; ok {
					chainguardImageRef = handleTaggedMappedImage(img, tag, needsDevSuffix, opts)
				}
			}

			// If still no match, try with Docker Hub variants
			if chainguardImageRef == "" {
				// Generate variants like "docker.io/library/node:23.9"
				baseVariants := generateDockerHubVariants(base)
				for _, variant := range baseVariants {
					variantWithTag := variant + ":" + tag
					if img, ok := opts.ExtraMappings.Images[variantWithTag]; ok {
						chainguardImageRef = handleTaggedMappedImage(img, tag, needsDevSuffix, opts)
						break
					}
				}
			}

			// If still no match, try removing 'library/' prefix if present
			if chainguardImageRef == "" && strings.HasPrefix(normalizedBase, "library/") {
				simpleBase := strings.TrimPrefix(normalizedBase, "library/")
				simpleRef := simpleBase + ":" + tag
				if img, ok := opts.ExtraMappings.Images[simpleRef]; ok {
					chainguardImageRef = handleTaggedMappedImage(img, tag, needsDevSuffix, opts)
				}
			}
		}
	}

	// If no direct match on full reference, continue with the existing logic
	if chainguardImageRef == "" {
		// First perform the default Chainguard conversion
		// Calculate default image reference using common approach
		baseFilename := filepath.Base(base)

		// Get the appropriate Chainguard image name using mappings
		targetImage := baseFilename
		var convertedTag string

		// Check for exact match first
		if img, ok := opts.ExtraMappings.Images[baseFilename]; ok {
			// Check if the mapped image includes a tag
			if parts := strings.Split(img, ":"); len(parts) > 1 {
				targetImage = parts[0]
				convertedTag = parts[1]
			} else {
				targetImage = img
			}
		} else {
			// No exact match, check for glob patterns with asterisks
			for pattern, img := range opts.ExtraMappings.Images {
				if strings.HasSuffix(pattern, "*") {
					prefix := strings.TrimSuffix(pattern, "*")
					if strings.HasPrefix(baseFilename, prefix) {
						// Found a match with a glob pattern
						if parts := strings.Split(img, ":"); len(parts) > 1 {
							targetImage = parts[0]
							convertedTag = parts[1]
						} else {
							targetImage = img
						}
						break
					}
				}
			}
		}

		// If targetTag is not specified in mapping, calculate it using the existing logic
		if convertedTag == "" {
			convertedTag = calculateConvertedTag(targetImage, tag, false, needsDevSuffix)
		}

		// Build the image reference
		chainguardImageRef = buildImageReference(targetImage, convertedTag, opts)
	}

	// Get the converted image reference
	var finalImageRef string

	// If a custom FROM line converter is provided, use it
	if opts.FromLineConverter != nil {
		customImageRef, err := opts.FromLineConverter(fromDetails, chainguardImageRef, needsDevSuffix)
		if err != nil {
			// On error, use the original value
			finalImageRef = arg.DefaultValue
		} else {
			finalImageRef = customImageRef
		}
	} else {
		// Use the default converter result
		finalImageRef = chainguardImageRef
	}

	// Create the converted ARG line
	argLine := DirectiveArg + " " + arg.Name + "=" + finalImageRef

	// Create the Arg details
	argDetails := &ArgDetails{
		Name:         arg.Name,
		DefaultValue: finalImageRef,
		UsedAsBase:   true,
	}

	return argLine, argDetails
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

	// Check if tag contains a variable reference (like ${NODE_VERSION})
	if strings.Contains(tag, "$") {
		// For dynamic tags, preserve the original tag
		convertedTag = tag
		// Add -dev suffix if needed
		if needsDevSuffix && !strings.HasSuffix(tag, "-dev") {
			convertedTag += "-dev"
		}
		return convertedTag
	}

	// Convert the tag normally for static tags
	convertedTag = convertImageTag(tag, isDynamicTag)

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

// processRunLine handles the conversion of RUN lines
func processRunLine(newLine *DockerfileLine, line *DockerfileLine, stagePackages map[int][]string, packageMap PackageMap) {
	beforeShell := line.Run.Shell.Before

	// Initialize RunDetails with Before shell
	newLine.Run = &RunDetails{
		Shell: &RunDetailsShell{
			Before: beforeShell,
		},
	}

	// First check for package manager commands
	modifiedPMCommands, distro, manager, packages, mappedPackages, afterShell :=
		convertPackageManagerCommands(beforeShell, packageMap)
	newLine.Run.Distro = distro
	newLine.Run.Manager = manager
	newLine.Run.Packages = packages

	// Add the mapped packages to the stage's package list
	if len(mappedPackages) > 0 {
		if _, exists := stagePackages[line.Stage]; !exists {
			stagePackages[line.Stage] = []string{}
		}
		stagePackages[line.Stage] = append(stagePackages[line.Stage], mappedPackages...)
	}

	modifiedBusyboxCommands := false
	modifiedBusyboxCommands, afterShell = convertBusyboxCommands(afterShell, stagePackages[line.Stage])

	// Check if we modified anything (related to package managers or useradd/groupadd)
	modifiedAnything := modifiedPMCommands || modifiedBusyboxCommands

	// If we modified the shell command, set After and Converted
	if modifiedAnything {
		newLine.Run.Shell.After = afterShell

		// Extract the original RUN directive from the raw line to preserve case
		rawLine := line.Raw
		upperRawLine := strings.ToUpper(rawLine)

		// Find the position of the case-insensitive "RUN " directive
		runPrefix := DirectiveRun + " "
		runIndex := strings.Index(upperRawLine, runPrefix)

		if runIndex != -1 {
			// Get the original case of the RUN directive
			originalRunDirective := rawLine[runIndex : runIndex+len(runPrefix)]
			newLine.Converted = originalRunDirective + afterShell.String()
		} else {
			// Fallback if we can't find the directive (shouldn't happen)
			newLine.Converted = DirectiveRun + " " + afterShell.String()
		}
	}
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

// shouldConvertFromLine determines if a FROM line should be converted
func shouldConvertFromLine(from *FromDetails) bool {
	// Skip conversion for scratch, parent stages, or dynamic bases
	if from.Base == "scratch" || from.Parent > 0 || from.BaseDynamic {
		return false
	}
	return true
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

// convertPackageManagerCommands converts package manager commands in a shell command
// to the Alpine equivalent (apk add)
func convertPackageManagerCommands(shell *ShellCommand, packageMap PackageMap) (bool, Distro, Manager, []string, []string, *ShellCommand) {
	if shell == nil {
		return false, "", "", nil, nil, nil
	}

	// First determine which distro/package manager we're dealing with
	// We'll only convert the first type of package manager we find
	var distro Distro
	var firstPM Manager
	packagesDetected := []string{}
	packagesToInstall := []string{}

	// Flag to indicate if any package manager command is found
	foundPMCommand := false

	// Process shell commands in one pass to identify package managers and collect packages
	for _, part := range shell.Parts {
		// Check if this is a known package manager command
		if pmInfo, ok := PackageManagerInfoMap[Manager(part.Command)]; ok {
			foundPMCommand = true

			// If this is the first package manager we've seen, use it for determining the distro
			if firstPM == "" {
				firstPM = Manager(part.Command)
				distro = pmInfo.Distro
			}

			// Only collect packages if this is an install command of the primary package manager
			if firstPM == Manager(part.Command) {
				// Check if this is an install command by finding the install keyword
				installKeywordIndex := -1
				for j, arg := range part.Args {
					if arg == pmInfo.InstallKeyword {
						installKeywordIndex = j
						break
					}
				}

				// If we found the install keyword, collect the packages
				if installKeywordIndex >= 0 {
					// Get packages after the install keyword and skip options
					for _, arg := range part.Args[installKeywordIndex+1:] {
						if !strings.HasPrefix(arg, "-") {
							packagesDetected = append(packagesDetected, arg)

							// Apply the package mapping if available
							if distroMap, exists := packageMap[distro]; exists && distroMap[arg] != nil {
								packagesToInstall = append(packagesToInstall, distroMap[arg]...)
							} else {
								packagesToInstall = append(packagesToInstall, arg)
							}
						}
					}
				}
			}
		}
	}

	// If we didn't find any package manager commands, return unmodified
	if !foundPMCommand {
		return false, "", "", nil, nil, shell
	}

	// Sort and deduplicate packages
	slices.Sort(packagesDetected)
	packagesDetected = slices.Compact(packagesDetected)
	slices.Sort(packagesToInstall)
	packagesToInstall = slices.Compact(packagesToInstall)

	// Process all shell parts again to handle specific conversions
	keepPart := make([]bool, len(shell.Parts))

	// First identify special commands and decide which parts to keep
	for i, part := range shell.Parts {
		// Assume we keep this part unless otherwise decided
		keepPart[i] = true

		// Handle known package manager commands
		switch Manager(part.Command) {
		case ManagerAptGet:
			// Handle apt-get update
			if contains(part.Args, "update") {
				keepPart[i] = false
			} else if contains(part.Args, "upgrade") {
				keepPart[i] = false
			} else if contains(part.Args, "install") {
				// If this is the first package manager we found, drop this command
				// as we'll add an apk add command later
				if firstPM == ManagerAptGet {
					keepPart[i] = false
				}
			}
		case ManagerApt:
			// Handle apt update
			if contains(part.Args, "update") {
				keepPart[i] = false
			} else if contains(part.Args, "upgrade") {
				keepPart[i] = false
			} else if contains(part.Args, "install") {
				// If this is the first package manager we found, drop this command
				// as we'll add an apk add command later
				if firstPM == ManagerApt {
					keepPart[i] = false
				}
			}
		case ManagerYum, ManagerDnf, ManagerMicrodnf:
			// Handle yum makecache
			if contains(part.Args, "makecache") {
				keepPart[i] = false
			} else if contains(part.Args, "update") {
				keepPart[i] = false
			} else if contains(part.Args, "install") {
				// If this is the first package manager we found, drop this command
				// as we'll add an apk add command later
				if firstPM == Manager(part.Command) {
					keepPart[i] = false
				}
			}
		case ManagerApk:
			// Handle apk
			if contains(part.Args, "update") {
				// Drop apk update as we'll add --no-cache to apk add
				keepPart[i] = false
			} else if contains(part.Args, "add") {
				// We'll create a new apk add command with all packages
				keepPart[i] = false
			}
		}
	}

	// Create a new command with only the parts we want to keep
	// plus the new apk add command at the beginning (if we have packages)
	finalParts := make([]*ShellPart, 0)

	// If we have packages to install, add the apk add command
	if len(packagesToInstall) > 0 {
		finalParts = append(finalParts, &ShellPart{
			Command: "apk",
			Args:    append([]string{"add", "--no-cache"}, packagesToInstall...),
		})
	}

	// Add the commands we're keeping
	for i, part := range shell.Parts {
		if keepPart[i] {
			newPart := cloneShellPart(part)
			finalParts = append(finalParts, newPart)
		}
	}

	// Set delimiters between commands
	for i := range finalParts {
		if i < len(finalParts)-1 {
			finalParts[i].Delimiter = "&&"
		} else {
			finalParts[i].Delimiter = ""
		}
	}

	// If no commands remain, add a no-op command
	if len(finalParts) == 0 {
		finalParts = append(finalParts, &ShellPart{
			Command:   "true",
			Args:      []string{},
			Delimiter: "",
		})
	}

	return true, distro, firstPM, packagesDetected, packagesToInstall, &ShellCommand{Parts: finalParts}
}

// Helper function to check if a slice contains a string
func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
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

// normalizeDockerHubImage normalizes Docker Hub image references
func normalizeDockerHubImage(imageRef string) string {
	// Remove any trailing slashes
	imageRef = strings.TrimRight(imageRef, "/")

	// Remove any prefix that is not a valid Docker Hub image name
	parts := strings.Split(imageRef, "/")
	for _, part := range parts {
		if strings.Contains(part, ".") || strings.Count(part, "/") > 1 {
			continue
		}
		return part
	}
	return imageRef
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

// handleTaggedMappedImage processes a mapped image with a tag and returns the full image reference
func handleTaggedMappedImage(mappedImage string, tag string, needsDevSuffix bool, opts Options) string {
	var targetImage, convertedTag string

	// Check if the mapped image includes a tag
	if parts := strings.Split(mappedImage, ":"); len(parts) > 1 {
		targetImage = parts[0]
		convertedTag = parts[1]
	} else {
		targetImage = mappedImage
		// If no tag in the mapping value, use the default tag logic
		convertedTag = calculateConvertedTag(targetImage, tag, false, needsDevSuffix)
	}

	// For direct matches where the key includes a tag, we use the mapping value as is
	// instead of applying the registry/org prefixes, unless it doesn't contain a slash
	if strings.Contains(targetImage, "/") {
		// The mapping specifies a fully qualified image with registry/repo
		if convertedTag != "" {
			return targetImage + ":" + convertedTag
		} else {
			return targetImage
		}
	} else {
		// Apply the standard formatting with registry/org
		return buildImageReference(targetImage, convertedTag, opts)
	}
}
