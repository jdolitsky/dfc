/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"encoding/json"
	"log"
	"os"
)

// MCPRequest defines the structure for an MCP request.
// Note: Using os.ReadFile and os.WriteFile instead of ioutil for modern Go.
// Also, ensuring relative paths for test files are handled correctly from the new location.

type MCPRequest struct {
	Jsonrpc string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Method  string    `json:"method"`
	Params  MCPParams `json:"params"`
}

// MCPParams defines the parameters for an MCP request.
type MCPParams struct {
	Name      string      `json:"name"`
	Arguments interface{} `json:"arguments"`
}

// ConvertArguments defines the arguments for the convert_dockerfile tool.
type ConvertArguments struct {
	DockerfileContent string `json:"dockerfile_content"`
	Organization      string `json:"organization"`
}

// AnalyzeArguments defines the arguments for the analyze_dockerfile tool.
type AnalyzeArguments struct {
	DockerfileContent string `json:"dockerfile_content"`
}

func main() {
	// Adjusted path to be relative to cmd/generatejson/
	dockerfileContent, err := os.ReadFile("../test-dockerfile.txt")
	if err != nil {
		log.Fatalf("Failed to read test-dockerfile.txt: %v", err)
	}

	// Convert dockerfile request
	convertRequest := MCPRequest{
		Jsonrpc: "2.0",
		ID:      "test-1",
		Method:  "mcp.call_tool",
		Params: MCPParams{
			Name: "convert_dockerfile",
			Arguments: ConvertArguments{
				DockerfileContent: string(dockerfileContent),
				Organization:      "example",
			},
		},
	}
	// Adjusted path to be relative to cmd/generatejson/
	writeRequestToFile("../test-request.json", convertRequest)

	// Analyze dockerfile request
	analyzeRequest := MCPRequest{
		Jsonrpc: "2.0",
		ID:      "test-2",
		Method:  "mcp.call_tool",
		Params: MCPParams{
			Name: "analyze_dockerfile",
			Arguments: AnalyzeArguments{
				DockerfileContent: string(dockerfileContent),
			},
		},
	}
	// Adjusted path to be relative to cmd/generatejson/
	writeRequestToFile("../analyze-request.json", analyzeRequest)

	// Healthcheck request
	healthcheckRequest := MCPRequest{
		Jsonrpc: "2.0",
		ID:      "test-3",
		Method:  "mcp.call_tool",
		Params: MCPParams{
			Name:      "healthcheck",
			Arguments: struct{}{}, // Empty arguments
		},
	}
	// Adjusted path to be relative to cmd/generatejson/
	writeRequestToFile("../healthcheck-request.json", healthcheckRequest)

	log.Println("Successfully generated JSON request files.")
}

func writeRequestToFile(filename string, request MCPRequest) {
	file, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON for %s: %v", filename, err)
	}

	err = os.WriteFile(filename, file, 0644)
	if err != nil {
		log.Fatalf("Failed to write %s: %v", filename, err)
	}
}
