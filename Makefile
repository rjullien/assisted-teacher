.PHONY: test test-backend test-frontend lint build clean

# Run all tests
test: test-backend test-frontend

# Backend Go tests
test-backend:
	@echo "=== Backend Go tests ==="
	cd backend && go test ./... -v -count=1 -timeout=30s

# Frontend React tests
test-frontend:
	@echo "=== Frontend React tests ==="
	cd frontend && npx vitest --run

# Lint (optional)
lint:
	@echo "=== Backend lint ==="
	cd backend && golangci-lint run ./...

# Build
build:
	@echo "=== Building frontend ==="
	cd frontend && npm run build
	@echo "=== Building backend ==="
	cd backend && go build -o ../bin/cours-ia .

# Clean
clean:
	rm -rf bin/
	rm -rf backend/static/*
	rm -rf frontend/node_modules/

# Docker
docker:
	docker compose build

# Run mock agent for dev
mock-agent:
	cd backend && go build -o ../bin/mock-acp-agent ./internal/bridge/mockagent
	@echo "Mock agent built: bin/mock-acp-agent"
	@echo "Run with: ACP_AGENT_CMD=./bin/mock-acp-agent go run ./backend"
