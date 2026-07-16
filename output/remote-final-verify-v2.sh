set -eu
backup=/root/newapi-deploy-backups/billing-history-20260714-181326
app=newapi_isgp-new-api-1
mysqlc=newapi_isgp-mysql-1
pass=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_ROOT_PASSWORD=//p')
db=$(docker inspect "$mysqlc" --format '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^MYSQL_DATABASE=//p')
mysqlq() { docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw -uroot "$db" -e "$1"; }
uid=$(docker exec -e MYSQL_PWD="$pass" "$mysqlc" mysql --batch --raw --skip-column-names -uroot "$db" -e "SELECT id FROM users WHERE username='AIGC@YCDM' OR email='AIGC@YCDM' ORDER BY id LIMIT 1")
{
printf '== SERVICE ==\n'
docker inspect "$app" --format 'image={{.Config.Image}} image_id={{.Image}} status={{.State.Status}} health={{.State.Health.Status}} restart_count={{.RestartCount}} started={{.State.StartedAt}}'
printf 'status_http='; curl -sS -o /tmp/status.json -w '%{http_code}' --max-time 10 http://127.0.0.1:3000/api/status; echo
printf 'root_http='; curl -sS -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:3000/; echo
printf 'token_usage_unauth_http='; curl -sS -o /tmp/token-usage.json -w '%{http_code}' --max-time 10 'http://127.0.0.1:3000/api/token/usage?token_ids=459&start_timestamp=0&end_timestamp=1'; echo
printf '\n== TARGET LEDGER ==\n'
mysqlq "SELECT u.id,u.username,u.quota wallet_remaining,u.used_quota user_used,u.request_count,(u.quota+u.used_quota) wallet_plus_used,(SELECT COALESCE(SUM(used_quota),0) FROM tokens WHERE user_id=u.id) token_used_sum,(SELECT COALESCE(SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) settled_log,(SELECT COALESCE(SUM(CASE WHEN type=8 THEN quota ELSE 0 END),0) FROM logs WHERE user_id=u.id) rollback_sum,(SELECT COALESCE(SUM(quota_amount),0) FROM top_ups WHERE user_id=u.id AND status='success') successful_topup_quota FROM users u WHERE u.id=$uid;"
printf '\n== TARGET TOPUP HISTORY ==\n'
mysqlq "SELECT id,quota_amount,money,payment_method,payment_provider,FROM_UNIXTIME(create_time) create_time,status,trade_no FROM top_ups WHERE user_id=$uid ORDER BY create_time,id;"
printf '\n== PER TOKEN DIFFERENCE ==\n'
mysqlq "SELECT t.id,t.name,t.used_quota,COALESCE(x.settled_quota,0) settled_quota,COALESCE(x.rollback_quota,0) rollback_quota,t.used_quota-COALESCE(x.settled_quota,0) difference FROM tokens t LEFT JOIN (SELECT token_id,SUM(CASE WHEN type=2 THEN quota WHEN type=6 THEN -quota ELSE 0 END) settled_quota,SUM(CASE WHEN type=8 THEN quota ELSE 0 END) rollback_quota FROM logs WHERE user_id=$uid GROUP BY token_id) x ON x.token_id=t.id WHERE t.user_id=$uid ORDER BY t.id;"
printf '\n== HISTORY TYPES ==\n'
mysqlq "SELECT type,COUNT(*) rows_count,COALESCE(SUM(quota),0) quota_sum FROM logs WHERE user_id=$uid GROUP BY type ORDER BY type;"
printf '\n== TASK BILLING STATE ==\n'
mysqlq "SELECT status,COALESCE(JSON_UNQUOTE(JSON_EXTRACT(private_data,'$.billing_state')),'NULL') billing_state,COUNT(*) rows_count,SUM(quota) quota_sum FROM tasks WHERE user_id=$uid GROUP BY status,billing_state ORDER BY status,billing_state;"
printf '\n== ADMIN BACKFILL ==\n'
mysqlq "SELECT \`key\`,FROM_UNIXTIME(CAST(value AS UNSIGNED)) completed_at FROM options WHERE \`key\`='_internal.admin_quota_topup_backfill_v1'; SELECT COUNT(*) admin_topups,COALESCE(SUM(quota_amount),0) admin_quota FROM top_ups WHERE payment_provider='admin' AND status='success';"
printf '\n== STARTUP ERRORS ==\n'
docker logs --since '2026-07-14T18:29:09Z' "$app" 2>&1 | grep -Ei 'panic|fatal|error' | tail -50 || true
} | tee "$backup/deployment-and-accounting-after.tsv"
printf '\nreport=%s\n' "$backup/deployment-and-accounting-after.tsv"
