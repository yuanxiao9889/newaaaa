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
import {
  Code2,
  Download,
  FileImage,
  FileJson,
  ImageIcon,
  KeyRound,
  Layers3,
  Menu,
  Search,
  ShieldAlert,
  Upload,
  X,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ScrollArea, ScrollBar } from '@/components/ui/scroll-area'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

import { CodeExample, type CodeSamples } from './code-example'
import {
  type ImageModelDoc,
  imageModels,
  oopiiModelGroups,
  sizeRows,
} from './data'
import { DocsNavigation } from './docs-navigation'
import { generationContracts } from './generation-data'
import { GenerationDocs, IntegrationExport } from './generation-docs'
import { buildHyImageSamples } from './hy-image-samples'
import { MidjourneyDocs } from './midjourney-docs'
import { buildOpenAISamples } from './openai-samples'
import { videoGroups } from './video-data'
import { VideoDocs } from './video-docs'

type ModelOperation = 'generate' | 'edit'

const modelSectionIds = new Map<string, string>([
  ...imageModels.flatMap((model) =>
    [model.model, ...(model.modelIds ?? [])].map(
      (id) => [id, `model-${model.id}`] as const
    )
  ),
  ...generationContracts.map(
    (contract) => [contract.model, contract.id] as const
  ),
  ...videoGroups.flatMap((group) =>
    group.variants.flatMap((variant) =>
      variant.models.map((id) => [id, group.id] as const)
    )
  ),
  ...oopiiModelGroups.text.map((model) => [model.id, 'text-api'] as const),
  ...oopiiModelGroups.midjourney.map(
    (model) => [model.id, 'midjourney-endpoints'] as const
  ),
])

const textApiRoutes = [
  {
    name: 'OpenAI-compatible chat',
    method: 'POST',
    path: '/v1/chat/completions',
    models: 'GPT, Claude, Gemini, Grok, GLM, DeepSeek, Kimi',
  },
  {
    name: 'OpenAI Responses',
    method: 'POST',
    path: '/v1/responses',
    models: 'GPT',
  },
  {
    name: 'Anthropic Messages',
    method: 'POST',
    path: '/v1/messages',
    models: 'Claude',
  },
  {
    name: 'Gemini generateContent',
    method: 'POST',
    path: '/v1beta/models/{model}:generateContent',
    models: 'Gemini',
  },
] as const

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

function buildAsyncImageSamples(baseUrl: string): CodeSamples {
  return {
    curl: `#!/usr/bin/env bash
set -euo pipefail

submit=$(curl -sS '${baseUrl}/v1/images/generations?async=true' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{
    "model": "all-image-2",
    "prompt": "Cinematic storyboard frame at sunrise",
    "n": 1,
    "size": "2048x1152",
    "quality": "medium"
  }')

task_id=$(printf '%s' "$submit" | jq -r '.task_id // .id // empty')
[ -n "$task_id" ] || { printf '%s\n' "$submit"; exit 1; }

sleep 3
for attempt in $(seq 1 180); do
  task=$(curl -sS '${baseUrl}/v1/images/tasks/'"$task_id" \\
    -H "Authorization: Bearer $NEW_API_KEY" \\
    -H "X-API-Key: $NEW_API_KEY" \\
    -H 'Accept: application/json')
  status=$(printf '%s' "$task" | jq -r '.status')

  case "$status" in
    succeeded|success|completed|done)
      curl -fL '${baseUrl}/v1/images/tasks/'"$task_id"'/content' \\
        -H "Authorization: Bearer $NEW_API_KEY" \\
        -o result.png
      exit 0
      ;;
    failed|failure|error|expired|cancelled)
      printf '%s\n' "$task" >&2
      exit 1
      ;;
  esac

  sleep 5
done

echo 'Task polling timed out' >&2
exit 1`,
    python: `import os
import time
from pathlib import Path

import requests

BASE_URL = "${baseUrl}"
API_KEY = os.environ["NEW_API_KEY"]
HEADERS = {"Authorization": f"Bearer {API_KEY}"}

submit = requests.post(
    f"{BASE_URL}/v1/images/generations?async=true",
    headers=HEADERS,
    json={
        "model": "all-image-2",
        "prompt": "Cinematic storyboard frame at sunrise",
        "n": 1,
        "size": "2048x1152",
        "quality": "medium",
    },
    timeout=60,
)
submit.raise_for_status()
submit_data = submit.json()
task_id = submit_data.get("task_id") or submit_data.get("id")
if not task_id:
    raise RuntimeError(f"Missing task_id: {submit_data}")

pending = {"queued", "pending", "submitted", "waiting", "running", "processing", "in_progress", "generating"}
succeeded = {"succeeded", "success", "completed", "done"}
failed = {"failed", "failure", "error", "expired", "cancelled"}
deadline = time.monotonic() + 15 * 60

time.sleep(3)
while time.monotonic() < deadline:
    response = requests.get(
        f"{BASE_URL}/v1/images/tasks/{task_id}",
        headers={**HEADERS, "X-API-Key": API_KEY, "Accept": "application/json"},
        timeout=30,
    )
    response.raise_for_status()
    task = response.json()
    status = str(task.get("status", "")).lower()

    if status in succeeded:
        content = requests.get(
            f"{BASE_URL}/v1/images/tasks/{task_id}/content",
            headers=HEADERS,
            timeout=120,
        )
        content.raise_for_status()
        Path("result.png").write_bytes(content.content)
        break
    if status in failed:
        raise RuntimeError(f"Image task failed: {task}")
    if status not in pending:
        raise RuntimeError(f"Unknown task status: {task}")

    time.sleep(5)
else:
    raise TimeoutError(f"Task {task_id} did not finish within 15 minutes")`,
    javascript: `import { writeFile } from 'node:fs/promises'

const baseURL = '${baseUrl}'
const apiKey = process.env.NEW_API_KEY
const headers = { Authorization: \`Bearer \${apiKey}\` }

const submitResponse = await fetch(
  \`\${baseURL}/v1/images/generations?async=true\`,
  {
    method: 'POST',
    headers: { ...headers, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model: 'all-image-2',
      prompt: 'Cinematic storyboard frame at sunrise',
      n: 1,
      size: '2048x1152',
      quality: 'medium',
    }),
  },
)
if (!submitResponse.ok) throw new Error(await submitResponse.text())
const submitted = await submitResponse.json()
const taskId = submitted.task_id ?? submitted.id
if (!taskId) throw new Error('The response did not include task_id')

const pending = new Set(['queued', 'pending', 'submitted', 'waiting', 'running', 'processing', 'in_progress', 'generating'])
const succeeded = new Set(['succeeded', 'success', 'completed', 'done'])
const failed = new Set(['failed', 'failure', 'error', 'expired', 'cancelled'])
const deadline = Date.now() + 15 * 60 * 1000

await new Promise((resolve) => setTimeout(resolve, 3000))
while (Date.now() < deadline) {
  const response = await fetch(\`\${baseURL}/v1/images/tasks/\${taskId}\`, {
    headers: { ...headers, 'X-API-Key': apiKey, Accept: 'application/json' },
  })
  if (!response.ok) throw new Error(await response.text())
  const task = await response.json()
  const status = String(task.status ?? '').toLowerCase()

  if (succeeded.has(status)) {
    const content = await fetch(
      \`\${baseURL}/v1/images/tasks/\${taskId}/content\`,
      { headers },
    )
    if (!content.ok) throw new Error(await content.text())
    await writeFile('result.png', Buffer.from(await content.arrayBuffer()))
    break
  }
  if (failed.has(status)) throw new Error(JSON.stringify(task))
  if (!pending.has(status)) throw new Error(\`Unknown task status: \${status}\`)

  await new Promise((resolve) => setTimeout(resolve, 5000))
}

if (Date.now() >= deadline) throw new Error('Task polling timed out')`,
  }
}

function buildCompressionSamples(): CodeSamples {
  return {
    curl: `# Resize and strip metadata before upload (ImageMagick)
magick reference.png \\
  -auto-orient \\
  -resize '2048x2048>' \\
  -strip \\
  -quality 85 \\
  reference-compressed.jpg

# Let curl generate the multipart boundary automatically
curl '$NEW_API_BASE/v1/images/edits' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -F 'model=all-image-2' \\
  -F 'prompt=Keep the character identity' \\
  -F 'image[]=@reference-compressed.jpg;type=image/jpeg' \\
  -F 'size=2048x1152' \\
  -F 'quality=medium'`,
    python: `from io import BytesIO
from pathlib import Path

from PIL import Image, ImageOps


def compress_image(path, max_edge=2048, quality=85):
    with Image.open(path) as source:
        image = ImageOps.exif_transpose(source).convert("RGB")
        image.thumbnail((max_edge, max_edge), Image.Resampling.LANCZOS)

        output = BytesIO()
        image.save(
            output,
            format="JPEG",
            quality=quality,
            optimize=True,
            progressive=True,
        )
        output.seek(0)
        return output


compressed = compress_image("reference.png")
Path("reference-compressed.jpg").write_bytes(compressed.getvalue())`,
    javascript: `import sharp from 'sharp'

await sharp('reference.png')
  .rotate()
  .resize({
    width: 2048,
    height: 2048,
    fit: 'inside',
    withoutEnlargement: true,
  })
  .jpeg({ quality: 85, progressive: true })
  .toFile('reference-compressed.jpg')`,
  }
}

function SectionHeading({
  id,
  eyebrow,
  title,
  description,
}: {
  id: string
  eyebrow: string
  title: string
  description?: string
}) {
  return (
    <div id={id} className='scroll-mt-24 pt-4'>
      <div className='text-primary mb-2 text-xs font-semibold tracking-[0.14em] uppercase'>
        {eyebrow}
      </div>
      <h2 className='text-2xl font-semibold tracking-tight sm:text-3xl'>
        {title}
      </h2>
      {description && (
        <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-7'>
          {description}
        </p>
      )}
    </div>
  )
}

function ParameterTable({ model }: { model: ImageModelDoc }) {
  const { t } = useTranslation()
  return (
    <div className='border-border/70 mt-4 overflow-hidden rounded-xl border'>
      <ScrollArea className='w-full'>
        <table className='w-full min-w-[860px] text-left text-sm'>
          <thead className='bg-muted/45 text-muted-foreground'>
            <tr>
              {[
                'Parameter',
                'Type',
                'Required',
                'Allowed values',
                'Description',
              ].map((label) => (
                <th key={label} className='px-4 py-3 font-medium'>
                  {t(label)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className='divide-border divide-y'>
            {model.parameters.map((parameter) => (
              <tr key={parameter.name} className='align-top'>
                <td className='px-4 py-3'>
                  <code className='text-primary'>{parameter.name}</code>
                </td>
                <td className='px-4 py-3'>
                  <code>{parameter.type}</code>
                </td>
                <td className='px-4 py-3'>{t(parameter.required)}</td>
                <td className='px-4 py-3'>
                  <code>{parameter.values}</code>
                </td>
                <td className='text-muted-foreground max-w-sm px-4 py-3 leading-6'>
                  {t(parameter.description)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <ScrollBar orientation='horizontal' />
      </ScrollArea>
    </div>
  )
}

function ModelSection({
  model,
  baseUrl,
  index,
}: {
  model: ImageModelDoc
  baseUrl: string
  index: number
}) {
  const { t } = useTranslation()
  const [operation, setOperation] = useState<ModelOperation>('generate')
  const samples = useMemo(() => {
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
  }, [baseUrl, model, operation])
  let resolutionNote = t(
    'Use the size mapping reference below when the OpenAI-compatible route requires exact pixels.'
  )
  if (
    model.family === 'openai' &&
    !model.model.startsWith('grok-imagine-image-')
  ) {
    resolutionNote = t(
      'The canvas ratio is derived from the exact pixel size. Do not send aspect_ratio, image_backend, or response_format.'
    )
  } else if (model.model.startsWith('grok-imagine-image-')) {
    resolutionNote = t(
      'Both Grok routes use resolution=1k|2k and aspect_ratio. grok-imagine-image-2.0 additionally sends quality=low|medium.'
    )
  } else if (model.family === 'oopii') {
    resolutionNote = t(
      'Dou 5.0 Pro expects an exact pixel size such as 2048x1152 and public reference URLs. Poll the generation ID returned with HTTP 202.'
    )
  }
  if (model.model === 'hy-image-v3.5-preview') {
    resolutionNote = t(
      'Use WIDTHxHEIGHT: each edge must be 256–8192 pixels and total area must not exceed 16777216 pixels. The canvas defaults to 1.5K.'
    )
  }
  if (model.modelIds) {
    resolutionNote = t(
      model.resolutions.length === 1
        ? 'apiDocs.image25.oneKSize'
        : 'apiDocs.image25.sizeDescription'
    )
  }
  let protocolLabel = 'OOpii async'
  if (model.family === 'openai') protocolLabel = 'OpenAI Images'
  if (model.family === 'gemini') protocolLabel = 'Gemini API'

  return (
    <section
      id={`model-${model.id}`}
      className='border-border/70 scroll-mt-20 border-t pt-12'
    >
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div className='min-w-0'>
          <div className='mb-3 flex flex-wrap items-center gap-2'>
            <Badge variant='secondary'>{model.protocol}</Badge>
            <span className='text-muted-foreground text-xs'>
              {t('Model reference')} {String(index + 1).padStart(2, '0')}
            </span>
          </div>
          <h2 className='font-mono text-2xl font-semibold tracking-tight break-all sm:text-3xl'>
            {model.title ?? model.model}
          </h2>
          <p className='text-muted-foreground mt-3 max-w-3xl text-sm leading-7'>
            {t(model.summary)}
          </p>
        </div>
        <Badge variant='outline' className='shrink-0'>
          {protocolLabel}
        </Badge>
      </div>

      {model.modelIds && (
        <div className='mt-5'>
          <h3 className='text-sm font-semibold'>
            {t('apiDocs.image25.modelIds')}
          </h3>
          <ul className='mt-2 space-y-1 text-sm'>
            {model.modelIds.map((id) => (
              <li key={id}>
                <code className='break-all'>{id}</code>
              </li>
            ))}
          </ul>
          <p className='text-muted-foreground mt-2 text-sm'>
            {t('apiDocs.image25.groupNote')}
          </p>
        </div>
      )}

      <div className='mt-6 grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]'>
        <div className='border-border/70 bg-card rounded-xl border p-5'>
          <div className='text-sm font-semibold'>{t('Recommended for')}</div>
          <p className='text-muted-foreground mt-2 text-sm leading-6'>
            {t(model.recommendedFor)}
          </p>
        </div>
        <div className='border-border/70 bg-card rounded-xl border p-5'>
          <div className='text-sm font-semibold'>{t('Capabilities')}</div>
          <div className='mt-3 flex flex-wrap gap-2'>
            <Badge variant='outline'>{t('Text to image')}</Badge>
            {model.supportsEditing && (
              <Badge variant='outline'>{t('Reference image editing')}</Badge>
            )}
            {model.resolutions.map((resolution) => (
              <Badge key={resolution} variant='outline'>
                {resolution}
              </Badge>
            ))}
          </div>
        </div>
      </div>

      <h3 className='mt-8 text-lg font-semibold'>{t('Endpoints')}</h3>
      <div className='mt-4 grid gap-3'>
        {model.endpoints.map((endpoint) => (
          <div
            key={endpoint.path}
            className='border-border/70 bg-card grid gap-3 rounded-xl border p-4 lg:grid-cols-[70px_minmax(0,1fr)_180px] lg:items-center'
          >
            <Badge
              className={cn(
                'w-fit text-white',
                endpoint.method === 'GET'
                  ? 'bg-sky-600 hover:bg-sky-600'
                  : 'bg-emerald-600 hover:bg-emerald-600'
              )}
            >
              {endpoint.method}
            </Badge>
            <div className='min-w-0'>
              <code className='block overflow-x-auto text-sm'>
                {endpoint.path.replace(
                  '{model}',
                  encodeURIComponent(model.model)
                )}
              </code>
              <p className='text-muted-foreground mt-1 text-xs leading-5'>
                {t(endpoint.purpose)}
              </p>
            </div>
            <code className='text-muted-foreground text-xs'>
              {endpoint.contentType}
            </code>
          </div>
        ))}
      </div>

      <h3 className='mt-8 text-lg font-semibold'>{t('Request parameters')}</h3>
      <ParameterTable model={model} />

      <div className='border-primary/20 bg-primary/5 mt-5 rounded-xl border p-4'>
        <div className='text-sm font-semibold'>
          {t('Verified request behavior')}
        </div>
        <ul className='text-muted-foreground mt-2 list-disc space-y-2 pl-5 text-sm leading-6'>
          {model.requestNotes.map((note) => (
            <li key={note}>{t(note)}</li>
          ))}
        </ul>
      </div>

      <div className='mt-8 grid gap-5 lg:grid-cols-2'>
        <div>
          <h3 className='text-lg font-semibold'>
            {t('Supported aspect ratios')}
          </h3>
          <div className='mt-3 flex flex-wrap gap-2'>
            {model.ratios.map((ratio) => (
              <code
                key={ratio}
                className='bg-muted rounded-md px-2 py-1 text-xs'
              >
                {ratio}
              </code>
            ))}
          </div>
        </div>
        <div>
          <h3 className='text-lg font-semibold'>
            {t('Supported resolutions')}
          </h3>
          <div className='mt-3 flex flex-wrap gap-2'>
            {model.resolutions.map((resolution) => (
              <code
                key={resolution}
                className='bg-muted rounded-md px-2 py-1 text-xs'
              >
                {resolution}
              </code>
            ))}
          </div>
          <p className='text-muted-foreground mt-3 text-xs leading-5'>
            {resolutionNote}
          </p>
        </div>
      </div>

      <div className='mt-8 flex flex-wrap items-center justify-between gap-3'>
        <h3 className='text-lg font-semibold'>
          {t('Complete request examples')}
        </h3>
        <Tabs
          value={operation}
          onValueChange={(value) => setOperation(value as ModelOperation)}
        >
          <TabsList>
            <TabsTrigger value='generate'>{t('Text to image')}</TabsTrigger>
            <TabsTrigger value='edit'>
              {t('Reference image editing')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <div className='mt-4'>
        <CodeExample
          samples={samples}
          title={`${model.model} ? ${t(operation === 'generate' ? 'Generate image' : 'Edit image')}`}
        />
      </div>
      {model.modelIds && (
        <div className='mt-6'>
          <h3 className='mb-3 text-lg font-semibold'>
            {t('apiDocs.image25.transparentExample')}
          </h3>
          <CodeExample
            samples={buildOpenAISamples(model, baseUrl, operation, true)}
            title={t('apiDocs.image25.transparentExample')}
          />
        </div>
      )}
      <div className='border-primary/20 bg-primary/5 mt-4 rounded-xl border px-4 py-3 text-sm'>
        <span className='font-medium'>{t('Image response path')}:</span>{' '}
        <code className='break-all'>{model.responsePath}</code>
      </div>
    </section>
  )
}

export function ApiDocs() {
  const { t } = useTranslation()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const baseUrl = 'https://www.oopii.cc'
  const modelSamples = useMemo<CodeSamples>(
    () => ({
      curl: `curl '${baseUrl}/v1/models' \\\n  -H "Authorization: Bearer $NEW_API_KEY"`,
      python: `import os\n\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${baseUrl}/v1",\n    api_key=os.environ["NEW_API_KEY"],\n)\n\nfor model in client.models.list().data:\n    print(model.id)`,
      javascript: `import OpenAI from 'openai'\n\nconst client = new OpenAI({\n  baseURL: '${baseUrl}/v1',\n  apiKey: process.env.NEW_API_KEY,\n})\n\nconst models = await client.models.list()\nconsole.log(models.data.map((model) => model.id))`,
    }),
    [baseUrl]
  )
  const asyncImageSamples = useMemo(
    () => buildAsyncImageSamples(baseUrl),
    [baseUrl]
  )
  const compressionSamples = useMemo(() => buildCompressionSamples(), [])

  return (
    <PublicLayout>
      <div className='bg-background min-h-[calc(100svh-4rem)]'>
        <div className='border-border/70 bg-background/95 sticky top-16 z-30 border-b backdrop-blur lg:hidden'>
          <div className='flex h-12 items-center justify-between px-4'>
            <div className='flex items-center gap-2 text-sm font-semibold'>
              <Code2 className='text-primary size-4' />
              {t('Model API documentation')}
            </div>
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={() => setMobileNavOpen((open) => !open)}
              aria-label={t('Toggle documentation navigation')}
              aria-expanded={mobileNavOpen}
              aria-controls='mobile-docs-navigation'
            >
              {mobileNavOpen ? (
                <X className='size-4' />
              ) : (
                <Menu className='size-4' />
              )}
            </Button>
          </div>
          {mobileNavOpen && (
            <div
              id='mobile-docs-navigation'
              className='bg-background border-t shadow-xl'
            >
              <ScrollArea className='h-[min(70svh,36rem)]'>
                <DocsNavigation onNavigate={() => setMobileNavOpen(false)} />
              </ScrollArea>
            </div>
          )}
        </div>

        <div className='mx-auto grid max-w-[1600px] lg:grid-cols-[286px_minmax(0,1fr)]'>
          <aside className='border-border/70 bg-muted/10 sticky top-16 hidden h-[calc(100svh-4rem)] border-r lg:block'>
            <div className='border-border/70 flex h-16 items-center gap-3 border-b px-6'>
              <div className='bg-primary/10 text-primary grid size-9 place-items-center rounded-xl'>
                <Code2 className='size-4' />
              </div>
              <div>
                <div className='text-sm font-semibold'>
                  {t('Model API documentation')}
                </div>
                <div className='text-muted-foreground text-xs'>
                  OOpii · new-api
                </div>
              </div>
            </div>
            <ScrollArea className='h-[calc(100%-4rem)]'>
              <DocsNavigation />
            </ScrollArea>
          </aside>

          <main className='min-w-0 px-4 py-10 sm:px-8 lg:px-10 xl:px-14'>
            <div className='mx-auto max-w-5xl'>
              <section id='overview' className='scroll-mt-24'>
                <div className='flex flex-wrap items-center gap-2'>
                  <Badge variant='secondary'>OOpii</Badge>
                  <Badge variant='outline'>OpenAI Images</Badge>
                  <Badge variant='outline'>Gemini generateContent</Badge>
                  <Badge variant='outline'>Midjourney v8.2</Badge>
                  <Badge variant='outline'>Seed Audio</Badge>
                  <Badge variant='outline'>{t('Video models')}</Badge>
                </div>
                <h1 className='mt-5 max-w-4xl text-3xl font-semibold tracking-tight sm:text-5xl'>
                  {t('OOpii developer API reference')}
                </h1>
                <p className='text-muted-foreground mt-5 max-w-3xl text-base leading-8'>
                  {t(
                    'Connect image, audio and video models with exact API routes, parameter tables and runnable examples.'
                  )}
                </p>
                <div className='mt-8 grid gap-4 sm:grid-cols-3'>
                  {[
                    [
                      String(
                        imageModels.reduce(
                          (count, model) =>
                            count + (model.modelIds?.length ?? 1),
                          2
                        )
                      ),
                      'Image and audio models',
                    ],
                    [
                      String(
                        videoGroups.reduce(
                          (count, group) =>
                            count +
                            group.variants.reduce(
                              (sum, variant) => sum + variant.models.length,
                              0
                            ),
                          0
                        )
                      ),
                      'Video models',
                    ],
                    ['3', 'Example languages'],
                  ].map(([value, label]) => (
                    <div
                      key={label}
                      className='border-border/70 bg-card rounded-xl border p-5'
                    >
                      <div className='text-2xl font-semibold'>{value}</div>
                      <div className='text-muted-foreground mt-1 text-xs'>
                        {t(label)}
                      </div>
                    </div>
                  ))}
                </div>
                <IntegrationExport baseUrl={baseUrl} />
                <div className='mt-5 flex flex-wrap gap-3 text-sm'>
                  {[
                    ['seed-audio', 'Audio generation'],
                    ['midjourney-image', 'Midjourney independent image API'],
                    ['video-api', 'Video task API'],
                  ].map(([id, label]) => (
                    <a
                      key={id}
                      className='text-primary hover:bg-muted rounded-lg border px-3 py-2'
                      href={`#${id}`}
                    >
                      {t(label)}
                    </a>
                  ))}
                </div>
              </section>

              <section className='mt-14'>
                <SectionHeading
                  id='authentication'
                  eyebrow={`01 · ${t('Get started')}`}
                  title={t('Authentication')}
                  description={t(
                    'Send a new-api token in the Authorization header for model discovery and generation requests. Keep the token on the server.'
                  )}
                />
                <div className='border-border/70 bg-card mt-6 rounded-xl border p-5'>
                  <div className='flex items-start gap-3'>
                    <div className='bg-primary/10 text-primary mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg'>
                      <KeyRound className='size-4' />
                    </div>
                    <div className='min-w-0'>
                      <div className='font-semibold'>Authorization: Bearer</div>
                      <code className='bg-muted mt-3 block overflow-x-auto rounded-lg px-4 py-3 text-sm'>
                        Authorization: Bearer $NEW_API_KEY
                      </code>
                      <p className='text-muted-foreground mt-3 text-sm leading-6'>
                        {t(
                          'The examples use the OOpii new-api deployment at {{baseUrl}}.',
                          { baseUrl }
                        )}
                      </p>
                    </div>
                  </div>
                </div>
              </section>

              <section className='border-border/70 mt-16 border-t pt-12'>
                <SectionHeading
                  id='oopii-model-catalog'
                  eyebrow='OOpii'
                  title={t('Current OOpii catalog snapshot')}
                  description={t(
                    'Audio, independent Midjourney and video contracts checked on 2026-10-22. Query /v1/models for account-specific availability.'
                  )}
                />
                <div className='mt-6 grid gap-6'>
                  {(
                    [
                      ['text', 'Text model families'],
                      ['image', 'Image models'],
                      ['audio', 'Audio generation'],
                      ['video', 'Video models'],
                      ['midjourney', 'Midjourney models'],
                    ] as const
                  ).map(([group, label]) => (
                    <div
                      key={group}
                      className='border-border/70 bg-card overflow-hidden rounded-xl border'
                    >
                      <div className='border-border/70 bg-muted/25 flex items-center justify-between gap-3 border-b px-4 py-3'>
                        <h3 className='font-semibold'>{t(label)}</h3>
                        <Badge variant='secondary'>
                          {oopiiModelGroups[group].length}
                        </Badge>
                      </div>
                      <ScrollArea className='w-full'>
                        <table className='w-full min-w-[720px] text-left text-sm'>
                          <thead className='bg-muted/35 text-muted-foreground'>
                            <tr>
                              {[
                                group === 'text' ? 'Model family' : 'Model',
                                'Provider',
                                'Protocol',
                              ].map((heading) => (
                                <th
                                  key={heading}
                                  className='px-4 py-3 font-medium'
                                >
                                  {t(heading)}
                                </th>
                              ))}
                            </tr>
                          </thead>
                          <tbody className='divide-border divide-y'>
                            {oopiiModelGroups[group].map((model) => (
                              <tr key={model.id} className='align-top'>
                                <td className='px-4 py-3'>
                                  <a
                                    href={`#${modelSectionIds.get(model.id) ?? 'models-api'}`}
                                    className='text-primary focus-visible:outline-ring rounded-sm underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4'
                                  >
                                    {group === 'text' ? (
                                      <span className='font-medium'>
                                        {model.id}
                                      </span>
                                    ) : (
                                      <code className='text-primary break-all'>
                                        {model.id}
                                      </code>
                                    )}
                                  </a>
                                </td>
                                <td className='px-4 py-3'>{model.owner}</td>
                                <td className='px-4 py-3'>
                                  <div className='flex flex-wrap gap-1.5'>
                                    {model.endpoints.map((endpoint) => (
                                      <Badge key={endpoint} variant='outline'>
                                        {endpoint}
                                      </Badge>
                                    ))}
                                  </div>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                        <ScrollBar orientation='horizontal' />
                      </ScrollArea>
                    </div>
                  ))}
                </div>
                <div className='border-border/70 bg-muted/20 mt-4 rounded-xl border p-4 text-sm leading-6'>
                  {t(
                    'Text models are grouped by provider and family. Query /v1/models for the exact model IDs available to your API key.'
                  )}
                </div>
              </section>

              <section className='mt-16'>
                <SectionHeading
                  id='text-api'
                  eyebrow='OOpii'
                  title={t('Text and multimodal API routes')}
                  description={t(
                    'The OOpii text catalog currently includes OpenAI-compatible chat models, Responses-compatible models, Claude models with Anthropic and OpenAI routes, and Gemini models with Gemini and OpenAI routes.'
                  )}
                />
                <div className='border-border/70 mt-6 overflow-hidden rounded-xl border'>
                  <ScrollArea className='w-full'>
                    <table className='w-full min-w-[820px] text-left text-sm'>
                      <thead className='bg-muted/45 text-muted-foreground'>
                        <tr>
                          {['Protocol', 'Method', 'Endpoint', 'Models'].map(
                            (heading) => (
                              <th
                                key={heading}
                                className='px-4 py-3 font-medium'
                              >
                                {t(heading)}
                              </th>
                            )
                          )}
                        </tr>
                      </thead>
                      <tbody className='divide-border divide-y'>
                        {textApiRoutes.map((route) => (
                          <tr key={route.path} className='align-top'>
                            <td className='px-4 py-3 font-medium'>
                              {t(route.name)}
                            </td>
                            <td className='px-4 py-3'>
                              <Badge>{route.method}</Badge>
                            </td>
                            <td className='px-4 py-3'>
                              <code>{route.path}</code>
                            </td>
                            <td className='text-muted-foreground px-4 py-3 leading-6'>
                              {route.models}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation='horizontal' />
                  </ScrollArea>
                </div>
                <p className='text-muted-foreground mt-4 text-sm leading-6'>
                  {t(
                    'Audio, independent Midjourney and video contracts checked on 2026-10-22. Query /v1/models for account-specific availability.'
                  )}
                </p>
              </section>

              <GenerationDocs baseUrl={baseUrl} />
              <VideoDocs baseUrl={baseUrl} />

              <div className='mt-16 space-y-16'>
                {imageModels.map((model, index) => (
                  <ModelSection
                    key={model.id}
                    model={model}
                    baseUrl={baseUrl}
                    index={index}
                  />
                ))}
              </div>

              <MidjourneyDocs baseUrl={baseUrl} />

              <section className='border-border/70 mt-16 border-t pt-12'>
                <SectionHeading
                  id='models-api'
                  eyebrow='API REFERENCE'
                  title={t('Query available models')}
                  description={t(
                    'Call this endpoint after adding an OOpii channel to confirm which model IDs are currently exposed to clients. A successful response follows the OpenAI model-list format.'
                  )}
                />
                <div className='border-border/70 bg-card mt-5 grid gap-3 rounded-xl border p-4 sm:grid-cols-[70px_minmax(0,1fr)_180px] sm:items-center'>
                  <Badge className='w-fit bg-sky-600 text-white hover:bg-sky-600'>
                    GET
                  </Badge>
                  <code className='overflow-x-auto'>/v1/models</code>
                  <code className='text-muted-foreground text-xs'>
                    application/json
                  </code>
                </div>
                <div className='mt-4'>
                  <CodeExample
                    samples={modelSamples}
                    title={t('List models')}
                  />
                </div>
                <div className='mt-4 rounded-xl border border-amber-500/25 bg-amber-500/5 p-4 text-sm leading-6'>
                  <strong>{t('Channel configuration note')}:</strong>{' '}
                  {t(
                    'The channel base URL may include or omit a trailing slash. new-api normalizes it before requesting /v1/models so OOpii does not receive a double-slash path.'
                  )}
                </div>
                <div className='border-border/70 bg-muted/20 mt-3 rounded-xl border p-4 text-sm leading-6'>
                  <strong>{t('Why a configured model may be missing')}:</strong>{' '}
                  {t(
                    'The client-facing /v1/models response is filtered by the token model limit, user or token group abilities, enabled channel models, and billing configuration. Unless self-use mode or Accept unset ratio model is enabled, models without billing configuration are omitted.'
                  )}
                </div>
              </section>

              <section className='mt-16'>
                <SectionHeading
                  id='async-images'
                  eyebrow='API REFERENCE'
                  title={t('Asynchronous image tasks')}
                  description={t(
                    'Use asynchronous mode for slow, high-resolution, or reference-heavy generations. Submit once, save the task ID, poll the status endpoint, and download the completed image.'
                  )}
                />

                <div className='border-primary/20 bg-primary/5 mt-6 rounded-xl border p-4 text-sm leading-6'>
                  <strong>{t('Storyboard Copilot behavior')}:</strong>{' '}
                  {t(
                    'Follow the synchronous or asynchronous workflow shown for each model. Only poll when the submission returns a task ID; synchronous responses contain the image result directly.'
                  )}
                </div>

                <div className='mt-6 grid gap-3'>
                  {[
                    [
                      'POST',
                      '/v1/images/generations?async=true',
                      'Submit an OpenAI-compatible generation task.',
                    ],
                    [
                      'POST',
                      '/v1/images/edits?async=true',
                      'Submit a multipart reference-image editing task.',
                    ],
                    [
                      'POST',
                      '/v1beta/models/{model}:generateContent?async=true',
                      'Submit a Gemini-compatible image task.',
                    ],
                    [
                      'GET',
                      '/v1/images/tasks/{task_id}',
                      'Read status, error, expiration, and result URLs.',
                    ],
                    [
                      'GET',
                      '/v1/images/tasks/{task_id}/content',
                      'Download completed content with API authentication.',
                    ],
                    [
                      'GET',
                      '/v1/images/tasks/{task_id}/signed-content?token={signed_token}',
                      'Download completed content without Authorization by using the temporary signed URL returned by the status response.',
                    ],
                  ].map(([method, path, purpose]) => (
                    <div
                      key={path}
                      className='border-border/70 bg-card grid gap-3 rounded-xl border p-4 sm:grid-cols-[70px_minmax(0,1fr)] sm:items-center'
                    >
                      <Badge
                        className={cn(
                          'w-fit text-white',
                          method === 'GET'
                            ? 'bg-sky-600 hover:bg-sky-600'
                            : 'bg-emerald-600 hover:bg-emerald-600'
                        )}
                      >
                        {method}
                      </Badge>
                      <div className='min-w-0'>
                        <code className='block overflow-x-auto text-sm'>
                          {path}
                        </code>
                        <p className='text-muted-foreground mt-1 text-xs leading-5'>
                          {t(purpose)}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>

                <div className='mt-6 grid gap-4 md:grid-cols-3'>
                  {[
                    [
                      '1',
                      'Submit once',
                      'Append async=true and store task_id. For compatibility, clients may fall back to id.',
                    ],
                    [
                      '2',
                      'Poll safely',
                      'Wait 3 seconds before the first query, then poll every 3 to 5 seconds for up to 10 to 15 minutes.',
                    ],
                    [
                      '3',
                      'Download promptly',
                      'After success, download with authentication or use the signed URL returned in url before it expires.',
                    ],
                  ].map(([step, title, description]) => (
                    <div
                      key={step}
                      className='border-border/70 bg-card rounded-xl border p-5'
                    >
                      <div className='bg-primary/10 text-primary grid size-8 place-items-center rounded-full text-sm font-semibold'>
                        {step}
                      </div>
                      <h3 className='mt-4 font-semibold'>{t(title)}</h3>
                      <p className='text-muted-foreground mt-2 text-sm leading-6'>
                        {t(description)}
                      </p>
                    </div>
                  ))}
                </div>

                <div className='border-border/70 mt-6 overflow-hidden rounded-xl border'>
                  <ScrollArea className='w-full'>
                    <table className='w-full min-w-[760px] text-left text-sm'>
                      <thead className='bg-muted/45 text-muted-foreground'>
                        <tr>
                          {[
                            'Task phase',
                            'Canonical status',
                            'Compatible status values',
                            'Client action',
                          ].map((label) => (
                            <th key={label} className='px-4 py-3 font-medium'>
                              {t(label)}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className='divide-border divide-y'>
                        {[
                          [
                            'Submitted',
                            'submitted / queued',
                            'pending, waiting',
                            'Continue polling.',
                          ],
                          [
                            'Processing',
                            'processing',
                            'running, in_progress, generating',
                            'Continue polling without creating another task.',
                          ],
                          [
                            'Succeeded',
                            'succeeded',
                            'success, completed, done',
                            'Download and persist the image immediately.',
                          ],
                          [
                            'Failed',
                            'failed / expired',
                            'failure, error, cancelled',
                            'Stop polling and record the status plus error body.',
                          ],
                        ].map(([phase, canonical, aliases, action]) => (
                          <tr key={phase} className='align-top'>
                            <td className='px-4 py-3 font-medium'>
                              {t(phase)}
                            </td>
                            <td className='px-4 py-3'>
                              <code>{canonical}</code>
                            </td>
                            <td className='px-4 py-3'>
                              <code>{aliases}</code>
                            </td>
                            <td className='text-muted-foreground px-4 py-3 leading-6'>
                              {t(action)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation='horizontal' />
                  </ScrollArea>
                </div>

                <div className='mt-6'>
                  <CodeExample
                    samples={asyncImageSamples}
                    title={t('Submit, poll, and download')}
                  />
                </div>

                <div className='mt-4 flex gap-3 rounded-xl border border-amber-500/25 bg-amber-500/5 p-4 text-sm leading-6'>
                  <ShieldAlert className='mt-0.5 size-4 shrink-0 text-amber-600' />
                  <p>
                    <strong>{t('Important polling rule')}:</strong>{' '}
                    {t(
                      'After a task is accepted, do not submit the same generation again. Keep querying the existing task ID. High-frequency polling can trigger rate limits, and 4K tasks may require a longer deadline.'
                    )}
                  </p>
                </div>

                <div className='border-border/70 bg-muted/20 mt-4 rounded-xl border p-4 text-sm leading-6'>
                  <strong>{t('Download authentication')}:</strong>{' '}
                  {t(
                    'The content_url endpoint normally requires Authorization. A successful task may also return a temporary signed URL in url with url_expires_at; signed-content URLs must be used before expiration.'
                  )}
                </div>
              </section>

              <section className='mt-16'>
                <SectionHeading
                  id='image-compression'
                  eyebrow='UPLOAD GUIDE'
                  title={t('Image compression and upload')}
                  description={t(
                    'Compress reference images before multipart or Base64 upload to reduce 413 responses, request timeouts, memory pressure, and upstream decoding failures.'
                  )}
                />

                <div className='mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
                  {[
                    ['2048 px', 'Recommended longest edge'],
                    ['2-4 MB', 'Recommended size per image'],
                    ['80-88%', 'JPEG quality range'],
                    ['+33%', 'Approximate Base64 overhead'],
                  ].map(([value, label]) => (
                    <div
                      key={label}
                      className='border-border/70 bg-card rounded-xl border p-5'
                    >
                      <div className='text-2xl font-semibold'>{value}</div>
                      <div className='text-muted-foreground mt-2 text-xs leading-5'>
                        {t(label)}
                      </div>
                    </div>
                  ))}
                </div>

                <div className='mt-6 grid gap-4 lg:grid-cols-3'>
                  {[
                    [
                      FileImage,
                      'JPEG for photographs',
                      'Use quality 80 to 88 for photographic references without transparency. Convert oversized PNG photographs to JPEG before Base64 encoding.',
                    ],
                    [
                      Upload,
                      'WebP for smaller files',
                      'Use quality 80 to 85 when the client and upstream route accept WebP. It often reduces upload size while preserving visual detail.',
                    ],
                    [
                      Layers3,
                      'PNG for transparency',
                      'Keep PNG for masks, line art, logos, alpha channels, or pixel-exact assets. Resize and strip metadata even when PNG must be preserved.',
                    ],
                  ].map(([Icon, title, description]) => (
                    <div
                      key={title as string}
                      className='border-border/70 bg-card rounded-xl border p-5'
                    >
                      <Icon className='text-primary size-5' />
                      <h3 className='mt-4 font-semibold'>
                        {t(title as string)}
                      </h3>
                      <p className='text-muted-foreground mt-2 text-sm leading-6'>
                        {t(description as string)}
                      </p>
                    </div>
                  ))}
                </div>

                <div className='border-border/70 mt-6 overflow-hidden rounded-xl border'>
                  <ScrollArea className='w-full'>
                    <table className='w-full min-w-[820px] text-left text-sm'>
                      <thead className='bg-muted/45 text-muted-foreground'>
                        <tr>
                          {[
                            'Upload method',
                            'Image field',
                            'Required encoding',
                            'Common mistake',
                          ].map((label) => (
                            <th key={label} className='px-4 py-3 font-medium'>
                              {t(label)}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className='divide-border divide-y'>
                        {[
                          [
                            'OpenAI image edits',
                            'image / image[]',
                            'multipart/form-data file',
                            'Do not set a fixed multipart boundary manually.',
                          ],
                          [
                            'Gemini inlineData',
                            'inlineData.data',
                            'Raw Base64 only',
                            'Remove the data:image/png;base64, prefix.',
                          ],
                          [
                            'Grok reference images',
                            'image / images',
                            'Complete data URL',
                            'Include the correct MIME prefix for each image.',
                          ],
                          [
                            'Public image URL',
                            'image_url or provider field',
                            'Publicly reachable HTTPS URL',
                            'Private, localhost, expiring, or login-only URLs cannot be fetched upstream.',
                          ],
                        ].map(([method, field, encoding, mistake]) => (
                          <tr key={method} className='align-top'>
                            <td className='px-4 py-3 font-medium'>
                              {t(method)}
                            </td>
                            <td className='px-4 py-3'>
                              <code>{field}</code>
                            </td>
                            <td className='px-4 py-3'>{t(encoding)}</td>
                            <td className='text-muted-foreground px-4 py-3 leading-6'>
                              {t(mistake)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation='horizontal' />
                  </ScrollArea>
                </div>

                <div className='mt-6'>
                  <CodeExample
                    samples={compressionSamples}
                    title={t('Compress a reference image')}
                  />
                </div>

                <div className='mt-6 grid gap-3 sm:grid-cols-2'>
                  {[
                    [
                      '413 - Payload too large',
                      'Reduce the number of references, resize the longest edge, convert photographic PNG files to JPEG or WebP, and lower quality gradually.',
                    ],
                    [
                      '400 - Invalid image data',
                      'Verify the MIME type, Base64 prefix rule, file integrity, and whether the selected endpoint expects JSON or multipart data.',
                    ],
                    [
                      '502 / 504 - Upstream timeout',
                      'Compress the images, reduce 4K reference inputs, use asynchronous mode, and retry with a bounded backoff.',
                    ],
                    [
                      'Memory or Base64 failures',
                      'Account for about 33 percent Base64 expansion and avoid holding multiple uncompressed 4K or 8K images in memory at once.',
                    ],
                  ].map(([title, description]) => (
                    <div
                      key={title}
                      className='border-border/70 bg-muted/20 rounded-xl border p-4'
                    >
                      <div className='flex items-center gap-2 font-semibold'>
                        <Download className='text-primary size-4' />
                        {t(title)}
                      </div>
                      <p className='text-muted-foreground mt-2 text-sm leading-6'>
                        {t(description)}
                      </p>
                    </div>
                  ))}
                </div>

                <p className='text-muted-foreground mt-4 text-xs leading-6'>
                  {t(
                    'These values are production recommendations rather than fixed provider limits. For identity, style, or composition references, 1536 px is often sufficient; avoid uploading original 4K or 8K files unless fine detail is essential.'
                  )}
                </p>
              </section>

              <section className='mt-16'>
                <SectionHeading
                  id='size-mapping'
                  eyebrow='API REFERENCE'
                  title={t('Size mapping')}
                  description={t(
                    'Use these common Storyboard Copilot mappings when an OpenAI-compatible model accepts exact width and height through the size parameter.'
                  )}
                />
                <div className='border-border/70 mt-6 overflow-hidden rounded-xl border'>
                  <ScrollArea className='w-full'>
                    <table className='w-full min-w-[520px] text-left text-sm'>
                      <thead className='bg-muted/45 text-muted-foreground'>
                        <tr>
                          {['Resolution', 'Aspect ratio', 'Pixel size'].map(
                            (label) => (
                              <th key={label} className='px-4 py-3 font-medium'>
                                {t(label)}
                              </th>
                            )
                          )}
                        </tr>
                      </thead>
                      <tbody className='divide-border divide-y'>
                        {sizeRows.map(([resolution, ratio, size]) => (
                          <tr key={`${resolution}-${ratio}`}>
                            <td className='px-4 py-3'>
                              <code>{resolution}</code>
                            </td>
                            <td className='px-4 py-3'>
                              <code>{ratio}</code>
                            </td>
                            <td className='px-4 py-3'>
                              <code>{size}</code>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation='horizontal' />
                  </ScrollArea>
                </div>
              </section>

              <section className='mt-16'>
                <SectionHeading
                  id='response-format'
                  eyebrow='API REFERENCE'
                  title={t('Response format')}
                  description={t(
                    'Read the image URL or base64 field that matches the selected compatibility protocol.'
                  )}
                />
                <div className='mt-6 grid gap-4 lg:grid-cols-2'>
                  <div className='border-border/70 bg-card rounded-xl border p-5'>
                    <div className='flex items-center gap-2 font-semibold'>
                      <ImageIcon className='text-primary size-4' /> OpenAI
                      Images
                    </div>
                    <p className='text-muted-foreground mt-2 text-xs'>
                      {t('URL response')}
                    </p>
                    <pre className='bg-muted/45 mt-4 overflow-x-auto rounded-lg p-4 text-xs leading-6'>{`{\n  "created": 1710000000,\n  "data": [{\n    "url": "https://.../image.png"\n  }]\n}`}</pre>
                  </div>
                  <div className='border-border/70 bg-card rounded-xl border p-5'>
                    <div className='flex items-center gap-2 font-semibold'>
                      <FileJson className='text-primary size-4' /> Gemini
                    </div>
                    <p className='text-muted-foreground mt-2 text-xs'>
                      {t('Inline base64 response')}
                    </p>
                    <pre className='bg-muted/45 mt-4 overflow-x-auto rounded-lg p-4 text-xs leading-6'>{`{\n  "candidates": [{\n    "content": {\n      "parts": [{\n        "inlineData": {\n          "mimeType": "image/png",\n          "data": "...base64..."\n        }\n      }]\n    }\n  }]\n}`}</pre>
                  </div>
                </div>
              </section>

              <section className='mt-16 pb-16'>
                <SectionHeading
                  id='errors'
                  eyebrow='API REFERENCE'
                  title={t('Errors and status codes')}
                  description={t(
                    'Handle authentication, validation, quota, and upstream failures explicitly. Error bodies may include a message, type, parameter, and request identifier.'
                  )}
                />
                <div className='border-border/70 mt-6 overflow-hidden rounded-xl border'>
                  <ScrollArea className='w-full'>
                    <table className='w-full min-w-[700px] text-left text-sm'>
                      <thead className='bg-muted/45 text-muted-foreground'>
                        <tr>
                          {['Status', 'Meaning', 'Recommended action'].map(
                            (label) => (
                              <th key={label} className='px-4 py-3 font-medium'>
                                {t(label)}
                              </th>
                            )
                          )}
                        </tr>
                      </thead>
                      <tbody className='divide-border divide-y'>
                        {[
                          [
                            '400',
                            'Invalid request parameters',
                            'Check model ID, ratio, size, content type, and required fields.',
                          ],
                          [
                            '401',
                            'Invalid or missing API token',
                            'Confirm the Bearer token and the token status.',
                          ],
                          [
                            '403',
                            'Model or channel access denied',
                            'Check token model permissions and channel group access.',
                          ],
                          [
                            '429',
                            'Rate limit or insufficient quota',
                            'Retry with backoff or add quota before retrying.',
                          ],
                          [
                            '500 / 502',
                            'Gateway or upstream processing error',
                            'Log the request ID and retry only idempotent generation tasks.',
                          ],
                          [
                            '503 / 504',
                            'Upstream unavailable or timed out',
                            'Retry with exponential backoff and a bounded attempt count.',
                          ],
                        ].map(([statusCode, meaning, action]) => (
                          <tr key={statusCode} className='align-top'>
                            <td className='px-4 py-3'>
                              <Badge variant='outline'>{statusCode}</Badge>
                            </td>
                            <td className='px-4 py-3 font-medium'>
                              {t(meaning)}
                            </td>
                            <td className='text-muted-foreground px-4 py-3 leading-6'>
                              {t(action)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation='horizontal' />
                  </ScrollArea>
                </div>
                <div className='border-border/70 bg-muted/20 mt-6 flex gap-3 rounded-xl border p-4 text-sm leading-6'>
                  <Search className='text-primary mt-0.5 size-4 shrink-0' />
                  <p>
                    <strong>{t('Troubleshooting order')}:</strong>{' '}
                    {t(
                      'first call /v1/models, then verify the exact model ID, endpoint family, request Content-Type, and the response path documented for that model.'
                    )}
                  </p>
                </div>
              </section>
            </div>
          </main>
        </div>
      </div>
    </PublicLayout>
  )
}
