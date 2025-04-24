# ---- Build Stage ----
# vvv CHANGE THIS LINE vvv
FROM golang:1.23-alpine AS builder
# ^^^ CHANGE THIS LINE ^^^

# Set working directory
WORKDIR /app

# Copy go module files first for caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application using the correct path
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/main ./agent/cmd/app

# ---- Final Stage ----
FROM alpine:latest

# Set working directory
WORKDIR /app

# Copy only the built executable from the builder stage
COPY --from=builder /app/main .

# Command to run the executable
CMD ["./main"]