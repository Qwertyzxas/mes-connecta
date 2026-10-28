SHELL := /bin/bash

.PHONY: setup env tidy deps dev-backend dev-web up down logs build vet test test-db test-backend test-e2e

setup: env
	cd web && npm install
	cd e2e && npm install && npx playwright install chromium

env:
	@test -f .env || { \
		cp .env.example .env && \
		sed -i.bak \
			-e "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$$(openssl rand -hex 24)/" \
			-e "s/^JWT_SECRET=.*/JWT_SECRET=$$(openssl rand -hex 32)/" \
			-e "s/^S3_SECRET_KEY=.*/S3_SECRET_KEY=$$(openssl rand -hex 24)/" \
			-e "s/^LIVEKIT_API_SECRET=.*/LIVEKIT_API_SECRET=$$(openssl rand -hex 32)/" .env && \
		rm -f .env.bak && echo ".env created"; }
	@grep -q '^LIVEKIT_API_SECRET=' .env || echo "LIVEKIT_API_SECRET=$$(openssl rand -hex 32)" >> .env
	@test -f deploy/s3.json || { set -a && . ./.env && set +a && mkdir -p deploy && \
		printf '{"identities":[{"name":"connecta","credentials":[{"accessKey":"connecta","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' "$$S3_SECRET_KEY" > deploy/s3.json; }

tidy:
	cd backend && go mod tidy

deps: env
	docker compose up -d --wait postgres redis s3 livekit

test-db: deps
	@docker compose exec -T postgres psql -U connecta -d connecta -tAc \
		"SELECT 1 FROM pg_database WHERE datname = 'connecta_test'" | grep -q 1 || \
		docker compose exec -T postgres createdb -U connecta connecta_test

dev-backend: env
	@set -a && source .env && set +a && cd backend && \
		DATABASE_URL="$(DB_URL)" \
		REDIS_URL="redis://localhost:6379/0" \
		S3_ENDPOINT=localhost:9000 S3_ACCESS_KEY=connecta S3_SECRET_KEY="$$S3_SECRET_KEY" \
		LIVEKIT_URL=ws://localhost:7880 LIVEKIT_API_KEY=connecta LIVEKIT_API_SECRET="$$LIVEKIT_API_SECRET" \
		COOKIE_SECURE=false \
		go run ./cmd/server

dev-web:
	cd web && npm run dev

DB_URL = postgres://connecta:$$POSTGRES_PASSWORD@localhost:5432/connecta?sslmode=disable
TEST_DB_URL = postgres://connecta:$$POSTGRES_PASSWORD@localhost:5432/connecta_test?sslmode=disable

test: test-backend test-e2e

test-backend: test-db
	@set -a && source .env && set +a && cd backend && \
		TEST_DATABASE_URL="$(TEST_DB_URL)" TEST_REDIS_URL="redis://localhost:6379/15" \
		TEST_S3_ENDPOINT=localhost:9000 TEST_S3_ACCESS_KEY=connecta TEST_S3_SECRET_KEY="$$S3_SECRET_KEY" \
		go test -race -count=1 ./...

test-e2e: test-db
	@set -a && source .env && set +a && cd e2e && \
		DATABASE_URL="$(TEST_DB_URL)" REDIS_URL="redis://localhost:6379/14" JWT_SECRET="$$JWT_SECRET" \
		S3_ENDPOINT=localhost:9000 S3_ACCESS_KEY=connecta S3_SECRET_KEY="$$S3_SECRET_KEY" \
		LIVEKIT_URL=ws://localhost:7880 LIVEKIT_API_KEY=connecta LIVEKIT_API_SECRET="$$LIVEKIT_API_SECRET" \
		npx playwright test

vet:
	cd backend && go vet ./...
	cd web && npm run typecheck

build:
	cd backend && go build -o bin/server ./cmd/server
	cd web && npm run build

up: env
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f backend web
