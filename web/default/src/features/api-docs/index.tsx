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
  Check,
  ChevronRight,
  Clipboard,
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
  Sparkles,
  Terminal,
  Upload,
  X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { PublicLayout } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useStatus } from "@/hooks/use-status";
import { cn } from "@/lib/utils";

import { type ImageModelDoc, imageModels, sizeRows } from "./data";

type CodeLanguage = "curl" | "python" | "javascript";
type ModelOperation = "generate" | "edit";
type CodeSamples = Record<CodeLanguage, string>;

const referenceItems = [
  ["models-api", "Query available models"],
  ["async-images", "Asynchronous image tasks"],
  ["image-compression", "Image compression and upload"],
  ["size-mapping", "Size mapping"],
  ["response-format", "Response format"],
  ["errors", "Errors and status codes"],
] as const;

function resolveBaseUrl(status: unknown) {
  const source = status as Record<string, unknown> | null;
  const nested = source?.data as Record<string, unknown> | undefined;
  const candidate =
    source?.server_address ??
    source?.serverAddress ??
    nested?.server_address ??
    nested?.serverAddress;

  if (typeof candidate === "string" && candidate.trim()) {
    return candidate.trim().replace(/\/+$/, "");
  }
  if (typeof window !== "undefined") return window.location.origin;
  return "https://api.example.com";
}

function endpointFor(model: ImageModelDoc) {
  if (model.family === "openai") return "/v1/images/generations";
  return `/v1beta/models/${encodeURIComponent(model.model)}:generateContent`;
}

function buildOpenAISamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation,
): CodeSamples {
  const usesAspectRatio = model.model !== "gpt-image-2";
  const aspectRatioJson = usesAspectRatio
    ? ',\n    "aspect_ratio": "16:9"'
    : "";
  const aspectRatioForm = usesAspectRatio
    ? " \\\n  -F 'aspect_ratio=16:9'"
    : "";
  const aspectRatioPython = usesAspectRatio
    ? ',\n        "aspect_ratio": "16:9",'
    : "";
  const aspectRatioJs = usesAspectRatio
    ? "\nform.append('aspect_ratio', '16:9')"
    : "";

  if (operation === "edit") {
    return {
      curl: `curl '${baseUrl}/v1/images/edits' \\
  -H 'Authorization: Bearer $NEW_API_KEY' \\
  -F 'model=${model.model}' \\
  -F 'prompt=Keep the character identity and change the scene to sunrise' \\
  -F 'n=1' \\
  -F 'image[]=@reference-1.png' \\
  -F 'image[]=@reference-2.png' \\
  -F 'size=2048x1152'${aspectRatioForm} \\
  -F 'quality=medium'`,
      python: `import os\n\nimport requests\n\nwith open("reference-1.png", "rb") as first, open("reference-2.png", "rb") as second:\n    response = requests.post(\n        "${baseUrl}/v1/images/edits",\n        headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n        data={\n            "model": "${model.model}",\n            "prompt": "Keep the character identity and change the scene to sunrise",\n            "n": "1",\n            "size": "2048x1152",${aspectRatioPython}\n            "quality": "medium",\n        },\n        files=[\n            ("image[]", ("reference-1.png", first, "image/png")),\n            ("image[]", ("reference-2.png", second, "image/png")),\n        ],\n        timeout=180,\n    )\n\nresponse.raise_for_status()\nprint(response.json())`,
      javascript: `import { readFile } from 'node:fs/promises'\n\nconst form = new FormData()\nform.append('model', '${model.model}')\nform.append('prompt', 'Keep the character identity and change the scene to sunrise')\nform.append('n', '1')\nform.append('size', '2048x1152')${aspectRatioJs}\nform.append('quality', 'medium')\nform.append('image[]', new Blob([await readFile('reference-1.png')], { type: 'image/png' }), 'reference-1.png')\nform.append('image[]', new Blob([await readFile('reference-2.png')], { type: 'image/png' }), 'reference-2.png')\n\nconst response = await fetch('${baseUrl}/v1/images/edits', {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${process.env.NEW_API_KEY}\` },\n  body: form,\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
    };
  }

  return {
    curl: `curl '${baseUrl}/v1/images/generations' \\
  -H 'Authorization: Bearer $NEW_API_KEY' \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "model": "${model.model}",\n    "prompt": "Cinematic storyboard frame of a detective in a rainy neon alley",\n    "n": 1,\n    "size": "2048x1152"${aspectRatioJson},\n    "quality": "medium"\n  }'`,
    python: `import os\n\nimport requests\n\npayload = {\n    "model": "${model.model}",\n    "prompt": "Cinematic storyboard frame of a detective in a rainy neon alley",\n    "n": 1,\n    "size": "2048x1152",${aspectRatioPython}\n    "quality": "medium",\n}\nresponse = requests.post(\n    "${baseUrl}/v1/images/generations",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    javascript: `const response = await fetch('${baseUrl}/v1/images/generations', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    model: '${model.model}',\n    prompt: 'Cinematic storyboard frame of a detective in a rainy neon alley',\n    n: 1,\n    size: '2048x1152',${usesAspectRatio ? "\n    aspect_ratio: '16:9'," : ""}\n    quality: 'medium',\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
  };
}

function buildGrokSamples(
  baseUrl: string,
  operation: ModelOperation,
): CodeSamples {
  const prompt =
    operation === "edit"
      ? "Keep the character identity and change the scene to sunrise \u6bd4\u4f8b16:9"
      : "Cinematic storyboard frame of a detective in a rainy neon alley \u6bd4\u4f8b16:9";
  const requestSize = operation === "edit" ? "16:9" : "1280x720";
  const referenceJson =
    operation === "edit"
      ? ',\n    "reference_images": ["data:image/png;base64,BASE64_REFERENCE_IMAGE"],\n    "output_resolution": "1K",\n    "resolution": "1K",\n    "image_size": "1K",\n    "aspectRatio": "16:9",\n    "generationConfig": {\n      "imageConfig": {\n        "imageSize": "1K",\n        "aspectRatio": "16:9",\n        "size": "16:9"\n      }\n    }'
      : "";
  const pythonReference =
    operation === "edit"
      ? `\nimport base64\n\nwith open("reference.png", "rb") as file:\n    reference = "data:image/png;base64," + base64.b64encode(file.read()).decode("ascii")\n`
      : "";
  const pythonFields =
    operation === "edit"
      ? `\n    "reference_images": [reference],\n    "output_resolution": "1K",\n    "resolution": "1K",\n    "image_size": "1K",\n    "aspectRatio": "16:9",\n    "generationConfig": {\n        "imageConfig": {\n            "imageSize": "1K",\n            "aspectRatio": "16:9",\n            "size": "16:9",\n        }\n    },`
      : "";
  const jsReference =
    operation === "edit"
      ? `import { readFile } from 'node:fs/promises'\n\nconst reference = 'data:image/png;base64,' + (await readFile('reference.png')).toString('base64')\n\n`
      : "";
  const jsFields =
    operation === "edit"
      ? `\n    reference_images: [reference],\n    output_resolution: '1K',\n    resolution: '1K',\n    image_size: '1K',\n    aspectRatio: '16:9',\n    generationConfig: {\n      imageConfig: { imageSize: '1K', aspectRatio: '16:9', size: '16:9' },\n    },`
      : "";

  return {
    curl: `curl '${baseUrl}/v1/images/generations?async=true' \\
  -H 'Authorization: Bearer $NEW_API_KEY' \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "model": "grok-imagine-image-pro",\n    "prompt": "${prompt}",\n    "n": 1,\n    "response_format": "b64_json",\n    "size": "${requestSize}",\n    "aspect_ratio": "16:9"${referenceJson}\n  }'`,
    python: `import os\n\nimport requests\n${pythonReference}\npayload = {\n    "model": "grok-imagine-image-pro",\n    "prompt": "${prompt}",\n    "n": 1,\n    "response_format": "b64_json",\n    "size": "${requestSize}",\n    "aspect_ratio": "16:9",${pythonFields}\n}\nresponse = requests.post(\n    "${baseUrl}/v1/images/generations?async=true",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    javascript: `${jsReference}const response = await fetch('${baseUrl}/v1/images/generations?async=true', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    model: 'grok-imagine-image-pro',\n    prompt: '${prompt}',\n    n: 1,\n    response_format: 'b64_json',\n    size: '${requestSize}',\n    aspect_ratio: '16:9',${jsFields}\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
  };
}

function buildGeminiSamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation,
): CodeSamples {
  const url = `${baseUrl}${endpointFor(model)}`;
  const referencePart =
    operation === "edit"
      ? ',\n          {\n            "inlineData": {\n              "mimeType": "image/png",\n              "data": "BASE64_REFERENCE_IMAGE"\n            }\n          }'
      : "";
  const pythonReference =
    operation === "edit"
      ? `\nimport base64\n\nwith open("reference.png", "rb") as file:\n    reference = base64.b64encode(file.read()).decode("utf-8")\n`
      : "";
  const pythonPart =
    operation === "edit"
      ? `,\n                    {\n                        "inlineData": {\n                            "mimeType": "image/png",\n                            "data": reference,\n                        }\n                    }`
      : "";
  const jsReference =
    operation === "edit"
      ? `import { readFile } from 'node:fs/promises'\n\nconst reference = (await readFile('reference.png')).toString('base64')\n\n`
      : "";
  const jsPart =
    operation === "edit"
      ? `,\n          {\n            inlineData: { mimeType: 'image/png', data: reference },\n          }`
      : "";
  const basePrompt =
    operation === "edit"
      ? "Keep the character identity and change the scene to sunrise"
      : "Cinematic storyboard frame of a detective in a rainy neon alley";
  const prompt = `${basePrompt} [size: 2K, aspect ratio: 16:9]`;

  return {
    curl: `curl '${url}' \\
  -H 'Authorization: Bearer $NEW_API_KEY' \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "contents": [{\n      "role": "user",\n      "parts": [\n        { "text": "${prompt}" }${referencePart}\n      ]\n    }],\n    "generationConfig": {\n      "topP": 0.95,\n      "responseModalities": ["IMAGE"],\n      "imageConfig": {\n        "aspectRatio": "16:9",\n        "imageSize": "2K"\n      }\n    }\n  }'`,
    python: `import os\n\nimport requests\n${pythonReference}\npayload = {\n    "contents": [\n        {\n            "role": "user",\n            "parts": [\n                {"text": "${prompt}"}${pythonPart}\n            ],\n        }\n    ],\n    "generationConfig": {\n        "topP": 0.95,\n        "responseModalities": ["IMAGE"],\n        "imageConfig": {"aspectRatio": "16:9", "imageSize": "2K"},\n    },\n}\n\nresponse = requests.post(\n    "${url}",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nimage_base64 = response.json()["candidates"][0]["content"]["parts"][0]["inlineData"]["data"]`,
    javascript: `${jsReference}const response = await fetch('${url}', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    contents: [{\n      role: 'user',\n      parts: [\n        { text: '${prompt}' }${jsPart}\n      ],\n    }],\n    generationConfig: {\n      topP: 0.95,\n      responseModalities: ['IMAGE'],\n      imageConfig: { aspectRatio: '16:9', imageSize: '2K' },\n    },\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconst result = await response.json()\nconst imageBase64 = result.candidates[0].content.parts[0].inlineData.data`,
  };
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
    "aspect_ratio": "16:9",
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
        "aspect_ratio": "16:9",
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
      aspect_ratio: '16:9',
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
  };
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
  -F 'aspect_ratio=16:9'`,
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
  };
}

function CodeExample({
  samples,
  title,
}: {
  samples: CodeSamples;
  title: string;
}) {
  const { t } = useTranslation();
  const [language, setLanguage] = useState<CodeLanguage>("curl");
  const [copied, setCopied] = useState(false);

  const copyCode = async () => {
    await navigator.clipboard.writeText(samples[language]);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <div className="border-border/70 bg-card overflow-hidden rounded-xl border shadow-sm">
      <div className="border-border/70 bg-muted/25 flex flex-wrap items-center gap-3 border-b px-3 py-2.5">
        <div className="text-muted-foreground flex items-center gap-2 text-xs font-medium">
          <Terminal className="size-3.5" />
          {title}
        </div>
        <Tabs
          value={language}
          onValueChange={(value) => setLanguage(value as CodeLanguage)}
          className="ml-auto"
        >
          <TabsList className="h-7 p-0.5">
            {(["curl", "python", "javascript"] as const).map((item) => (
              <TabsTrigger
                key={item}
                value={item}
                className="h-6 px-2 text-[11px]"
              >
                {item === "curl"
                  ? "cURL"
                  : item === "python"
                    ? "Python"
                    : "JavaScript"}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={copyCode}
          aria-label={t("Copy code")}
        >
          {copied ? (
            <Check className="size-3.5" />
          ) : (
            <Clipboard className="size-3.5" />
          )}
        </Button>
      </div>
      <pre className="max-h-[34rem] overflow-auto bg-[#0b1020] p-4 text-[12px] leading-6 text-slate-200 dark:bg-black/35">
        <code>{samples[language]}</code>
      </pre>
    </div>
  );
}

function SectionHeading({
  id,
  eyebrow,
  title,
  description,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description?: string;
}) {
  return (
    <div id={id} className="scroll-mt-24 pt-4">
      <div className="text-primary mb-2 text-xs font-semibold tracking-[0.14em] uppercase">
        {eyebrow}
      </div>
      <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">
        {title}
      </h2>
      {description && (
        <p className="text-muted-foreground mt-2 max-w-3xl text-sm leading-7">
          {description}
        </p>
      )}
    </div>
  );
}

function ParameterTable({ model }: { model: ImageModelDoc }) {
  const { t } = useTranslation();
  return (
    <div className="border-border/70 mt-4 overflow-hidden rounded-xl border">
      <ScrollArea className="w-full">
        <table className="min-w-[860px] w-full text-left text-sm">
          <thead className="bg-muted/45 text-muted-foreground">
            <tr>
              {[
                "Parameter",
                "Type",
                "Required",
                "Allowed values",
                "Description",
              ].map((label) => (
                <th key={label} className="px-4 py-3 font-medium">
                  {t(label)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-border divide-y">
            {model.parameters.map((parameter) => (
              <tr key={parameter.name} className="align-top">
                <td className="px-4 py-3">
                  <code className="text-primary">{parameter.name}</code>
                </td>
                <td className="px-4 py-3">
                  <code>{parameter.type}</code>
                </td>
                <td className="px-4 py-3">{t(parameter.required)}</td>
                <td className="px-4 py-3">
                  <code>{parameter.values}</code>
                </td>
                <td className="text-muted-foreground max-w-sm px-4 py-3 leading-6">
                  {t(parameter.description)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <ScrollBar orientation="horizontal" />
      </ScrollArea>
    </div>
  );
}

function DocsNavigation({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useTranslation();
  const groups = [
    {
      label: "Get started",
      icon: Sparkles,
      items: [
        ["overview", "Overview"],
        ["authentication", "Authentication"],
      ],
    },
    {
      label: "OpenAI Images models",
      icon: ImageIcon,
      items: imageModels
        .filter((model) => model.family === "openai")
        .map((model) => [`model-${model.id}`, model.model]),
    },
    {
      label: "Gemini image models",
      icon: Layers3,
      items: imageModels
        .filter((model) => model.family === "gemini")
        .map((model) => [`model-${model.id}`, model.model]),
    },
    {
      label: "API reference",
      icon: Code2,
      items: referenceItems,
    },
  ];

  return (
    <nav
      className="space-y-6 px-4 py-5"
      aria-label={t("API documentation navigation")}
    >
      {groups.map((group) => {
        const Icon = group.icon;
        return (
          <div key={group.label}>
            <div className="text-muted-foreground mb-2 flex items-center gap-2 px-2 text-[11px] font-semibold tracking-[0.12em] uppercase">
              <Icon className="size-3.5" />
              {t(group.label)}
            </div>
            <div className="grid gap-0.5">
              {group.items.map(([id, label]) => (
                <a
                  key={id}
                  href={`#${id}`}
                  onClick={onNavigate}
                  className={cn(
                    "text-muted-foreground hover:bg-muted hover:text-foreground flex min-w-0 items-center gap-2 rounded-lg px-2 py-2 text-sm transition-colors",
                    id.startsWith("model-") && "font-mono text-xs",
                  )}
                >
                  <ChevronRight className="size-3 shrink-0 opacity-50" />
                  <span className="truncate">{t(label)}</span>
                </a>
              ))}
            </div>
          </div>
        );
      })}
    </nav>
  );
}

function ModelSection({
  model,
  baseUrl,
  index,
}: {
  model: ImageModelDoc;
  baseUrl: string;
  index: number;
}) {
  const { t } = useTranslation();
  const [operation, setOperation] = useState<ModelOperation>("generate");
  const samples = useMemo(() => {
    if (model.model === "grok-imagine-image-pro") {
      return buildGrokSamples(baseUrl, operation);
    }
    if (model.family === "openai") {
      return buildOpenAISamples(model, baseUrl, operation);
    }
    return buildGeminiSamples(model, baseUrl, operation);
  }, [baseUrl, model, operation]);

  return (
    <section
      id={`model-${model.id}`}
      className="scroll-mt-20 border-border/70 border-t pt-12"
    >
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{model.protocol}</Badge>
            <span className="text-muted-foreground text-xs">
              {t("Model reference")} {String(index + 1).padStart(2, "0")}
            </span>
          </div>
          <h2 className="break-all font-mono text-2xl font-semibold tracking-tight sm:text-3xl">
            {model.model}
          </h2>
          <p className="text-muted-foreground mt-3 max-w-3xl text-sm leading-7">
            {t(model.summary)}
          </p>
        </div>
        <Badge variant="outline" className="shrink-0">
          {model.family === "openai" ? "OpenAI Images" : "Gemini API"}
        </Badge>
      </div>

      <div className="mt-6 grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="border-border/70 bg-card rounded-xl border p-5">
          <div className="text-sm font-semibold">{t("Recommended for")}</div>
          <p className="text-muted-foreground mt-2 text-sm leading-6">
            {t(model.recommendedFor)}
          </p>
        </div>
        <div className="border-border/70 bg-card rounded-xl border p-5">
          <div className="text-sm font-semibold">{t("Capabilities")}</div>
          <div className="mt-3 flex flex-wrap gap-2">
            <Badge variant="outline">{t("Text to image")}</Badge>
            {model.supportsEditing && (
              <Badge variant="outline">{t("Reference image editing")}</Badge>
            )}
            {model.resolutions.map((resolution) => (
              <Badge key={resolution} variant="outline">
                {resolution}
              </Badge>
            ))}
          </div>
        </div>
      </div>

      <h3 className="mt-8 text-lg font-semibold">{t("Endpoints")}</h3>
      <div className="mt-4 grid gap-3">
        {model.endpoints.map((endpoint) => (
          <div
            key={endpoint.path}
            className="border-border/70 bg-card grid gap-3 rounded-xl border p-4 lg:grid-cols-[70px_minmax(0,1fr)_180px] lg:items-center"
          >
            <Badge className="w-fit bg-emerald-600 text-white hover:bg-emerald-600">
              {endpoint.method}
            </Badge>
            <div className="min-w-0">
              <code className="block overflow-x-auto text-sm">
                {endpoint.path.replace(
                  "{model}",
                  encodeURIComponent(model.model),
                )}
              </code>
              <p className="text-muted-foreground mt-1 text-xs leading-5">
                {t(endpoint.purpose)}
              </p>
            </div>
            <code className="text-muted-foreground text-xs">
              {endpoint.contentType}
            </code>
          </div>
        ))}
      </div>

      <h3 className="mt-8 text-lg font-semibold">{t("Request parameters")}</h3>
      <ParameterTable model={model} />

      <div className="border-primary/20 bg-primary/5 mt-5 rounded-xl border p-4">
        <div className="text-sm font-semibold">
          {t("Verified request behavior")}
        </div>
        <ul className="text-muted-foreground mt-2 space-y-2 pl-5 text-sm leading-6 list-disc">
          {model.requestNotes.map((note) => (
            <li key={note}>{t(note)}</li>
          ))}
        </ul>
      </div>

      <div className="mt-8 grid gap-5 lg:grid-cols-2">
        <div>
          <h3 className="text-lg font-semibold">
            {t("Supported aspect ratios")}
          </h3>
          <div className="mt-3 flex flex-wrap gap-2">
            {model.ratios.map((ratio) => (
              <code
                key={ratio}
                className="bg-muted rounded-md px-2 py-1 text-xs"
              >
                {ratio}
              </code>
            ))}
          </div>
        </div>
        <div>
          <h3 className="text-lg font-semibold">
            {t("Supported resolutions")}
          </h3>
          <div className="mt-3 flex flex-wrap gap-2">
            {model.resolutions.map((resolution) => (
              <code
                key={resolution}
                className="bg-muted rounded-md px-2 py-1 text-xs"
              >
                {resolution}
              </code>
            ))}
          </div>
          <p className="text-muted-foreground mt-3 text-xs leading-5">
            {model.model === "grok-imagine-image-pro"
              ? t(
                  "This model exposes automatic resolution selection. Storyboard Copilot maps 16:9 to size=1280x720 and uses 1K compatibility aliases for reference-image requests.",
                )
              : model.model === "gpt-image-2"
                ? t(
                    "gpt-image-2 derives the canvas ratio from the exact pixel size. Do not send aspect_ratio.",
                  )
                : t(
                    "Use the size mapping reference below when the OpenAI-compatible route requires exact pixels.",
                  )}
          </p>
        </div>
      </div>

      <div className="mt-8 flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-lg font-semibold">
          {t("Complete request examples")}
        </h3>
        <Tabs
          value={operation}
          onValueChange={(value) => setOperation(value as ModelOperation)}
        >
          <TabsList>
            <TabsTrigger value="generate">{t("Text to image")}</TabsTrigger>
            <TabsTrigger value="edit">
              {t("Reference image editing")}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <div className="mt-4">
        <CodeExample
          samples={samples}
          title={`${model.model} · ${t(operation === "generate" ? "Generate image" : "Edit image")}`}
        />
      </div>
      <div className="border-primary/20 bg-primary/5 mt-4 rounded-xl border px-4 py-3 text-sm">
        <span className="font-medium">{t("Image response path")}:</span>{" "}
        <code className="break-all">{model.responsePath}</code>
      </div>
    </section>
  );
}

export function ApiDocs() {
  const { t } = useTranslation();
  const { status } = useStatus();
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const baseUrl = useMemo(() => resolveBaseUrl(status), [status]);
  const modelSamples = useMemo<CodeSamples>(
    () => ({
      curl: `curl '${baseUrl}/v1/models' \\\n  -H 'Authorization: Bearer $NEW_API_KEY'`,
      python: `import os\n\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${baseUrl}/v1",\n    api_key=os.environ["NEW_API_KEY"],\n)\n\nfor model in client.models.list().data:\n    print(model.id)`,
      javascript: `import OpenAI from 'openai'\n\nconst client = new OpenAI({\n  baseURL: '${baseUrl}/v1',\n  apiKey: process.env.NEW_API_KEY,\n})\n\nconst models = await client.models.list()\nconsole.log(models.data.map((model) => model.id))`,
    }),
    [baseUrl],
  );
  const asyncImageSamples = useMemo(
    () => buildAsyncImageSamples(baseUrl),
    [baseUrl],
  );
  const compressionSamples = useMemo(() => buildCompressionSamples(), []);

  return (
    <PublicLayout>
      <div className="bg-background min-h-[calc(100svh-4rem)]">
        <div className="border-border/70 bg-background/95 sticky top-16 z-30 border-b backdrop-blur lg:hidden">
          <div className="flex h-12 items-center justify-between px-4">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <Code2 className="text-primary size-4" />
              {t("Image API documentation")}
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => setMobileNavOpen((open) => !open)}
              aria-label={t("Toggle documentation navigation")}
            >
              {mobileNavOpen ? (
                <X className="size-4" />
              ) : (
                <Menu className="size-4" />
              )}
            </Button>
          </div>
          {mobileNavOpen && (
            <div className="bg-background border-t shadow-xl">
              <ScrollArea className="max-h-[70svh]">
                <DocsNavigation onNavigate={() => setMobileNavOpen(false)} />
              </ScrollArea>
            </div>
          )}
        </div>

        <div className="mx-auto grid max-w-[1600px] lg:grid-cols-[286px_minmax(0,1fr)]">
          <aside className="border-border/70 bg-muted/10 sticky top-16 hidden h-[calc(100svh-4rem)] border-r lg:block">
            <div className="border-border/70 flex h-16 items-center gap-3 border-b px-6">
              <div className="bg-primary/10 text-primary grid size-9 place-items-center rounded-xl">
                <Code2 className="size-4" />
              </div>
              <div>
                <div className="text-sm font-semibold">{t("Image API")}</div>
                <div className="text-muted-foreground text-xs">
                  OOpii · new-api
                </div>
              </div>
            </div>
            <ScrollArea className="h-[calc(100%-4rem)]">
              <DocsNavigation />
            </ScrollArea>
          </aside>

          <main className="min-w-0 px-4 py-10 sm:px-8 lg:px-10 xl:px-14">
            <div className="mx-auto max-w-5xl">
              <section id="overview" className="scroll-mt-24">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="secondary">OOpii</Badge>
                  <Badge variant="outline">OpenAI Images</Badge>
                  <Badge variant="outline">Gemini generateContent</Badge>
                </div>
                <h1 className="mt-5 max-w-4xl text-3xl font-semibold tracking-tight sm:text-5xl">
                  {t("Image generation API reference")}
                </h1>
                <p className="text-muted-foreground mt-5 max-w-3xl text-base leading-8">
                  {t(
                    "Model-level reference for the OOpii routes used by Storyboard Copilot, including endpoints, parameters, aspect ratios, resolutions, editing workflows, and production-ready examples.",
                  )}
                </p>
                <div className="mt-8 grid gap-4 sm:grid-cols-3">
                  {[
                    [String(imageModels.length), "Documented models"],
                    ["2", "Compatible protocols"],
                    ["3", "Example languages"],
                  ].map(([value, label]) => (
                    <div
                      key={label}
                      className="border-border/70 bg-card rounded-xl border p-5"
                    >
                      <div className="text-2xl font-semibold">{value}</div>
                      <div className="text-muted-foreground mt-1 text-xs">
                        {t(label)}
                      </div>
                    </div>
                  ))}
                </div>
              </section>

              <section className="mt-14">
                <SectionHeading
                  id="authentication"
                  eyebrow="01 · GET STARTED"
                  title={t("Authentication")}
                  description={t(
                    "Send a new-api token in the Authorization header for model discovery and every image request. Keep the token on the server and never expose it in browser code.",
                  )}
                />
                <div className="border-border/70 bg-card mt-6 rounded-xl border p-5">
                  <div className="flex items-start gap-3">
                    <div className="bg-primary/10 text-primary mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg">
                      <KeyRound className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <div className="font-semibold">Authorization: Bearer</div>
                      <code className="bg-muted mt-3 block overflow-x-auto rounded-lg px-4 py-3 text-sm">
                        Authorization: Bearer $NEW_API_KEY
                      </code>
                      <p className="text-muted-foreground mt-3 text-sm leading-6">
                        {t(
                          "The API base URL shown in examples is detected from the current new-api deployment.",
                        )}
                      </p>
                    </div>
                  </div>
                </div>
              </section>

              <div className="mt-16 space-y-16">
                {imageModels.map((model, index) => (
                  <ModelSection
                    key={model.id}
                    model={model}
                    baseUrl={baseUrl}
                    index={index}
                  />
                ))}
              </div>

              <section className="mt-16 border-border/70 border-t pt-12">
                <SectionHeading
                  id="models-api"
                  eyebrow="API REFERENCE"
                  title={t("Query available models")}
                  description={t(
                    "Call this endpoint after adding an OOpii channel to confirm which model IDs are currently exposed to clients. A successful response follows the OpenAI model-list format.",
                  )}
                />
                <div className="mt-5 grid gap-3 sm:grid-cols-[70px_minmax(0,1fr)_180px] sm:items-center border-border/70 bg-card rounded-xl border p-4">
                  <Badge className="w-fit bg-sky-600 text-white hover:bg-sky-600">
                    GET
                  </Badge>
                  <code className="overflow-x-auto">/v1/models</code>
                  <code className="text-muted-foreground text-xs">
                    application/json
                  </code>
                </div>
                <div className="mt-4">
                  <CodeExample
                    samples={modelSamples}
                    title={t("List models")}
                  />
                </div>
                <div className="border-amber-500/25 bg-amber-500/5 mt-4 rounded-xl border p-4 text-sm leading-6">
                  <strong>{t("Channel configuration note")}:</strong>{" "}
                  {t(
                    "The channel base URL may include or omit a trailing slash. new-api normalizes it before requesting /v1/models so OOpii does not receive a double-slash path.",
                  )}
                </div>
                <div className="border-border/70 bg-muted/20 mt-3 rounded-xl border p-4 text-sm leading-6">
                  <strong>{t("Why a configured model may be missing")}:</strong>{" "}
                  {t(
                    "The client-facing /v1/models response is filtered by the token model limit, user or token group abilities, enabled channel models, and billing configuration. Unless self-use mode or Accept unset ratio model is enabled, models without billing configuration are omitted.",
                  )}
                </div>
              </section>

              <section className="mt-16">
                <SectionHeading
                  id="async-images"
                  eyebrow="API REFERENCE"
                  title={t("Asynchronous image tasks")}
                  description={t(
                    "Use asynchronous mode for slow, high-resolution, or reference-heavy generations. Submit once, save the task ID, poll the status endpoint, and download the completed image.",
                  )}
                />

                <div className="border-primary/20 bg-primary/5 mt-6 rounded-xl border p-4 text-sm leading-6">
                  <strong>{t("Storyboard Copilot behavior")}:</strong>{" "}
                  {t(
                    "The built-in OOpii provider enables async_image_task for all six documented image models. Its normal submit path appends async=true; Grok reference-image requests intentionally keep the plain generations URL because OOpii async submission can discard embedded references.",
                  )}
                </div>

                <div className="mt-6 grid gap-3">
                  {[
                    [
                      "POST",
                      "/v1/images/generations?async=true",
                      "Submit an OpenAI-compatible generation task.",
                    ],
                    [
                      "POST",
                      "/v1/images/edits?async=true",
                      "Submit a multipart reference-image editing task.",
                    ],
                    [
                      "POST",
                      "/v1beta/models/{model}:generateContent?async=true",
                      "Submit a Gemini-compatible image task.",
                    ],
                    [
                      "GET",
                      "/v1/images/tasks/{task_id}",
                      "Read status, error, expiration, and result URLs.",
                    ],
                    [
                      "GET",
                      "/v1/images/tasks/{task_id}/content",
                      "Download completed content with API authentication.",
                    ],
                    [
                      "GET",
                      "/v1/images/tasks/{task_id}/signed-content?token={signed_token}",
                      "Download completed content without Authorization by using the temporary signed URL returned by the status response.",
                    ],
                  ].map(([method, path, purpose]) => (
                    <div
                      key={path}
                      className="border-border/70 bg-card grid gap-3 rounded-xl border p-4 sm:grid-cols-[70px_minmax(0,1fr)] sm:items-center"
                    >
                      <Badge
                        className={cn(
                          "w-fit text-white",
                          method === "GET"
                            ? "bg-sky-600 hover:bg-sky-600"
                            : "bg-emerald-600 hover:bg-emerald-600",
                        )}
                      >
                        {method}
                      </Badge>
                      <div className="min-w-0">
                        <code className="block overflow-x-auto text-sm">
                          {path}
                        </code>
                        <p className="text-muted-foreground mt-1 text-xs leading-5">
                          {t(purpose)}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>

                <div className="mt-6 grid gap-4 md:grid-cols-3">
                  {[
                    [
                      "1",
                      "Submit once",
                      "Append async=true and store task_id. For compatibility, clients may fall back to id.",
                    ],
                    [
                      "2",
                      "Poll safely",
                      "Wait 3 seconds before the first query, then poll every 3 to 5 seconds for up to 10 to 15 minutes.",
                    ],
                    [
                      "3",
                      "Download promptly",
                      "After success, download with authentication or use the signed URL returned in url before it expires.",
                    ],
                  ].map(([step, title, description]) => (
                    <div
                      key={step}
                      className="border-border/70 bg-card rounded-xl border p-5"
                    >
                      <div className="bg-primary/10 text-primary grid size-8 place-items-center rounded-full text-sm font-semibold">
                        {step}
                      </div>
                      <h3 className="mt-4 font-semibold">{t(title)}</h3>
                      <p className="text-muted-foreground mt-2 text-sm leading-6">
                        {t(description)}
                      </p>
                    </div>
                  ))}
                </div>

                <div className="mt-6 overflow-hidden rounded-xl border border-border/70">
                  <ScrollArea className="w-full">
                    <table className="min-w-[760px] w-full text-left text-sm">
                      <thead className="bg-muted/45 text-muted-foreground">
                        <tr>
                          {[
                            "Task phase",
                            "Canonical status",
                            "Compatible status values",
                            "Client action",
                          ].map((label) => (
                            <th key={label} className="px-4 py-3 font-medium">
                              {t(label)}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className="divide-border divide-y">
                        {[
                          [
                            "Submitted",
                            "submitted / queued",
                            "pending, waiting",
                            "Continue polling.",
                          ],
                          [
                            "Processing",
                            "processing",
                            "running, in_progress, generating",
                            "Continue polling without creating another task.",
                          ],
                          [
                            "Succeeded",
                            "succeeded",
                            "success, completed, done",
                            "Download and persist the image immediately.",
                          ],
                          [
                            "Failed",
                            "failed / expired",
                            "failure, error, cancelled",
                            "Stop polling and record the status plus error body.",
                          ],
                        ].map(([phase, canonical, aliases, action]) => (
                          <tr key={phase} className="align-top">
                            <td className="px-4 py-3 font-medium">
                              {t(phase)}
                            </td>
                            <td className="px-4 py-3">
                              <code>{canonical}</code>
                            </td>
                            <td className="px-4 py-3">
                              <code>{aliases}</code>
                            </td>
                            <td className="text-muted-foreground px-4 py-3 leading-6">
                              {t(action)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation="horizontal" />
                  </ScrollArea>
                </div>

                <div className="mt-6">
                  <CodeExample
                    samples={asyncImageSamples}
                    title={t("Submit, poll, and download")}
                  />
                </div>

                <div className="border-amber-500/25 bg-amber-500/5 mt-4 flex gap-3 rounded-xl border p-4 text-sm leading-6">
                  <ShieldAlert className="mt-0.5 size-4 shrink-0 text-amber-600" />
                  <p>
                    <strong>{t("Important polling rule")}:</strong>{" "}
                    {t(
                      "After a task is accepted, do not submit the same generation again. Keep querying the existing task ID. High-frequency polling can trigger rate limits, and 4K tasks may require a longer deadline.",
                    )}
                  </p>
                </div>

                <div className="border-border/70 bg-muted/20 mt-4 rounded-xl border p-4 text-sm leading-6">
                  <strong>{t("Download authentication")}:</strong>{" "}
                  {t(
                    "The content_url endpoint normally requires Authorization. A successful task may also return a temporary signed URL in url with url_expires_at; signed-content URLs must be used before expiration.",
                  )}
                </div>
              </section>

              <section className="mt-16">
                <SectionHeading
                  id="image-compression"
                  eyebrow="UPLOAD GUIDE"
                  title={t("Image compression and upload")}
                  description={t(
                    "Compress reference images before multipart or Base64 upload to reduce 413 responses, request timeouts, memory pressure, and upstream decoding failures.",
                  )}
                />

                <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                  {[
                    ["2048 px", "Recommended longest edge"],
                    ["2-4 MB", "Recommended size per image"],
                    ["80-88%", "JPEG quality range"],
                    ["+33%", "Approximate Base64 overhead"],
                  ].map(([value, label]) => (
                    <div
                      key={label}
                      className="border-border/70 bg-card rounded-xl border p-5"
                    >
                      <div className="text-2xl font-semibold">{value}</div>
                      <div className="text-muted-foreground mt-2 text-xs leading-5">
                        {t(label)}
                      </div>
                    </div>
                  ))}
                </div>

                <div className="mt-6 grid gap-4 lg:grid-cols-3">
                  {[
                    [
                      FileImage,
                      "JPEG for photographs",
                      "Use quality 80 to 88 for photographic references without transparency. Convert oversized PNG photographs to JPEG before Base64 encoding.",
                    ],
                    [
                      Upload,
                      "WebP for smaller files",
                      "Use quality 80 to 85 when the client and upstream route accept WebP. It often reduces upload size while preserving visual detail.",
                    ],
                    [
                      Layers3,
                      "PNG for transparency",
                      "Keep PNG for masks, line art, logos, alpha channels, or pixel-exact assets. Resize and strip metadata even when PNG must be preserved.",
                    ],
                  ].map(([Icon, title, description]) => (
                    <div
                      key={title as string}
                      className="border-border/70 bg-card rounded-xl border p-5"
                    >
                      <Icon className="text-primary size-5" />
                      <h3 className="mt-4 font-semibold">
                        {t(title as string)}
                      </h3>
                      <p className="text-muted-foreground mt-2 text-sm leading-6">
                        {t(description as string)}
                      </p>
                    </div>
                  ))}
                </div>

                <div className="mt-6 overflow-hidden rounded-xl border border-border/70">
                  <ScrollArea className="w-full">
                    <table className="min-w-[820px] w-full text-left text-sm">
                      <thead className="bg-muted/45 text-muted-foreground">
                        <tr>
                          {[
                            "Upload method",
                            "Image field",
                            "Required encoding",
                            "Common mistake",
                          ].map((label) => (
                            <th key={label} className="px-4 py-3 font-medium">
                              {t(label)}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody className="divide-border divide-y">
                        {[
                          [
                            "OpenAI image edits",
                            "image / image[]",
                            "multipart/form-data file",
                            "Do not set a fixed multipart boundary manually.",
                          ],
                          [
                            "Gemini inlineData",
                            "inlineData.data",
                            "Raw Base64 only",
                            "Remove the data:image/png;base64, prefix.",
                          ],
                          [
                            "Grok reference images",
                            "reference_images[]",
                            "Complete data URL",
                            "Include the correct MIME prefix for each image.",
                          ],
                          [
                            "Public image URL",
                            "image_url or provider field",
                            "Publicly reachable HTTPS URL",
                            "Private, localhost, expiring, or login-only URLs cannot be fetched upstream.",
                          ],
                        ].map(([method, field, encoding, mistake]) => (
                          <tr key={method} className="align-top">
                            <td className="px-4 py-3 font-medium">
                              {t(method)}
                            </td>
                            <td className="px-4 py-3">
                              <code>{field}</code>
                            </td>
                            <td className="px-4 py-3">{t(encoding)}</td>
                            <td className="text-muted-foreground px-4 py-3 leading-6">
                              {t(mistake)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation="horizontal" />
                  </ScrollArea>
                </div>

                <div className="mt-6">
                  <CodeExample
                    samples={compressionSamples}
                    title={t("Compress a reference image")}
                  />
                </div>

                <div className="mt-6 grid gap-3 sm:grid-cols-2">
                  {[
                    [
                      "413 - Payload too large",
                      "Reduce the number of references, resize the longest edge, convert photographic PNG files to JPEG or WebP, and lower quality gradually.",
                    ],
                    [
                      "400 - Invalid image data",
                      "Verify the MIME type, Base64 prefix rule, file integrity, and whether the selected endpoint expects JSON or multipart data.",
                    ],
                    [
                      "502 / 504 - Upstream timeout",
                      "Compress the images, reduce 4K reference inputs, use asynchronous mode, and retry with a bounded backoff.",
                    ],
                    [
                      "Memory or Base64 failures",
                      "Account for about 33 percent Base64 expansion and avoid holding multiple uncompressed 4K or 8K images in memory at once.",
                    ],
                  ].map(([title, description]) => (
                    <div
                      key={title}
                      className="border-border/70 bg-muted/20 rounded-xl border p-4"
                    >
                      <div className="flex items-center gap-2 font-semibold">
                        <Download className="text-primary size-4" />
                        {t(title)}
                      </div>
                      <p className="text-muted-foreground mt-2 text-sm leading-6">
                        {t(description)}
                      </p>
                    </div>
                  ))}
                </div>

                <p className="text-muted-foreground mt-4 text-xs leading-6">
                  {t(
                    "These values are production recommendations rather than fixed provider limits. For identity, style, or composition references, 1536 px is often sufficient; avoid uploading original 4K or 8K files unless fine detail is essential.",
                  )}
                </p>
              </section>

              <section className="mt-16">
                <SectionHeading
                  id="size-mapping"
                  eyebrow="API REFERENCE"
                  title={t("Size mapping")}
                  description={t(
                    "Use these common Storyboard Copilot mappings when an OpenAI-compatible model accepts exact width and height through the size parameter.",
                  )}
                />
                <div className="border-border/70 mt-6 overflow-hidden rounded-xl border">
                  <ScrollArea className="w-full">
                    <table className="min-w-[520px] w-full text-left text-sm">
                      <thead className="bg-muted/45 text-muted-foreground">
                        <tr>
                          {["Resolution", "Aspect ratio", "Pixel size"].map(
                            (label) => (
                              <th key={label} className="px-4 py-3 font-medium">
                                {t(label)}
                              </th>
                            ),
                          )}
                        </tr>
                      </thead>
                      <tbody className="divide-border divide-y">
                        {sizeRows.map(([resolution, ratio, size]) => (
                          <tr key={`${resolution}-${ratio}`}>
                            <td className="px-4 py-3">
                              <code>{resolution}</code>
                            </td>
                            <td className="px-4 py-3">
                              <code>{ratio}</code>
                            </td>
                            <td className="px-4 py-3">
                              <code>{size}</code>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation="horizontal" />
                  </ScrollArea>
                </div>
              </section>

              <section className="mt-16">
                <SectionHeading
                  id="response-format"
                  eyebrow="API REFERENCE"
                  title={t("Response format")}
                  description={t(
                    "Read the image URL or base64 field that matches the selected compatibility protocol.",
                  )}
                />
                <div className="mt-6 grid gap-4 lg:grid-cols-2">
                  <div className="border-border/70 bg-card rounded-xl border p-5">
                    <div className="flex items-center gap-2 font-semibold">
                      <ImageIcon className="text-primary size-4" /> OpenAI
                      Images
                    </div>
                    <p className="text-muted-foreground mt-2 text-xs">
                      {t("URL response")}
                    </p>
                    <pre className="bg-muted/45 mt-4 overflow-x-auto rounded-lg p-4 text-xs leading-6">{`{\n  "created": 1710000000,\n  "data": [{\n    "url": "https://.../image.png"\n  }]\n}`}</pre>
                  </div>
                  <div className="border-border/70 bg-card rounded-xl border p-5">
                    <div className="flex items-center gap-2 font-semibold">
                      <FileJson className="text-primary size-4" /> Gemini
                    </div>
                    <p className="text-muted-foreground mt-2 text-xs">
                      {t("Inline base64 response")}
                    </p>
                    <pre className="bg-muted/45 mt-4 overflow-x-auto rounded-lg p-4 text-xs leading-6">{`{\n  "candidates": [{\n    "content": {\n      "parts": [{\n        "inlineData": {\n          "mimeType": "image/png",\n          "data": "...base64..."\n        }\n      }]\n    }\n  }]\n}`}</pre>
                  </div>
                </div>
              </section>

              <section className="mt-16 pb-16">
                <SectionHeading
                  id="errors"
                  eyebrow="API REFERENCE"
                  title={t("Errors and status codes")}
                  description={t(
                    "Handle authentication, validation, quota, and upstream failures explicitly. Error bodies may include a message, type, parameter, and request identifier.",
                  )}
                />
                <div className="border-border/70 mt-6 overflow-hidden rounded-xl border">
                  <ScrollArea className="w-full">
                    <table className="min-w-[700px] w-full text-left text-sm">
                      <thead className="bg-muted/45 text-muted-foreground">
                        <tr>
                          {["Status", "Meaning", "Recommended action"].map(
                            (label) => (
                              <th key={label} className="px-4 py-3 font-medium">
                                {t(label)}
                              </th>
                            ),
                          )}
                        </tr>
                      </thead>
                      <tbody className="divide-border divide-y">
                        {[
                          [
                            "400",
                            "Invalid request parameters",
                            "Check model ID, ratio, size, content type, and required fields.",
                          ],
                          [
                            "401",
                            "Invalid or missing API token",
                            "Confirm the Bearer token and the token status.",
                          ],
                          [
                            "403",
                            "Model or channel access denied",
                            "Check token model permissions and channel group access.",
                          ],
                          [
                            "429",
                            "Rate limit or insufficient quota",
                            "Retry with backoff or add quota before retrying.",
                          ],
                          [
                            "500 / 502",
                            "Gateway or upstream processing error",
                            "Log the request ID and retry only idempotent generation tasks.",
                          ],
                          [
                            "503 / 504",
                            "Upstream unavailable or timed out",
                            "Retry with exponential backoff and a bounded attempt count.",
                          ],
                        ].map(([statusCode, meaning, action]) => (
                          <tr key={statusCode} className="align-top">
                            <td className="px-4 py-3">
                              <Badge variant="outline">{statusCode}</Badge>
                            </td>
                            <td className="px-4 py-3 font-medium">
                              {t(meaning)}
                            </td>
                            <td className="text-muted-foreground px-4 py-3 leading-6">
                              {t(action)}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <ScrollBar orientation="horizontal" />
                  </ScrollArea>
                </div>
                <div className="border-border/70 bg-muted/20 mt-6 flex gap-3 rounded-xl border p-4 text-sm leading-6">
                  <Search className="text-primary mt-0.5 size-4 shrink-0" />
                  <p>
                    <strong>{t("Troubleshooting order")}:</strong>{" "}
                    {t(
                      "first call /v1/models, then verify the exact model ID, endpoint family, request Content-Type, and the response path documented for that model.",
                    )}
                  </p>
                </div>
              </section>
            </div>
          </main>
        </div>
      </div>
    </PublicLayout>
  );
}
