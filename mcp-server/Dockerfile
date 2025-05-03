FROM cgr.dev/chainguard/nodejs:latest

WORKDIR /app

# Copy package files and install dependencies
COPY package*.json ./
RUN npm install

# Copy application code
COPY . .

# Expose the port
EXPOSE 3000

# Start the server
CMD ["node", "index.js"] 