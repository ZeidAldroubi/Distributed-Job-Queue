#!/usr/bin/env sh
set -eu

TOTAL="${TOTAL:-500}"
CONCURRENCY="${CONCURRENCY:-50}"
URL="${URL:-http://localhost:8080/jobs}"
BODY='{"type":"resize_image","payload":{"image_url":"http://api:8080/sample/image.png"}}'

if command -v hey >/dev/null 2>&1; then
  hey -n "$TOTAL" -c "$CONCURRENCY" -m POST -H "Content-Type: application/json" -d "$BODY" "$URL"
else
  go run ./main.go -n "$TOTAL" -c "$CONCURRENCY" -url "$URL" -body "$BODY"
fi
