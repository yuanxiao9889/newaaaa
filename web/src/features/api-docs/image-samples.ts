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
import type { ImageModelDoc } from './data'
import { buildHyImageSamples } from './hy-image-samples'
import { buildOpenAISamples } from './openai-samples'

type ModelOperation = 'generate' | 'edit'

function endpointFor(model: ImageModelDoc) {
  if (model.family === 'openai') return '/v1/images/generations'
  return `/v1beta/models/${encodeURIComponent(model.model)}:generateContent`
}

function buildGrokSamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation
): CodeSamples {
  const isGrokImage2 = model.model === 'grok-imagine-image-2.0'
  const requestPath =
    operation === 'edit'
      ? '/v1/images/generations'
      : '/v1/images/generations?async=true'
  const prompt =
    operation === 'edit'
      ? 'Keep the character identity and change the scene to sunrise'
      : 'Cinematic storyboard frame of a detective in a rainy neon alley'
  const qualityJson = isGrokImage2 ? ',\n    "quality": "medium"' : ''
  const qualityPython = isGrokImage2 ? '\n    "quality": "medium",' : ''
  const qualityJavaScript = isGrokImage2 ? "\n    quality: 'medium'," : ''
  const referenceJson =
    operation === 'edit'
      ? ',\n    "image": {\n      "url": "data:image/png;base64,BASE64_REFERENCE_IMAGE"\n    }'
      : ''
  const pythonReference =
    operation === 'edit'
      ? `\nimport base64\n\nwith open("reference.png", "rb") as file:\n    reference = "data:image/png;base64," + base64.b64encode(file.read()).decode("ascii")\n`
      : ''
  const pythonImage =
    operation === 'edit' ? '\n    "image": {"url": reference},' : ''
  const jsReference =
    operation === 'edit'
      ? `import { readFile } from 'node:fs/promises'\n\nconst reference = 'data:image/png;base64,' + (await readFile('reference.png')).toString('base64')\n\n`
      : ''
  const jsImage = operation === 'edit' ? '\n    image: { url: reference },' : ''

  return {
    curl: `curl '${baseUrl}${requestPath}' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "model": "${model.model}",\n    "prompt": "${prompt}",\n    "n": 1,\n    "response_format": "b64_json",\n    "resolution": "2k",\n    "aspect_ratio": "16:9"${qualityJson}${referenceJson}\n  }'`,
    python: `import os\n\nimport requests\n${pythonReference}\npayload = {\n    "model": "${model.model}",\n    "prompt": "${prompt}",\n    "n": 1,\n    "response_format": "b64_json",\n    "resolution": "2k",\n    "aspect_ratio": "16:9",${qualityPython}${pythonImage}\n}\nresponse = requests.post(\n    "${baseUrl}${requestPath}",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    javascript: `${jsReference}const response = await fetch('${baseUrl}${requestPath}', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    model: '${model.model}',\n    prompt: '${prompt}',\n    n: 1,\n    response_format: 'b64_json',\n    resolution: '2k',\n    aspect_ratio: '16:9',${qualityJavaScript}${jsImage}\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
  }
}

function buildDouSamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation
): CodeSamples {
  const isEdit = operation === 'edit'
  const prompt = isEdit
    ? 'Keep the character identity and change the scene to sunrise'
    : 'Cinematic storyboard frame of a detective in a rainy neon alley'
  const requestFields = isEdit
    ? `"model": "${model.model}",
    "prompt": "${prompt}",
    "image_urls": ["https://example.com/reference-1.png", "https://example.com/reference-2.png"],
    "strength": "HIGH",
    "size": "2048x1152"`
    : `"model": "${model.model}",
    "prompt": "${prompt}",
    "size": "2048x1152"`

  return {
    curl: `curl '${baseUrl}/v1/images/generations' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H "X-API-Key: $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{
    ${requestFields}
  }'

# The API normally returns HTTP 202 with an id. Poll until status is succeeded.
curl '${baseUrl}/v1/images/generations/{id}' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H "X-API-Key: $NEW_API_KEY"

curl -fL '${baseUrl}/v1/images/generations/{id}/content' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H "X-API-Key: $NEW_API_KEY" \\
  -o result.png`,
    python: `import os
import time
from pathlib import Path

import requests

BASE_URL = "${baseUrl}"
API_KEY = os.environ["NEW_API_KEY"]
HEADERS = {"Authorization": f"Bearer {API_KEY}", "X-API-Key": API_KEY}

${
  isEdit
    ? `reference_url = "https://media.example.com/reference.png"
payload = {
    "model": "${model.model}",
    "prompt": "${prompt}",
    "image_url": reference_url,
    "strength": "HIGH",
    "size": "2048x1152",
}`
    : `payload = {
    "model": "${model.model}",
    "prompt": "${prompt}",
    "size": "2048x1152",
}`
}

submitted = requests.post(
    f"{BASE_URL}/v1/images/generations",
    headers={**HEADERS, "Content-Type": "application/json"},
    json=payload,
    timeout=120,
)
submitted.raise_for_status()
task_id = submitted.json()["id"]

while True:
    task = requests.get(f"{BASE_URL}/v1/images/generations/{task_id}", headers=HEADERS, timeout=30)
    task.raise_for_status()
    body = task.json()
    status = str(body.get("status", "")).lower()
    if status in {"succeeded", "success", "completed", "done"}:
        content = requests.get(f"{BASE_URL}/v1/images/generations/{task_id}/content", headers=HEADERS, timeout=300)
        content.raise_for_status()
        Path("result.png").write_bytes(content.content)
        break
    if status in {"failed", "error", "expired", "cancelled"}:
        raise RuntimeError(body)
    time.sleep(3)`,
    javascript: `const apiKey = process.env.NEW_API_KEY
const headers = { Authorization: \`Bearer \${apiKey}\`, 'X-API-Key': apiKey }
${
  isEdit
    ? `const referenceURL = 'https://media.example.com/reference.png'
const payload = {
  model: '${model.model}',
  prompt: '${prompt}',
  image_url: referenceURL,
  strength: 'HIGH',
  size: '2048x1152',
}`
    : `const payload = {
  model: '${model.model}',
  prompt: '${prompt}',
  size: '2048x1152',
}`
}
const submitted = await fetch('${baseUrl}/v1/images/generations', {
  method: 'POST',
  headers: { ...headers, 'Content-Type': 'application/json' },
  body: JSON.stringify(payload),
})
if (!submitted.ok) throw new Error(await submitted.text())
const taskId = (await submitted.json()).id
for (;;) {
  const response = await fetch(\`${baseUrl}/v1/images/generations/\${taskId}\`, { headers })
  if (!response.ok) throw new Error(await response.text())
  const task = await response.json()
  const status = String(task.status ?? '').toLowerCase()
  if (['succeeded', 'success', 'completed', 'done'].includes(status)) {
    const content = await fetch(\`${baseUrl}/v1/images/generations/\${taskId}/content\`, { headers })
    if (!content.ok) throw new Error(await content.text())
    const { writeFile } = await import('node:fs/promises')
    await writeFile('result.png', Buffer.from(await content.arrayBuffer()))
    break
  }
  if (['failed', 'error', 'expired', 'cancelled'].includes(status)) throw new Error(JSON.stringify(task))
  await new Promise((resolve) => setTimeout(resolve, 3000))
}`,
  }
}

function buildGeminiSamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation
): CodeSamples {
  const url = `${baseUrl}${endpointFor(model)}`
  const referencePart =
    operation === 'edit'
      ? ',\n          {\n            "inlineData": {\n              "mimeType": "image/png",\n              "data": "BASE64_REFERENCE_IMAGE"\n            }\n          }'
      : ''
  const pythonReference =
    operation === 'edit'
      ? `\nimport base64\n\nwith open("reference.png", "rb") as file:\n    reference = base64.b64encode(file.read()).decode("utf-8")\n`
      : ''
  const pythonPart =
    operation === 'edit'
      ? `,\n                    {\n                        "inlineData": {\n                            "mimeType": "image/png",\n                            "data": reference,\n                        }\n                    }`
      : ''
  const jsReference =
    operation === 'edit'
      ? `import { readFile } from 'node:fs/promises'\n\nconst reference = (await readFile('reference.png')).toString('base64')\n\n`
      : ''
  const jsPart =
    operation === 'edit'
      ? `,\n          {\n            inlineData: { mimeType: 'image/png', data: reference },\n          }`
      : ''
  const basePrompt =
    operation === 'edit'
      ? 'Keep the character identity and change the scene to sunrise'
      : 'Cinematic storyboard frame of a detective in a rainy neon alley'
  const prompt = `${basePrompt} [size: 2K, aspect ratio: 16:9]`

  const thinking = model.model === 'nano-banana-2.1'
  return {
    curl: `curl '${url}' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "contents": [{\n      "role": "user",\n      "parts": [\n        { "text": "${prompt}" }${referencePart}\n      ]\n    }],\n    "generationConfig": {\n      "topP": 0.95,${thinking ? '\n      "thinkingConfig": {"thinkingLevel": "medium"},' : ''}\n      "responseModalities": ["IMAGE"],\n      "imageConfig": {\n        "aspectRatio": "16:9",\n        "imageSize": "2K"\n      }\n    }\n  }'`,
    python: `import os\n\nimport requests\n${pythonReference}\npayload = {\n    "contents": [\n        {\n            "role": "user",\n            "parts": [\n                {"text": "${prompt}"}${pythonPart}\n            ],\n        }\n    ],\n    "generationConfig": {\n        "topP": 0.95,${thinking ? '\n      "thinkingConfig": {"thinkingLevel": "medium"},' : ''}\n        "responseModalities": ["IMAGE"],\n        "imageConfig": {"aspectRatio": "16:9", "imageSize": "2K"},\n    },\n}\n\nresponse = requests.post(\n    "${url}",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nimage_base64 = response.json()["candidates"][0]["content"]["parts"][0]["inlineData"]["data"]`,
    javascript: `${jsReference}const response = await fetch('${url}', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    contents: [{\n      role: 'user',\n      parts: [\n        { text: '${prompt}' }${jsPart}\n      ],\n    }],\n    generationConfig: {\n      topP: 0.95,${thinking ? "\n      thinkingConfig: { thinkingLevel: 'medium' }," : ''}\n      responseModalities: ['IMAGE'],\n      imageConfig: { aspectRatio: '16:9', imageSize: '2K' },\n    },\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconst result = await response.json()\nconst imageBase64 = result.candidates[0].content.parts[0].inlineData.data`,
  }
}

export function buildImageSamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation
): CodeSamples {
  if (model.model === 'hy-image-v3.5-preview') {
    return buildHyImageSamples(baseUrl, operation)
  }
  if (model.family === 'oopii') {
    return buildDouSamples(model, baseUrl, operation)
  }
  if (model.model.startsWith('grok-imagine-image-')) {
    return buildGrokSamples(model, baseUrl, operation)
  }
  if (model.family === 'openai') {
    return buildOpenAISamples(model, baseUrl, operation)
  }
  return buildGeminiSamples(model, baseUrl, operation)
}
