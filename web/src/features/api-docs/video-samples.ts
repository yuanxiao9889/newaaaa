import type { CodeSamples } from './code-example'

export function buildVideoSubmitSamples(
  baseUrl: string,
  body: Record<string, unknown>
): CodeSamples {
  const url = `${baseUrl.replace(/\/+$/, '')}/v1/videos`
  const json = JSON.stringify(body, null, 2)
  return {
    curl: `curl --fail-with-body ${JSON.stringify(url)} \\\n  -H "Authorization: Bearer $NEW_API_KEY" \\\n  -H 'Content-Type: application/json' \\\n  --data-binary @- <<'JSON'\n${json}\nJSON`,
    python: `import json\nimport os\nimport requests\n\npayload = json.loads(${JSON.stringify(json)})\nresponse = requests.post(\n    ${JSON.stringify(url)},\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload, timeout=180,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    javascript: `const response = await fetch(${JSON.stringify(url)}, {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${process.env.NEW_API_KEY}\`, 'Content-Type': 'application/json' },\n  body: JSON.stringify(${json}),\n  signal: AbortSignal.timeout(180000),\n})\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
  }
}

export function buildVideoLifecycleSamples(baseUrl: string): CodeSamples {
  const base = JSON.stringify(baseUrl.replace(/\/+$/, ''))
  const body = {
    model: '全能视频2.5-301010-T',
    prompt: 'A cinematic shot of a mountain ridge at sunrise.',
    duration: 5,
    ratio: '16:9',
    resolution: '720p',
  }
  return {
    curl: `${buildVideoSubmitSamples(baseUrl, body).curl}\n\n# Set TASK_ID to the public id from the create response.\nTASK_ID='task_replace_me'\ncurl --fail-with-body ${JSON.stringify(`${baseUrl}/v1/videos/`)}"$TASK_ID" \\\n  -H "Authorization: Bearer $NEW_API_KEY"\n\n# Only after a successful terminal status:\ncurl --fail-with-body ${JSON.stringify(`${baseUrl}/v1/videos/`)}"$TASK_ID/content" \\\n  -H "Authorization: Bearer $NEW_API_KEY" --output result.mp4`,
    python: `import json
import os
import time
from pathlib import Path
from urllib.parse import quote
import requests

base_url = ${base}
headers = {"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"}
payload = json.loads(${JSON.stringify(JSON.stringify(body))})
# Submit exactly once. Replace payload with a recipe from your model group.
created = requests.post(f"{base_url}/v1/videos", headers=headers, json=payload, timeout=180)
created.raise_for_status()
body = created.json()
task_id = body.get("id") or body.get("task_id")
if not isinstance(task_id, str) or not task_id:
    raise RuntimeError(f"Missing public task ID: {body}")
Path("video-task.json").write_text(json.dumps({"id": task_id, "base_url": base_url}), encoding="utf-8")
print("Saved task ID:", task_id)
task_url = f"{base_url}/v1/videos/{quote(task_id, safe='')}"
deadline = time.monotonic() + 1200
while time.monotonic() < deadline:
    response = requests.get(task_url, headers=headers, timeout=60)
    response.raise_for_status()
    task = response.json()
    status = str(task.get("status", "")).lower()
    if status in {"completed", "succeeded", "success", "done"}:
        with requests.get(f"{task_url}/content", headers=headers, timeout=300, stream=True) as content:
            content.raise_for_status()
            with open("result.mp4", "wb") as output:
                for chunk in content.iter_content(chunk_size=1024 * 1024):
                    output.write(chunk)
        break
    if status in {"failed", "failure", "error", "cancelled", "canceled", "expired"}:
        raise RuntimeError(f"Task {task_id} failed: {task}")
    time.sleep(5)
else:
    raise TimeoutError(f"Polling timed out. Resume GET for {task_id}; do not submit again.")`,
    javascript: `import { writeFile } from 'node:fs/promises'

const baseURL = ${base}
const headers = { Authorization: \`Bearer \${process.env.NEW_API_KEY}\` }
const payload = ${JSON.stringify(body, null, 2)}
// Submit exactly once. Replace payload with a recipe from your model group.
const created = await fetch(\`\${baseURL}/v1/videos\`, {
  method: 'POST', headers: { ...headers, 'Content-Type': 'application/json' },
  body: JSON.stringify(payload), signal: AbortSignal.timeout(180000),
})
if (!created.ok) throw new Error(await created.text())
const body = await created.json()
const taskId = body.id ?? body.task_id
if (typeof taskId !== 'string' || !taskId) throw new Error('Missing public task ID')
await writeFile('video-task.json', JSON.stringify({ id: taskId, baseURL }))
console.log('Saved task ID:', taskId)
const taskURL = \`\${baseURL}/v1/videos/\${encodeURIComponent(taskId)}\`
const deadline = Date.now() + 20 * 60 * 1000
let completed = false
while (Date.now() < deadline) {
  const response = await fetch(taskURL, { headers, signal: AbortSignal.timeout(60000) })
  if (!response.ok) throw new Error(await response.text())
  const task = await response.json()
  const status = String(task.status ?? '').toLowerCase()
  if (['completed', 'succeeded', 'success', 'done'].includes(status)) {
    const content = await fetch(\`\${taskURL}/content\`, { headers, signal: AbortSignal.timeout(300000) })
    if (!content.ok) throw new Error(await content.text())
    await writeFile('result.mp4', Buffer.from(await content.arrayBuffer()))
    completed = true
    break
  }
  if (['failed', 'failure', 'error', 'cancelled', 'canceled', 'expired'].includes(status)) {
    throw new Error(\`Task \${taskId} failed: \${JSON.stringify(task)}\`)
  }
  await new Promise((resolve) => setTimeout(resolve, 5000))
}
if (!completed) throw new Error(\`Polling timed out. Resume GET for \${taskId}; do not submit again.\`)`,
  }
}
