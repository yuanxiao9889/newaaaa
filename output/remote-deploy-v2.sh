set -eu
workdir=/www/dk_project/dk_app/newapi/newapi_isGp
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
compose="$workdir/docker-compose.yml"
new_image=new-api-local:local-20260715-billing-history-v2
app=newapi_isgp-new-api-1
cp -a "$compose" "$backup/docker-compose.pre-v2.yml"
python3 - "$compose" "$new_image" <<'PY'
import sys
path, image = sys.argv[1], sys.argv[2]
with open(path, 'r', encoding='utf-8') as f:
    lines = f.readlines()
inside = False
changed = False
for i, line in enumerate(lines):
    if line.startswith('  new-api:'):
        inside = True
        continue
    if inside and line.startswith('  ') and not line.startswith('    ') and line.strip():
        break
    if inside and line.startswith('    image:'):
        lines[i] = f'    image: {image}\n'
        changed = True
        break
if not changed:
    raise SystemExit('new-api image line not found')
with open(path, 'w', encoding='utf-8') as f:
    f.writelines(lines)
PY
cd "$workdir"
docker compose config >/dev/null
if ! docker compose up -d --no-deps --force-recreate new-api; then
  cp -a "$backup/docker-compose.pre-v2.yml" "$compose"
  docker compose up -d --no-deps --force-recreate new-api || true
  exit 1
fi
healthy=0
for i in $(seq 1 60); do
  status=$(docker inspect "$app" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)
  if [ "$status" = healthy ] && curl -fsS --max-time 5 http://127.0.0.1:3000/api/status | grep -q '"success"[[:space:]]*:[[:space:]]*true'; then
    healthy=1
    break
  fi
  sleep 2
done
if [ "$healthy" -ne 1 ]; then
  echo 'v2 deployment unhealthy; rolling back'
  docker logs --tail 200 "$app" || true
  cp -a "$backup/docker-compose.pre-v2.yml" "$compose"
  docker compose up -d --no-deps --force-recreate new-api || true
  exit 1
fi
docker inspect "$app" --format 'container={{.Name}} image={{.Config.Image}} image_id={{.Image}} status={{.State.Status}} health={{.State.Health.Status}} restart_count={{.RestartCount}} started={{.State.StartedAt}}'
docker logs --since 5m "$app" 2>&1 | grep -Ei 'backfill|error|panic|fatal|migrat' | tail -100 || true
