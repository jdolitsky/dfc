#!/bin/bash

# Start the Dockerfile Converter MCP Server
echo "Starting Dockerfile Converter MCP Server..."

# Install dependencies if node_modules directory doesn't exist
if [ ! -d "node_modules" ]; then
  echo "Installing dependencies..."
  npm install
fi

# Start the server
echo "Starting server on port 3000..."
node index.js 