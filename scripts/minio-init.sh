#!/bin/sh
# minio-init.sh — runs once at startup via docker-compose to create buckets.
# Executed by the minio-init service using the MinIO Client (mc).
set -e

MC="mc"
ALIAS="oj"
ENDPOINT="http://minio:9000"
ACCESS_KEY="${MINIO_ROOT_USER:-minioadmin}"
SECRET_KEY="${MINIO_ROOT_PASSWORD:-minioadmin}"

echo "Waiting for MinIO to be ready..."
until $MC alias set "$ALIAS" "$ENDPOINT" "$ACCESS_KEY" "$SECRET_KEY" 2>/dev/null; do
  sleep 2
done

echo "MinIO ready. Creating buckets..."

# submissions — contestant source code uploaded by the API server
$MC mb --ignore-existing "$ALIAS/submissions"

# testcases — problem input/output files uploaded by problem setters
$MC mb --ignore-existing "$ALIAS/testcases"

# Apply a private access policy so objects are not publicly listable.
$MC anonymous set none "$ALIAS/submissions"
$MC anonymous set none "$ALIAS/testcases"

echo "MinIO init complete."
