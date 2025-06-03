#!/usr/bin/env bash

# Copyright 2025 Chainguard, Inc.
# SPDX-License-Identifier: Apache-2.0


set -e

# Build the server
go build -o mcp-server-go

# Run the server
./mcp-server-go 