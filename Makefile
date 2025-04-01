# Makefile for Flexible Single-Agent Setup with Dynamic Support for More Agents

.PHONY: run build test docker-build docker-run clean create-database launch-database clean-database

# Default target
all: build

# Run the default agent or a specified agent
run:
	@if [ -z "$(AGENT)" ]; then \
		echo "Running default agent (ca-scraper)..."; \
		go run ./agent/cmd/app/main.go; \
	elif [ "$(AGENT)" = "all" ]; then \
		echo "Running all agents..."; \
		go run ./agent/cmd/app/main.go & \
		wait; \
	else \
		echo "Running agent: $(AGENT)..."; \
		go run ./$(AGENT)/cmd/app/main.go; \
	fi

# Build the default agent or a specified agent
build:
	@if [ -z "$(AGENT)" ]; then \
		echo "Building default agent (ca-scraper)..."; \
		go build -o bin/ca-scraper ./agent/cmd/app/main.go; \
	else \
		echo "Building agent: $(AGENT)..."; \
		go build -o bin/$(AGENT) ./$(AGENT)/cmd/app/main.go; \
	fi

# Run tests for the default agent or a specified agent
test:
	@if [ -z "$(AGENT)" ]; then \
		echo "Running tests for default agent (ca-scraper)..."; \
		go test ./agent/...; \
	else \
		echo "Running tests for agent: $(AGENT)..."; \
		go test ./$(AGENT)/...; \
	fi

# Build Docker image for the default agent or a specified agent
docker-build:
	@if [ -z "$(AGENT)" ]; then \
		echo "Building Docker image for default agent (ca-scraper)..."; \
		docker build -t ca-scraper -f agent/Dockerfile ./agent; \
	else \
		echo "Building Docker image for agent: $(AGENT)..."; \
		docker build -t $(AGENT) -f $(AGENT)/Dockerfile ./$(AGENT); \
	fi

# Run Docker container for the default agent or a specified agent
docker-run:
	@if [ -z "$(AGENT)" ]; then \
		echo "Running Docker container for default agent (ca-scraper)..."; \
		docker run -d -p 8080:8080 --name ca-scraper ca-scraper; \
	else \
		echo "Running Docker container for agent: $(AGENT)..."; \
		docker run -d -p 8080:8080 --name $(AGENT) $(AGENT); \
	fi

# Database Commands
create-database:
	@if [ -z "$(DATABASE)" ] || [ -z "$(USER)" ] || [ -z "$(PASSWORD)" ]; then \
		echo "Error: DATABASE, USER, and PASSWORD variables are required (e.g., make create-database DATABASE=mydb USER=myuser PASSWORD=mypassword)"; \
		exit 1; \
	fi; \
	echo "Creating database: $(DATABASE)..."; \
	psql -c "CREATE DATABASE $(DATABASE);" || echo "Database $(DATABASE) already exists."; \
	echo "Creating user: $(USER)..."; \
	psql -c "CREATE USER $(USER) WITH PASSWORD '$(PASSWORD)';" || echo "User $(USER) already exists."; \
	echo "Granting privileges to $(USER) on $(DATABASE)..."; \
	psql -c "GRANT ALL PRIVILEGES ON DATABASE $(DATABASE) TO $(USER);"; \
	echo "Database setup complete."

launch-database:
	@if [ -z "$(DATABASE)" ] || [ -z "$(USER)" ]; then \
		echo "Error: DATABASE and USER variables are required (e.g., make launch-database DATABASE=mydb USER=myuser)"; \
		exit 1; \
	fi; \
	echo "Launching database session for $(DATABASE) as $(USER)..."; \
	psql -U $(USER) -d $(DATABASE)

clean-database:
	@if [ -z "$(DATABASE)" ] || [ -z "$(USER)" ]; then \
		echo "Error: DATABASE and USER variables are required (e.g., make clean-database DATABASE=mydb USER=myuser)"; \
		exit 1; \
	fi; \
	echo "Dropping database $(DATABASE)..."; \
	psql -c "DROP DATABASE IF EXISTS $(DATABASE);"; \
	echo "Dropping user $(USER)..."; \
	psql -c "DROP USER IF EXISTS $(USER);"; \
	echo "Database and user cleanup complete."

# Clean up build artifacts and Docker containers/images
clean:
	@echo "Cleaning up build artifacts..."
	rm -rf bin/
	docker rm -f ca-scraper || true
	docker rmi -f ca-scraper || true
	go clean
