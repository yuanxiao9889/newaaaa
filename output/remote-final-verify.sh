set -eu
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
mysqlq() { docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "$1"; }
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
printf '== SERVICE ==\n'
docker inspect "$app" --format 'image={{.Config.Image}} image_id={{.Image}} status={{.State.Status}} health={{.State.Health.Status}} restart_count={{.RestartCount}} started={{.State.StartedAt}}'
printf 'status_http='; curl -sS -o /tmp/status.json -w '%{http_code}' --max-time 10 http://127.0.0.1:3000/api/status; echo
printf 'root_http='; curl -sS -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:3000/; echo
printf 'token_usage_unauth_http='; curl -sS -o /tmp/token-usage.json -w '%{http_code}' --max-time 10 'http://127.0.0.1:3000/api/token/usage?token_ids=459&start_timestamp=0&end_timestamp=1'; echo
printf '\n== USER RECONCILIATION ==\n'
mysqlq "SELECT u.id,u.username,u.quota wallet_remaining,u.used_quota user_used,u.request_count,(u.quota+u.used_quota) wallet_plus_used,(SELECT COALESCE(SUM(used_quota),0) FROM tokens WHERE user_id=u.id) token_used_sum,(SELECT COALESCE(SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) settled_log,(SELECT COALESCE(SUM(CASE WHEN type=2 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) consume_sum,(SELECT COALESCE(SUM(CASE WHEN type=6 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) refund_sum,(SELECT COALESCE(SUM(CASE WHEN type=8 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) rollback_sum FROM users u WHERE u.id=$uid;"
printf '\n== PER TOKEN RECONCILIATION ==\n'
mysqlq "SELECT t.id,t.name,t.remain_quota,t.used_quota,COALESCE(x.consume_quota,0) consume_quota,COALESCE(x.refund_quota,0) refund_quota,COALESCE(x.rollback_quota,0) rollback_quota,COALESCE(x.settled_quota,0) settled_quota,t.used_quota-COALESCE(x.settled_quota,0) difference FROM tokens t LEFT JOIN (SELECT token_id,SUM(CASE WHEN type=2 THEN quota ELSE 0 END) consume_quota,SUM(CASE WHEN type=6 THEN quota ELSE 0 END) refund_quota,SUM(CASE WHEN type=8 THEN quota ELSE 0 END) rollback_quota,SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END) settled_quota FROM logs WHERE user_id=$uid GROUP BY token_id) x ON x.token_id=t.id WHERE t.user_id=$uid ORDER BY t.id;"
printf '\n== ROLLBACK MATCH ==\n'
mysqlq "SELECT l.type,COUNT(*) rows_count,SUM(l.quota) quota_sum,SUM(t.id IS NULL) unmatched,SUM(t.status='FAILURE') failure_tasks,SUM(COALESCE(JSON_UNQUOTE(JSON_EXTRACT(t.private_data,'$.internal_async')),'false')='true') internal_async FROM logs l LEFT JOIN tasks t ON t.user_id=l.user_id AND t.task_id=JSON_UNQUOTE(JSON_EXTRACT(l.other,'$.task_id')) WHERE l.user_id=$uid AND l.type IN (6,8) GROUP BY l.type ORDER BY l.type;"
printf '\n== BILLING STATE ==\n'
mysqlq "SELECT status,COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.billing_state')),'NULL') billing_state,COUNT(*) rows_count,SUM(quota) quota_sum FROM tasks WHERE user_id=$uid GROUP BY status,billing_state ORDER BY status,billing_state;"
printf '\n== ERROR LOGS SINCE START ==\n'
docker logs --since '2026-07-14T18:19:31Z' "$app" 2>&1 | grep -Ei 'panic|fatal|error' | tail -50 || true
