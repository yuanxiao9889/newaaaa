set -eu
workdir=/www/dk_project/dk_app/newapi/newapi_isGp
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
compose="$workdir/docker-compose.yml"
new_image=new-api-local:local-20260715-billing-history
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
cutoff_log=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT COALESCE(MAX(id),0) FROM logs")
cutoff_task=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT COALESCE(MAX(id),0) FROM tasks")
cutoff_target_refund=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT COALESCE(MAX(id),0) FROM logs WHERE user_id=$uid AND type=6")
printf 'cutover_utc=%s\ncutoff_log_id=%s\ncutoff_task_id=%s\ncutoff_target_refund_log_id=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$cutoff_log" "$cutoff_task" "$cutoff_target_refund" | tee "$backup/cutover.txt"
cp -a "$compose" "$backup/docker-compose.pre-deploy.yml"
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
  cp -a "$backup/docker-compose.pre-deploy.yml" "$compose"
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
  echo 'new deployment unhealthy; rolling back'
  docker logs --tail 200 "$app" || true
  cp -a "$backup/docker-compose.pre-deploy.yml" "$compose"
  docker compose up -d --no-deps --force-recreate new-api || true
  exit 1
fi
docker inspect "$app" --format 'container={{.Name}} image={{.Config.Image}} image_id={{.Image}} status={{.State.Status}} health={{.State.Health.Status}} started={{.State.StartedAt}}'
curl -fsS --max-time 10 http://127.0.0.1:3000/api/status | grep -o '"success"[[:space:]]*:[[:space:]]*true' | head -1
docker logs --since 5m "$app" 2>&1 | grep -Ei 'error|panic|fatal|migrat' | tail -100 || true
