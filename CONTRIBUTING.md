# Contributing

## Setup for Development

### Prerequisites
- Go 1.22 or later
- pre-commit installed (`pip install pre-commit` or `brew install pre-commit`)

### Initial Setup
1. Clone the repository:
   ```bash
   git clone https://github.com/elisiariocouto/specular.git
   cd specular
   ```

2. Install dependencies:
   ```bash
   make deps
   ```

3. Install pre-commit hooks:
   ```bash
   pre-commit install
   ```

## Building

```bash
make build
```

This creates the binary at `bin/specular`.

## Running Locally

```bash
# Set up cache directory
mkdir -p /tmp/specular-cache

# Configure environment variables
export SPECULAR_PORT=8080
export SPECULAR_HOST=0.0.0.0
export SPECULAR_CACHE_DIR=/tmp/specular-cache
export SPECULAR_BASE_URL=http://localhost:8080

# Run the server
make run
```

Then visit `http://localhost:8080/health` to verify the server is running.

## Testing

### Running Tests
```bash
make test
```

### Running Tests with Coverage
```bash
make test-coverage
```

Coverage report is generated as `coverage.html`.

## Code Quality

### Formatting Code
```bash
make fmt
```

### Linting
```bash
make lint
```

### Pre-commit Hooks

Pre-commit hooks will automatically run before each commit to:
- Format code with `go fmt` and `goimports`
- Run linters with `go vet` and `staticcheck`
- Run tests to ensure nothing is broken

If pre-commit fails, the commit is canceled. Fix the issues and try again.

To run pre-commit manually on all files:
```bash
pre-commit run --all-files
```

To skip pre-commit hooks (not recommended):
```bash
git commit --no-verify
```

## Release Process

### Creating a Release

1. Run the release script:
   ```bash
   ./scripts/release.sh
   ```

2. The script will:
   - Calculate the next CalVer version (YYYY.M.MICRO)
   - Update CHANGELOG.md with git-cliff
   - Create a git tag and commit
   - Prompt for confirmation before pushing

3. Confirm and push:
   ```
   Are you sure you want to push the changes and tags to the remote repository? (y/n)
   ```

4. GitHub Actions will automatically:
   - Build binaries for multiple platforms (linux/darwin/windows × amd64/arm64)
   - Push Docker images to Docker Hub and GitHub Container Registry
   - Create a GitHub Release with binaries and changelog

### Commit Messages

Follow conventional commits format:
```
type(scope): Description starting with uppercase and ending with period.
```

Supported types: `feat`, `fix`, `refactor`, `docs`, `perf`, `test`, `chore`

Examples:
- `feat: Add S3 storage backend.`
- `fix: Resolve cache invalidation race condition.`
- `docs: Update configuration examples.`

## Docker Development

### Build Docker Image
```bash
make docker-build
```

### Run Docker Container
```bash
make docker-run
```

This mounts a local cache directory and binds to port 8080.

## Architecture Overview

The mirror consists of several layers:

- **HTTP Server** - Handles requests and routing
- **Mirror Service** - Core cache-or-fetch business logic
- **Storage Layer** - Abstract interface with filesystem implementation
- **Upstream Client** - Fetches from provider registries with retry logic
- **Observability** - Prometheus metrics and structured logging

For detailed architecture information, see [AGENTS.md](AGENTS.md#architecture).
