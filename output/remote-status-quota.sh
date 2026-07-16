set -eu
curl -fsS http://127.0.0.1:3000/api/status | tr ',' '\n' | grep -Ei 'quota_per_unit|display.*currency|currency' | head -20 || true
