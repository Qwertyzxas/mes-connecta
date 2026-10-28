#!/usr/bin/env sh
set -e
cd "$(dirname "$0")/.."

if ! command -v docker >/dev/null 2>&1; then
  echo "Нужен Docker Desktop: https://www.docker.com/products/docker-desktop/"
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker установлен, но не запущен. Откройте Docker Desktop и повторите."
  exit 1
fi

rand() {
  openssl rand -hex "$1" 2>/dev/null || head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
}

if [ ! -f .env ]; then
  cat > .env <<ENV
DOMAIN=localhost
POSTGRES_PASSWORD=$(rand 24)
JWT_SECRET=$(rand 32)
S3_SECRET_KEY=$(rand 24)
LIVEKIT_API_SECRET=$(rand 32)
LIVEKIT_CONFIG=./deploy/livekit.local.yaml
ACCESS_TTL=15m
REFRESH_TTL=720h
ENV
  echo ".env создан"
fi

set -a
. ./.env
set +a

if [ ! -f deploy/s3.json ]; then
  printf '{"identities":[{"name":"connecta","credentials":[{"accessKey":"connecta","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' "$S3_SECRET_KEY" > deploy/s3.json
fi

docker compose up -d --build
echo
echo "Connecta запущена: https://${DOMAIN:-localhost}"
echo "Остановить: docker compose down"
