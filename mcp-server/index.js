const express = require('express');
const bodyParser = require('body-parser');
const { z } = require('zod');

const app = express();
const PORT = process.env.PORT || 3000;

// Middleware
app.use(bodyParser.json());

// MCP Server implementation
class MCPServer {
  constructor() {
    this.tools = {};
  }

  tool(name, description, schema, handler) {
    this.tools[name] = { name, description, schema, handler };
    return this;
  }

  registerRoutes(app) {
    // Register all tools as API endpoints
    for (const [name, tool] of Object.entries(this.tools)) {
      app.post(`/api/tools/${name}`, async (req, res) => {
        try {
          const params = req.body;
          const result = await tool.handler(params);
          res.json(result);
        } catch (error) {
          console.error(`Error in ${name} tool:`, error);
          res.status(500).json({
            content: [{ type: 'text', text: `Error: ${error.message}` }],
            isError: true
          });
        }
      });
    }

    // Register a GET endpoint to list available tools
    app.get('/api/tools', (req, res) => {
      const toolList = Object.entries(this.tools).map(([name, tool]) => ({
        name,
        description: tool.description
      }));
      res.json({ tools: toolList });
    });
  }
}

// Create MCP server instance
const server = new MCPServer();

// Register the Dockerfile converter tool
server.tool(
  'convert_dockerfile',
  'Convert a Dockerfile to use Chainguard Images and APKs in FROM and RUN lines',
  z.object({
    dockerfile_content: z.string().describe("The content of the Dockerfile to convert"),
    organization: z.string().optional().describe("The Chainguard organization to use (defaults to 'ORG')"),
    registry: z.string().optional().describe("Alternative registry to use instead of cgr.dev")
  }),
  async (params) => {
    /** Convert a Dockerfile to use Chainguard Images and APKs in FROM and RUN lines */
    try {
      const dockerfileContent = params.dockerfile_content;
      const organization = params.organization || 'ORG';
      const registry = params.registry;

      // Validate input
      if (!dockerfileContent || dockerfileContent.trim() === '') {
        return {
          content: [{ type: 'text', text: 'Error: Dockerfile content cannot be empty' }],
          isError: true
        };
      }

      // Process the Dockerfile
      const convertedDockerfile = await convertDockerfile(dockerfileContent, organization, registry);
      
      return {
        content: [
          { 
            type: 'text', 
            text: convertedDockerfile
          }
        ]
      };
    } catch (error) {
      console.error('Error in convert_dockerfile tool:', error);
      return {
        content: [{ type: 'text', text: `Error converting Dockerfile: ${error.message}` }],
        isError: true
      };
    }
  }
);

/**
 * Convert a Dockerfile to use Chainguard Images and APKs
 * @param {string} dockerfileContent - The content of the Dockerfile to convert
 * @param {string} organization - The Chainguard organization to use
 * @param {string|undefined} registry - Alternative registry to use instead of cgr.dev
 * @returns {Promise<string>} - The converted Dockerfile content
 */
async function convertDockerfile(dockerfileContent, organization, registry) {
  // Parse the Dockerfile
  const dockerfile = parseDockerfile(dockerfileContent);
  
  // Convert the Dockerfile
  const convertedDockerfile = convertParsedDockerfile(dockerfile, organization, registry);
  
  return convertedDockerfile;
}

/**
 * Parse a Dockerfile into a structured format
 * @param {string} content - The Dockerfile content
 * @returns {Object} - The parsed Dockerfile
 */
function parseDockerfile(content) {
  const lines = content.split('\n');
  const stages = [];
  let currentStage = { lines: [], hasRun: false };
  
  lines.forEach(line => {
    const trimmedLine = line.trim();
    
    // Check for FROM line to identify stages
    if (trimmedLine.startsWith('FROM ')) {
      if (currentStage.lines.length > 0) {
        stages.push(currentStage);
      }
      currentStage = { lines: [line], hasRun: false };
    } else {
      currentStage.lines.push(line);
      
      // Check if this stage has RUN commands
      if (trimmedLine.startsWith('RUN ')) {
        currentStage.hasRun = true;
      }
    }
  });
  
  // Add the last stage
  if (currentStage.lines.length > 0) {
    stages.push(currentStage);
  }
  
  return { stages };
}

/**
 * Convert a parsed Dockerfile to use Chainguard Images and APKs
 * @param {Object} dockerfile - The parsed Dockerfile
 * @param {string} organization - The Chainguard organization
 * @param {string|undefined} registry - Alternative registry
 * @returns {string} - The converted Dockerfile
 */
function convertParsedDockerfile(dockerfile, organization, registry) {
  const convertedStages = dockerfile.stages.map(stage => {
    const convertedLines = [];
    let fromLineConverted = false;
    let userRootAdded = false;
    
    for (let i = 0; i < stage.lines.length; i++) {
      const line = stage.lines[i];
      const trimmedLine = line.trim();
      
      // Convert FROM line
      if (trimmedLine.startsWith('FROM ')) {
        const convertedFromLine = convertFromLine(trimmedLine, stage.hasRun, organization, registry);
        convertedLines.push(convertedFromLine);
        fromLineConverted = true;
      }
      // Convert RUN line with package manager commands
      else if (trimmedLine.startsWith('RUN ')) {
        // Add USER root if we're going to convert package manager commands and haven't added it yet
        if (fromLineConverted && !userRootAdded && hasPackageManagerCommand(trimmedLine)) {
          convertedLines.push('USER root');
          userRootAdded = true;
        }
        
        const convertedRunLine = convertRunLine(trimmedLine);
        convertedLines.push(convertedRunLine);
      }
      // Keep other lines as is
      else {
        convertedLines.push(line);
      }
    }
    
    return convertedLines;
  });
  
  // Flatten the stages back into a single string
  return convertedStages.flat().join('\n');
}

/**
 * Convert a FROM line to use Chainguard Images
 * @param {string} fromLine - The FROM line
 * @param {boolean} stageHasRun - Whether the stage has RUN commands
 * @param {string} organization - The Chainguard organization
 * @param {string|undefined} registry - Alternative registry
 * @returns {string} - The converted FROM line
 */
function convertFromLine(fromLine, stageHasRun, organization, registry) {
  // Extract the base image and tag
  const fromParts = fromLine.substring(5).trim().split(' ');
  const imageRef = fromParts[0];
  
  // Handle named stages (FROM image AS name)
  const stageName = fromParts.length > 1 && fromParts[1].toUpperCase() === 'AS' ? 
    ` ${fromParts[1]} ${fromParts[2]}` : '';
  
  // Split image reference into parts
  const [imageName, tag] = imageRef.split(':');
  
  // Map common base images to Chainguard equivalents
  const imageMapping = {
    'node': 'node',
    'nodejs': 'node',
    'python': 'python',
    'golang': 'go',
    'go': 'go',
    'java': 'jdk',
    'openjdk': 'jdk',
    'nginx': 'nginx',
    'ubuntu': 'chainguard-base',
    'debian': 'chainguard-base',
    'alpine': 'chainguard-base',
    'busybox': 'chainguard-base',
    'php': 'php',
    'ruby': 'ruby'
  };
  
  // Normalize image name (remove docker.io/library/ prefix)
  const normalizedImageName = imageName
    .replace(/^docker\.io\/library\//, '')
    .replace(/^index\.docker\.io\/library\//, '')
    .replace(/^library\//, '');
  
  // Check if we have a mapping for this image
  const mappedImage = imageMapping[normalizedImageName] || normalizedImageName;
  
  // Determine the registry prefix
  const registryPrefix = registry || `cgr.dev/${organization}`;
  
  // Special case for chainguard-base
  if (mappedImage === 'chainguard-base') {
    return `FROM ${registryPrefix}/${mappedImage}:latest${stageName}`;
  }
  
  // Determine the appropriate tag
  let newTag;
  
  if (!tag) {
    // No tag specified
    newTag = stageHasRun ? 'latest-dev' : 'latest';
  } else if (tag.includes('$')) {
    // Tag contains variables
    newTag = stageHasRun ? `${tag}-dev` : tag;
  } else if (/^v?\d+(\.\d+)*$/.test(tag)) {
    // Semantic version tag
    // Remove 'v' prefix if present
    let semverTag = tag.startsWith('v') ? tag.substring(1) : tag;
    // Truncate to major.minor
    const parts = semverTag.split('.');
    semverTag = parts.slice(0, 2).join('.');
    // Add -dev suffix if needed
    newTag = stageHasRun ? `${semverTag}-dev` : semverTag;
  } else {
    // Non-semver tag
    newTag = stageHasRun ? 'latest-dev' : 'latest';
  }
  
  return `FROM ${registryPrefix}/${mappedImage}:${newTag}${stageName}`;
}

/**
 * Convert a RUN line to use APK package manager
 * @param {string} runLine - The RUN line
 * @returns {string} - The converted RUN line
 */
function convertRunLine(runLine) {
  const command = runLine.substring(4).trim();
  
  // Check for package manager commands
  if (hasAptGetInstall(command)) {
    return convertAptGetInstall(runLine);
  } else if (hasYumInstall(command)) {
    return convertYumInstall(runLine);
  } else if (hasUserAddCommand(command)) {
    return convertUserAddCommand(runLine);
  } else if (hasTarCommand(command)) {
    return convertTarCommand(runLine);
  }
  
  // If no conversion needed, return the original line
  return runLine;
}

/**
 * Check if a command contains any package manager command
 * @param {string} command - The command to check
 * @returns {boolean} - Whether the command contains a package manager command
 */
function hasPackageManagerCommand(command) {
  return hasAptGetInstall(command) || hasYumInstall(command);
}

/**
 * Check if a command contains apt-get install
 * @param {string} command - The command to check
 * @returns {boolean} - Whether the command contains apt-get install
 */
function hasAptGetInstall(command) {
  return command.includes('apt-get install') || command.includes('apt install');
}

/**
 * Check if a command contains yum/dnf install
 * @param {string} command - The command to check
 * @returns {boolean} - Whether the command contains yum/dnf install
 */
function hasYumInstall(command) {
  return command.includes('yum install') || 
         command.includes('dnf install') || 
         command.includes('microdnf install');
}

/**
 * Check if a command contains useradd/groupadd
 * @param {string} command - The command to check
 * @returns {boolean} - Whether the command contains useradd/groupadd
 */
function hasUserAddCommand(command) {
  return command.includes('useradd') || command.includes('groupadd');
}

/**
 * Check if a command contains tar with GNU syntax
 * @param {string} command - The command to check
 * @returns {boolean} - Whether the command contains tar with GNU syntax
 */
function hasTarCommand(command) {
  return command.includes('tar ') && 
         (command.includes(' --no-same-owner') || 
          command.includes(' --no-same-permissions'));
}

/**
 * Convert apt-get install command to apk add
 * @param {string} runLine - The RUN line with apt-get install
 * @returns {string} - The converted RUN line with apk add
 */
function convertAptGetInstall(runLine) {
  const command = runLine.substring(4).trim();
  
  // Extract package names
  const packageRegex = /(?:apt-get|apt)\s+install\s+(?:-y\s+)?([^&|;]+)/;
  const match = packageRegex.exec(command);
  
  if (match && match[1]) {
    // Extract packages, removing options like -y
    const packagesStr = match[1].trim();
    const packages = packagesStr.split(/\s+/)
      .filter(pkg => !pkg.startsWith('-') && pkg !== '\\');
    
    // Map common packages to their Alpine equivalents
    const packageMapping = {
      'curl': 'curl',
      'wget': 'wget',
      'git': 'git',
      'nano': 'nano',
      'vim': 'vim',
      'python3': 'python3',
      'python': 'python3',
      'nodejs': 'nodejs',
      'npm': 'npm',
      'build-essential': 'build-base',
      'ca-certificates': 'ca-certificates'
    };
    
    // Map packages to their Alpine equivalents
    const mappedPackages = packages.map(pkg => packageMapping[pkg] || pkg);
    
    // Create the new apk add command
    return `RUN apk add --no-cache ${mappedPackages.join(' ')}`;
  }
  
  // If we couldn't parse the packages, return the original line
  return runLine;
}

/**
 * Convert yum/dnf install command to apk add
 * @param {string} runLine - The RUN line with yum/dnf install
 * @returns {string} - The converted RUN line with apk add
 */
function convertYumInstall(runLine) {
  const command = runLine.substring(4).trim();
  
  // Extract package names
  const packageRegex = /(?:yum|dnf|microdnf)\s+install\s+(?:-y\s+)?([^&|;]+)/;
  const match = packageRegex.exec(command);
  
  if (match && match[1]) {
    // Extract packages, removing options like -y
    const packagesStr = match[1].trim();
    const packages = packagesStr.split(/\s+/)
      .filter(pkg => !pkg.startsWith('-') && pkg !== '\\');
    
    // Map common packages to their Alpine equivalents (same mapping as apt-get)
    const packageMapping = {
      'curl': 'curl',
      'wget': 'wget',
      'git': 'git',
      'nano': 'nano',
      'vim': 'vim',
      'python3': 'python3',
      'python': 'python3',
      'nodejs': 'nodejs',
      'npm': 'npm',
      'gcc': 'gcc',
      'make': 'make',
      'ca-certificates': 'ca-certificates'
    };
    
    // Map packages to their Alpine equivalents
    const mappedPackages = packages.map(pkg => packageMapping[pkg] || pkg);
    
    // Create the new apk add command
    return `RUN apk add --no-cache ${mappedPackages.join(' ')}`;
  }
  
  // If we couldn't parse the packages, return the original line
  return runLine;
}

/**
 * Convert useradd/groupadd commands to adduser/addgroup
 * @param {string} runLine - The RUN line with useradd/groupadd
 * @returns {string} - The converted RUN line with adduser/addgroup
 */
function convertUserAddCommand(runLine) {
  let command = runLine.substring(4).trim();
  
  // Convert useradd to adduser
  if (command.includes('useradd')) {
    // Simple conversion for common useradd options
    command = command
      .replace(/useradd\s+-u\s+(\d+)\s+-g\s+(\d+)\s+-s\s+([^\s]+)\s+([^\s]+)/, 
               'adduser -u $1 -g $2 -s $3 -D $4')
      .replace(/useradd\s+-u\s+(\d+)\s+([^\s]+)/, 
               'adduser -u $1 -D $2')
      .replace(/useradd\s+([^\s]+)/, 
               'adduser -D $1');
  }
  
  // Convert groupadd to addgroup
  if (command.includes('groupadd')) {
    // Simple conversion for common groupadd options
    command = command
      .replace(/groupadd\s+-g\s+(\d+)\s+([^\s]+)/, 
               'addgroup -g $1 $2')
      .replace(/groupadd\s+([^\s]+)/, 
               'addgroup $1');
  }
  
  return `RUN ${command}`;
}

/**
 * Convert tar commands with GNU syntax to busybox syntax
 * @param {string} runLine - The RUN line with tar command
 * @returns {string} - The converted RUN line with busybox tar syntax
 */
function convertTarCommand(runLine) {
  let command = runLine.substring(4).trim();
  
  // Convert GNU tar options to busybox tar options
  command = command
    .replace(/tar\s+--no-same-owner/, 'tar')
    .replace(/tar\s+--no-same-permissions/, 'tar');
  
  return `RUN ${command}`;
}

// Register routes
server.registerRoutes(app);

// Start the server
app.listen(PORT, () => {
  console.log(`MCP Server running on port ${PORT}`);
  console.log(`Available tools: ${Object.keys(server.tools).join(', ')}`);
}); 