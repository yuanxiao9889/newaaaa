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
// Set NEW_API_KEY. Optional: REFERENCE_IMAGE, STYLE_IMAGE (local files).
import { readFile } from 'node:fs/promises'
import { extname } from 'node:path'

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

async function upload(path) {
  if (!path) return null
  const mime = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp' }[extname(path).toLowerCase()]
  if (!mime) throw new Error('Use JPEG, PNG or WebP')
  const bytes = await readFile(path)
  if (!bytes.length || bytes.length > 8 * 1024 * 1024) throw new Error('Image must be 1 byte to 8 MiB; compress it first')
  const data = await request('/mj/submit/upload-discord-images', {
    base64Array: ['data:' + mime + ';base64,' + bytes.toString('base64')],
  })
  if (data.code !== 1 || !Array.isArray(data.result) || data.result.length !== 1 || typeof data.result[0] !== 'string' || !data.result[0].startsWith('https://')) {
    throw new Error('Upload failed: ' + JSON.stringify(data))
  }
  return data.result[0]
}

const reference = await upload(process.env.REFERENCE_IMAGE)
const style = await upload(process.env.STYLE_IMAGE)
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
# Set NEW_API_KEY. Optional: REFERENCE_IMAGE, STYLE_IMAGE (local files).
import os
import time
import base64
from pathlib import Path
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

def upload(filename):
    if not filename:
        return None
    path = Path(filename)
    mime = {'.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp'}.get(path.suffix.lower())
    if not mime:
        raise ValueError('Use JPEG, PNG or WebP')
    content = path.read_bytes()
    if not 0 < len(content) <= 8 * 1024 * 1024:
        raise ValueError('Image must be 1 byte to 8 MiB; compress it first')
    data_url = 'data:' + mime + ';base64,' + base64.b64encode(content).decode('ascii')
    data = request('/mj/submit/upload-discord-images', {'base64Array': [data_url]})
    urls = data.get('result')
    if data.get('code') != 1 or not isinstance(urls, list) or len(urls) != 1 or not isinstance(urls[0], str) or not urls[0].startswith('https://'):
        raise RuntimeError(data)
    return urls[0]

reference = upload(os.environ.get('REFERENCE_IMAGE'))
style = upload(os.environ.get('STYLE_IMAGE'))
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
# Bash (WSL/Git Bash on Windows), curl, jq, base64, file.
# Set NEW_API_KEY. Optional: REFERENCE_IMAGE, STYLE_IMAGE (local files).
set -euo pipefail
: "${'${'}NEW_API_KEY:?Set NEW_API_KEY first}"
BASE_URL='__BASE_URL__'
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

upload() {
  local image="$1" mime size
  mime=$(file --brief --mime-type "$image")
  case "$mime" in image/png|image/jpeg|image/webp) ;; *) echo 'Use JPEG, PNG or WebP' >&2; return 1 ;; esac
  size=$(wc -c < "$image")
  (( size > 0 && size <= 8388608 )) || { echo 'Image exceeds 8 MiB or is empty' >&2; return 1; }
  { printf 'data:%s;base64,' "$mime"; base64 < "$image" | tr -d '\r\n'; } > "$work/image.txt"
  jq -n --rawfile image "$work/image.txt" '{base64Array:[$image]}' > "$work/upload.json"
  curl --fail-with-body -sS --max-time 180 "$BASE_URL/mj/submit/upload-discord-images" \
    -H "Authorization: Bearer $NEW_API_KEY" -H 'Content-Type: application/json' \
    --data-binary @"$work/upload.json" > "$work/upload-response.json"
  jq -e '.code == 1 and (.result | type == "array" and length == 1) and (.result[0] | type == "string" and startswith("https://"))' "$work/upload-response.json" >/dev/null || { cat "$work/upload-response.json" >&2; return 1; }
  jq -r '.result[0]' "$work/upload-response.json"
}

prompt='A cinematic portrait --ar 4:5 --v 7'
if [[ -n "${'${'}REFERENCE_IMAGE:-}" ]]; then ref=$(upload "$REFERENCE_IMAGE"); prompt="$ref $prompt"; fi
if [[ -n "${'${'}STYLE_IMAGE:-}" ]]; then style=$(upload "$STYLE_IMAGE"); prompt="$prompt --sref $style"; fi
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
