# ---- Build Stage ----
# Use a Go version compatible with your go.mod requirements (e.g., 1.23)
FROM golang:1.23-alpine AS builder

# Set working directory inside the container
WORKDIR /app

# Copy go module files first to leverage Docker cache
COPY go.mod go.sum ./

# Download dependencies
# Consider using RUN --mount=type=cache for faster subsequent builds if needed
RUN go mod download

# Copy the entire project source code into the container
COPY . .

# Build the Go application statically
# - Use the correct path to your main package: ./agent/cmd/app
# - Output the executable to /app/main inside the builder stage
# - Use ldflags to create a smaller binary (optional but good practice)
# - CGO_ENABLED=0 and GOOS=linux ensure compatibility with alpine base
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/main ./agent/cmd/app

# ---- Final Stage ----
# Use a minimal alpine image for the final container
FROM alpine:latest

# Set working directory inside the final container
WORKDIR /app

# Copy only the built executable from the builder stage into the final image
COPY --from=builder /app/main .

# Copy the migrations directory from the builder stage to the final image
# This ensures the migrate library can find the .sql files at runtime
COPY --from=builder /app/agent/database/migrations ./agent/database/migrations

# Command to run the executable when the container starts
# The application binary is now named 'main' and is in the current directory '/app'
CMD ["./main"]