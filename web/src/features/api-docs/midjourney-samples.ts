/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { CodeSamples } from './code-example'

export function getMidjourneyWorkflowSamples(baseUrl: string): CodeSamples {
  return {
    javascript: javascript.replaceAll('__BASE_URL__', baseUrl),
    python: python.replaceAll('__BASE_URL__', baseUrl),
    curl: curl.replaceAll('__BASE_URL__', baseUrl),
  }
}

const javascript = String.raw`// Node.js 20+. Save as mj.mjs; run: node mj.mjs
// Set NEW_API_KEY. Optional: REFERENCE_IMAGE_URL, STYLE_IMAGE_URL (public HTTPS URLs).

const baseURL = '__BASE_URL__'
const apiKey = process.env.NEW_API_KEY
if (!apiKey) throw new Error('Set NEW_API_KEY first')
const headers = { Authorization: 'Bearer ' + apiKey }

async function request(path, body, timeoutMs = 180000) {
  const response = await fetch(baseURL + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { ...headers, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(timeoutMs),
  })
  if (!response.ok) throw new Error('HTTP ' + response.status + ': ' + await response.text())
  const data = await response.json()
  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('Invalid JSON response')
  return data
}

const reference = process.env.REFERENCE_IMAGE_URL
const style = process.env.STYLE_IMAGE_URL
for (const url of [reference, style]) {
  if (url && !/^https:\/\/[^\s]+$/.test(url)) throw new Error('Use a public HTTPS image URL')
}
const prompt = [reference, 'A cinematic portrait --ar 4:5 --v 7', style ? '--sref ' + style : null].filter(Boolean).join(' ')
// Never automatically retry a submission: a timeout may still have created a billed task.
const submitted = await request('/mj/submit/imagine', {
  prompt, botType: 'MID_JOURNEY', speedMode: 'fast', base64Array: [],
})
if (![1, 21, 22].includes(submitted.code) || typeof submitted.result !== 'string' || !submitted.result.trim()) {
  throw new Error('Submit failed: ' + JSON.stringify(submitted))
}
const taskId = submitted.result
console.log('Task ID:', taskId) // Save this ID to resume polling later.
const deadline = Date.now() + 20 * 60 * 1000
while (Date.now() < deadline) {
  const remaining = deadline - Date.now()
  if (remaining <= 0) break
  const task = await request('/mj/task/' + encodeURIComponent(taskId) + '/fetch', undefined, Math.min(30000, remaining))
  if (task.code !== undefined || typeof task.status !== 'string') throw new Error('Query failed: ' + JSON.stringify(task))
  if (task.status === 'FAILURE' || task.status === 'CANCELLED') throw new Error(task.failReason || JSON.stringify(task))
  if (task.status === 'SUCCESS') {
    if (typeof task.imageUrl !== 'string' || !task.imageUrl) throw new Error('Success without imageUrl')
    console.log(task.imageUrl, task.buttons ?? [])
    process.exit(0)
  }
  if (!['NOT_START', 'SUBMITTED', 'IN_PROGRESS'].includes(task.status)) throw new Error('Unknown task status: ' + task.status)
  await new Promise(resolve => setTimeout(resolve, Math.max(0, Math.min(5000, deadline - Date.now()))))
}
throw new Error('Polling timed out; resume with task ID: ' + taskId)
`

const python = String.raw`# Python 3.9+; install dependency: python -m pip install requests
# Save as mj.py; run: python mj.py
# Set NEW_API_KEY. Optional: REFERENCE_IMAGE_URL, STYLE_IMAGE_URL (public HTTPS URLs).
import os
import time
import requests

BASE_URL = '__BASE_URL__'
HEADERS = {'Authorization': 'Bearer ' + os.environ['NEW_API_KEY']}

def request(path, body=None, timeout=180):
    response = requests.request('GET' if body is None else 'POST', BASE_URL + path,
                                headers=HEADERS, json=body, timeout=timeout)
    response.raise_for_status()
    data = response.json()
    if not isinstance(data, dict):
        raise RuntimeError('Invalid JSON response')
    return data

reference = os.environ.get('REFERENCE_IMAGE_URL')
style = os.environ.get('STYLE_IMAGE_URL')
for url in (reference, style):
    if url and (not url.startswith('https://') or any(char.isspace() for char in url)):
        raise ValueError('Use a public HTTPS image URL')
prompt = ' '.join(part for part in [reference, 'A cinematic portrait --ar 4:5 --v 7', '--sref ' + style if style else None] if part)
# Do not automatically retry POST: a timed-out submission may already be accepted.
submitted = request('/mj/submit/imagine', {
    'prompt': prompt, 'botType': 'MID_JOURNEY', 'speedMode': 'fast', 'base64Array': [],
})
task_id = submitted.get('result')
if submitted.get('code') not in (1, 21, 22) or not isinstance(task_id, str) or not task_id.strip():
    raise RuntimeError(submitted)
print('Task ID:', task_id, flush=True)
deadline = time.monotonic() + 20 * 60
from urllib.parse import quote
completed = False
while time.monotonic() < deadline:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        break
    task = request('/mj/task/' + quote(task_id, safe='') + '/fetch', timeout=min(30, remaining))
    status = task.get('status')
    if 'code' in task or not isinstance(status, str):
        raise RuntimeError(task)
    if status in ('FAILURE', 'CANCELLED'):
        raise RuntimeError(task.get('failReason') or task)
    if status == 'SUCCESS':
        if not isinstance(task.get('imageUrl'), str) or not task['imageUrl']:
            raise RuntimeError('Success without imageUrl')
        print(task['imageUrl'])
        print(task.get('buttons', []))
        completed = True
        break
    if status not in ('NOT_START', 'SUBMITTED', 'IN_PROGRESS'):
        raise RuntimeError('Unknown task status: ' + status)
    time.sleep(max(0, min(5, deadline - time.monotonic())))
if not completed:
    raise TimeoutError('Polling timed out; resume with task ID: ' + task_id)
`

const curl = String.raw`#!/usr/bin/env bash
# Bash (WSL/Git Bash on Windows), curl and jq.
# Set NEW_API_KEY. Optional: REFERENCE_IMAGE_URL, STYLE_IMAGE_URL (public HTTPS URLs).
set -euo pipefail
: "${'${'}NEW_API_KEY:?Set NEW_API_KEY first}"
BASE_URL='__BASE_URL__'
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

prompt='A cinematic portrait --ar 4:5 --v 7'
if [[ -n "${'${'}REFERENCE_IMAGE_URL:-}" ]]; then [[ "$REFERENCE_IMAGE_URL" == https://* && "$REFERENCE_IMAGE_URL" != *[[:space:]]* ]] || { echo "Use a public HTTPS image URL" >&2; exit 1; }; prompt="$REFERENCE_IMAGE_URL $prompt"; fi
if [[ -n "${'${'}STYLE_IMAGE_URL:-}" ]]; then [[ "$STYLE_IMAGE_URL" == https://* && "$STYLE_IMAGE_URL" != *[[:space:]]* ]] || { echo "Use a public HTTPS image URL" >&2; exit 1; }; prompt="$prompt --sref $STYLE_IMAGE_URL"; fi
jq -n --arg prompt "$prompt" '{prompt:$prompt,botType:"MID_JOURNEY",speedMode:"fast",base64Array:[]}' > "$work/submit.json"
# Do not automatically retry POST: a timed-out submission may already be accepted.
curl --fail-with-body -sS --max-time 180 "$BASE_URL/mj/submit/imagine" \
  -H "Authorization: Bearer $NEW_API_KEY" -H 'Content-Type: application/json' \
  --data-binary @"$work/submit.json" > "$work/submitted.json"
jq -e '(.code == 1 or .code == 21 or .code == 22) and (.result | type == "string" and test("\\S"))' "$work/submitted.json" >/dev/null || { cat "$work/submitted.json" >&2; exit 1; }
task_id=$(jq -r '.result' "$work/submitted.json")
task_path=$(jq -rn --arg id "$task_id" '$id | @uri')
printf 'Task ID: %s\n' "$task_id"
deadline=$((SECONDS + 1200))
while (( SECONDS < deadline )); do
  remaining=$((deadline - SECONDS)); timeout=30
  (( remaining >= timeout )) || timeout=$remaining
  (( timeout > 0 )) || break
  curl --fail-with-body -sS --max-time "$timeout" "$BASE_URL/mj/task/$task_path/fetch" \
    -H "Authorization: Bearer $NEW_API_KEY" > "$work/task.json"
  jq -e 'type == "object" and (has("code") | not) and (.status | type == "string")' "$work/task.json" >/dev/null || { cat "$work/task.json" >&2; exit 1; }
  status=$(jq -r '.status' "$work/task.json")
  case "$status" in
    SUCCESS) jq -e '.imageUrl | type == "string" and length > 0' "$work/task.json" >/dev/null; cat "$work/task.json"; exit 0 ;;
    FAILURE|CANCELLED) cat "$work/task.json" >&2; exit 1 ;;
    NOT_START|SUBMITTED|IN_PROGRESS) ;;
    *) cat "$work/task.json" >&2; exit 1 ;;
  esac
  remaining=$((deadline - SECONDS)); delay=5
  (( remaining >= delay )) || delay=$remaining
  (( delay > 0 )) && sleep "$delay"
done
printf 'Polling timed out; resume with task ID: %s\n' "$task_id" >&2
exit 1
`
