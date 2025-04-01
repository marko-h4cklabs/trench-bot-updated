# Dockerfile for Single-Agent Setup with Dynamic Support for Multiple Agents

# Step 1: Build stage using Go
FROM golang:1.20 as builder
WORKDIR /app

# Copy the entire project directory
COPY . .

# Install dependencies
RUN go mod tidy

# Build the specified agent (default: ca-scraper)
ARG AGENT=ca-scraper
RUN echo "Building agent: $AGENT..." && \
    go build -o /app/$AGENT ./agent/cmd/app/main.go

# Step 2: Minimal runtime stage
FROM alpine:latest
WORKDIR /root/

# Copy built binary from the builder stage
ARG AGENT=ca-scraper
COPY --from=builder /app/$AGENT ./agent

# Copy the environment file for the agent
COPY agent/.env agent.env

# Expose the default port for the agent
EXPOSE 8080

# Default command to run the agent
CMD ./agent
