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

type ModelOperation = 'generate' | 'edit'

export function buildOpenAISamples(
  model: ImageModelDoc,
  baseUrl: string,
  operation: ModelOperation,
  transparent = false
): CodeSamples {
  const size =
    model.resolutions.length === 1 && model.resolutions[0] === '1K'
      ? '1280x720'
      : '2048x1152'
  const quality =
    transparent &&
    model.parameters
      .find((parameter) => parameter.name === 'quality')
      ?.values.split(' | ')
      .includes('max')
      ? 'max'
      : 'medium'
  const curlFields = transparent
    ? " \\\n  -F 'background=transparent' \\\n  -F 'output_format=png'"
    : ''
  const jsonFields = transparent
    ? ',\n    "background": "transparent",\n    "output_format": "png"'
    : ''
  const pythonFields = transparent
    ? '\n            "background": "transparent",\n            "output_format": "png",'
    : ''
  const jsFormFields = transparent
    ? "\nform.append('background', 'transparent')\nform.append('output_format', 'png')"
    : ''
  const jsFields = transparent
    ? "\n    background: 'transparent',\n    output_format: 'png',"
    : ''
  if (operation === 'edit') {
    return {
      curl: `curl '${baseUrl}/v1/images/edits' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -F 'model=${model.model}' \\
  -F 'prompt=Keep the character identity and change the scene to sunrise' \\
  -F 'n=1' \\
  -F 'image[]=@reference-1.png' \\
  -F 'image[]=@reference-2.png' \\
  -F 'size=${size}' \\
  -F 'quality=${quality}'${curlFields}`,
      python: `import os\n\nimport requests\n\nwith open("reference-1.png", "rb") as first, open("reference-2.png", "rb") as second:\n    response = requests.post(\n        "${baseUrl}/v1/images/edits",\n        headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n        data={\n            "model": "${model.model}",\n            "prompt": "Keep the character identity and change the scene to sunrise",\n            "n": "1",\n            "size": "${size}",\n            "quality": "${quality}",${pythonFields}\n        },\n        files=[\n            ("image[]", ("reference-1.png", first, "image/png")),\n            ("image[]", ("reference-2.png", second, "image/png")),\n        ],\n        timeout=180,\n    )\n\nresponse.raise_for_status()\nprint(response.json())`,
      javascript: `import { readFile } from 'node:fs/promises'\n\nconst form = new FormData()\nform.append('model', '${model.model}')\nform.append('prompt', 'Keep the character identity and change the scene to sunrise')\nform.append('n', '1')\nform.append('size', '${size}')\nform.append('quality', '${quality}')${jsFormFields}\nform.append('image[]', new Blob([await readFile('reference-1.png')], { type: 'image/png' }), 'reference-1.png')\nform.append('image[]', new Blob([await readFile('reference-2.png')], { type: 'image/png' }), 'reference-2.png')\n\nconst response = await fetch('${baseUrl}/v1/images/edits', {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${process.env.NEW_API_KEY}\` },\n  body: form,\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
    }
  }

  return {
    curl: `curl '${baseUrl}/v1/images/generations' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{\n    "model": "${model.model}",\n    "prompt": "Cinematic storyboard frame of a detective in a rainy neon alley",\n    "n": 1,\n    "size": "${size}",\n    "quality": "${quality}"${jsonFields}\n  }'`,
    python: `import os\n\nimport requests\n\npayload = {\n    "model": "${model.model}",\n    "prompt": "Cinematic storyboard frame of a detective in a rainy neon alley",\n    "n": 1,\n    "size": "${size}",\n    "quality": "${quality}",${pythonFields.replaceAll('            ', '    ')}\n}\nresponse = requests.post(\n    "${baseUrl}/v1/images/generations",\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload,\n    timeout=180,\n)\nresponse.raise_for_status()\nprint(response.json())`,
    javascript: `const response = await fetch('${baseUrl}/v1/images/generations', {\n  method: 'POST',\n  headers: {\n    Authorization: \`Bearer \${process.env.NEW_API_KEY}\`,\n    'Content-Type': 'application/json',\n  },\n  body: JSON.stringify({\n    model: '${model.model}',\n    prompt: 'Cinematic storyboard frame of a detective in a rainy neon alley',\n    n: 1,\n    size: '${size}',\n    quality: '${quality}',${jsFields}\n  }),\n})\n\nif (!response.ok) throw new Error(await response.text())\nconsole.log(await response.json())`,
  }
}
