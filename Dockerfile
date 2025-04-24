# ---- Build Stage ----
    FROM golang:1.21-alpine AS builder

    # Set working directory
    WORKDIR /app
    
    # Copy go module files first for caching
    COPY go.mod go.sum ./
    
    # Download dependencies
    # Use RUN --mount for caching if desired, otherwise simple download
    # RUN --mount=type=cache,target=/go/pkg/mod go mod download
    RUN go mod download
    
    # Copy the rest of the source code
    COPY . .
    
    # Build the application using the correct path
    # Ensure the output binary is static for alpine (optional but recommended)
    RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/main ./agent/cmd/app
    
    # ---- Final Stage ----
    FROM alpine:latest
    
    # Set working directory
    WORKDIR /app
    
    # Copy only the built executable from the builder stage
    COPY --from=builder /app/main .
    
    # Expose the port the application listens on (from your env.go, likely 8080 or $PORT)
    # Note: Railway ignores this EXPOSE instruction for routing, but it's good practice.
    # EXPOSE 8080
    
    # Command to run the executable
    # Railway will inject the $PORT variable here if needed by your app
    CMD ["./main"]