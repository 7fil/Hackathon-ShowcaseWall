#!/usr/bin/env bash
# Wipe ALL data of one compose stack (PostgreSQL, Redis, cover images, Prometheus and
# Grafana history) and start it again empty and freshly migrated.
#
#   ./scripts/reset-data.sh            # run in the stack's directory on the VM
#
# This cannot be undone. It asks you to type the environment name first.
set -euo pipefail

cd "$(dirname "$0")/.."
[ -f .env ] || { echo "no .env here: run this in the stack's directory"; exit 1; }

env_name=$(grep '^APP_ENV=' .env | tail -n1 | cut -d= -f2-)
project=$(docker compose config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["name"])')

echo "Stack:       $project"
echo "Environment: ${env_name:-unknown}"
echo "This DELETES the database, Redis, cover images and monitoring data of this stack."
read -r -p "Type the environment name (${env_name:-?}) to continue: " answer
[ "$answer" = "$env_name" ] || { echo "aborted"; exit 1; }

docker compose down -v --remove-orphans
docker compose up -d --no-build
echo "Done: the stack started with an empty database. Make admins with:"
echo "  docker exec -it \$(docker compose ps -q api | head -n1) /app/seed email@example.com"
