#!/bin/bash
# =============================================================================
# Sub2API Docker Deployment Preparation Script
# =============================================================================
# Prepares .env and data directories in this repository's deploy/ folder.
# The application image is built from source on the server; this script does
# not download compose files or pull weishaw/sub2api.
#
# Usage (from a git checkout):
#   ./deploy/docker-deploy.sh
#   cd deploy
#   docker compose -f docker-compose.local.yml up -d --build
# =============================================================================

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

generate_secret() {
    openssl rand -hex 32
}

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

main() {
    echo ""
    echo "=========================================="
    echo "  Sub2API Deployment Preparation"
    echo "=========================================="
    echo ""

    SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
    REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"

    if [ ! -f "$REPO_ROOT/Dockerfile" ] || [ ! -f "$SCRIPT_DIR/docker-compose.local.yml" ]; then
        print_error "This script must run from a checkout of this repository."
        print_error "Clone the repo, then run: ./deploy/docker-deploy.sh"
        exit 1
    fi

    cd "$SCRIPT_DIR"

    if ! command_exists openssl; then
        print_error "openssl is not installed. Please install openssl first."
        exit 1
    fi

    if [ ! -f ".env.example" ]; then
        print_error "Missing .env.example in $SCRIPT_DIR"
        exit 1
    fi

    if [ -f ".env" ]; then
        print_warning "An .env file already exists in $SCRIPT_DIR"
        read -p "Overwrite existing .env? (y/N): " -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            print_info "Cancelled."
            exit 0
        fi
    fi

    print_info "Generating secure secrets..."
    echo ""

    JWT_SECRET=$(generate_secret)
    TOTP_ENCRYPTION_KEY=$(generate_secret)
    POSTGRES_PASSWORD=$(generate_secret)

    cp .env.example .env

    if sed --version >/dev/null 2>&1; then
        sed -i "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
    else
        sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=${JWT_SECRET}/" .env
        sed -i '' "s/^TOTP_ENCRYPTION_KEY=.*/TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}/" .env
        sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=${POSTGRES_PASSWORD}/" .env
    fi

    print_info "Creating data directories..."
    mkdir -p data postgres_data redis_data
    print_success "Created data directories"

    chmod 600 .env
    echo ""

    echo "=========================================="
    echo "  Preparation Complete!"
    echo "=========================================="
    echo ""
    echo "Generated secure credentials:"
    echo "  POSTGRES_PASSWORD:     ${POSTGRES_PASSWORD}"
    echo "  JWT_SECRET:            ${JWT_SECRET}"
    echo "  TOTP_ENCRYPTION_KEY:   ${TOTP_ENCRYPTION_KEY}"
    echo ""
    print_warning "These credentials have been saved to deploy/.env."
    print_warning "Please keep them secure and do not share publicly!"
    echo ""
    echo "Working directory: $SCRIPT_DIR"
    echo "  docker-compose.local.yml  - Compose file (builds image from repo root)"
    echo "  .env                      - Environment variables (generated secrets)"
    echo "  data/                     - Application data"
    echo "  postgres_data/            - PostgreSQL data"
    echo "  redis_data/               - Redis data"
    echo ""
    echo "Next steps:"
    echo "  1. (Optional) Edit deploy/.env to customize configuration"
    echo "  2. Build the image from source and start services:"
    echo "     cd \"$SCRIPT_DIR\""
    echo "     docker compose -f docker-compose.local.yml up -d --build"
    echo ""
    echo "  3. View logs:"
    echo "     docker compose -f docker-compose.local.yml logs -f sub2api"
    echo ""
    echo "  4. Access Web UI:"
    echo "     http://localhost:8080"
    echo ""
    echo "  Upgrade later:"
    echo "     git pull"
    echo "     docker compose -f docker-compose.local.yml up -d --build"
    echo ""
    print_info "The first build compiles the frontend and Go binary; it can take several minutes."
    print_info "If admin password is not set in .env, it will be auto-generated."
    print_info "Check logs for the generated admin password on first startup."
    echo ""
}

main "$@"
