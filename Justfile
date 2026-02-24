set dotenv-load := true

default:
  @just --list

# Flox Postgres service (optional)
pg-start:
  flox services start postgres

pg-stop:
  flox services stop postgres

pg-status:
  flox services status

# Backend
api:
  cd backend && go run ./cmd/api

migrate-up:
  cd backend && go run ./cmd/migrate up

migrate-down:
  cd backend && go run ./cmd/migrate down

migrate-status:
  cd backend && go run ./cmd/migrate status

# Frontend
ui:
  cd frontend && pnpm install && pnpm dev

# Docker (matches README path)
compose-up:
  docker compose up --build

# Demo: intentionally "big" build (for Depot demos).
# Usage:
#   DEMO_BIG_N_GO=20000 DEMO_BIG_N_TS=60000 just demo-big
demo-big:
  @echo "Generating demo-big artifacts and running heavier builds"
  cd backend && DEMO_BIG_N={{env_var_or_default("DEMO_BIG_N_GO","20000")}} go run ./tools/demobiggen -out internal/demobig/generated.go
  cd backend && go build ./cmd/api ./cmd/migrate ./cmd/worker ./cmd/report
  cd frontend && corepack enable && corepack prepare pnpm@10.28.0 --activate
  cd frontend && DEMO_BIG_N={{env_var_or_default("DEMO_BIG_N_TS","60000")}} pnpm demobig:gen
  cd frontend && DEMO_BIG=1 pnpm build
  docker build -f backend/Dockerfile --build-arg DEMO_BIG=1 --build-arg DEMO_BIG_N={{env_var_or_default("DEMO_BIG_N_GO","20000")}} -t vinyl-spins-api:demo .
  docker build -f frontend/Dockerfile.prod --build-arg DEMO_BIG=1 --build-arg DEMO_BIG_N={{env_var_or_default("DEMO_BIG_N_TS","60000")}} -t vinyl-spins-ui:demo frontend