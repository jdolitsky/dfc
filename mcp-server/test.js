const fs = require('fs');
const http = require('http');

// Sample Dockerfile to convert
const dockerfileContent = `FROM node:16
WORKDIR /app
COPY package*.json ./
RUN apt-get update && apt-get install -y git curl
RUN npm install
COPY . .
CMD ["npm", "start"]`;

// Test function
async function testDockerfileConverter() {
  console.log('Testing Dockerfile Converter MCP Server...');
  console.log('\nOriginal Dockerfile:');
  console.log(dockerfileContent);
  
  // Prepare request data
  const postData = JSON.stringify({
    dockerfile_content: dockerfileContent,
    organization: 'chainguard'
  });
  
  // Request options
  const options = {
    hostname: 'localhost',
    port: 3000,
    path: '/api/tools/convert_dockerfile',
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Content-Length': Buffer.byteLength(postData)
    }
  };
  
  // Send request
  return new Promise((resolve, reject) => {
    const req = http.request(options, (res) => {
      let data = '';
      
      res.on('data', (chunk) => {
        data += chunk;
      });
      
      res.on('end', () => {
        if (res.statusCode !== 200) {
          console.error(`Error: Received status code ${res.statusCode}`);
          reject(new Error(`Request failed with status code ${res.statusCode}`));
          return;
        }
        
        try {
          const response = JSON.parse(data);
          console.log('\nConverted Dockerfile:');
          console.log(response.content[0].text);
          resolve();
        } catch (error) {
          console.error('Error parsing response:', error);
          reject(error);
        }
      });
    });
    
    req.on('error', (error) => {
      console.error('Error making request:', error);
      reject(error);
    });
    
    req.write(postData);
    req.end();
  });
}

// Check if the server is already running
async function isServerRunning() {
  return new Promise((resolve) => {
    const req = http.request({
      hostname: 'localhost',
      port: 3000,
      path: '/api/tools',
      method: 'GET'
    }, (res) => {
      resolve(res.statusCode === 200);
    });
    
    req.on('error', () => {
      resolve(false);
    });
    
    req.end();
  });
}

// Main function
async function main() {
  const serverRunning = await isServerRunning();
  
  if (serverRunning) {
    console.log('MCP Server is running. Testing Dockerfile conversion...');
    
    try {
      await testDockerfileConverter();
      console.log('\nTest completed successfully!');
    } catch (error) {
      console.error('Test failed:', error);
      process.exit(1);
    }
  } else {
    console.error('Error: MCP Server is not running. Please start the server with "npm start" first.');
    process.exit(1);
  }
}

main(); 