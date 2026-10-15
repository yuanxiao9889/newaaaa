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

export function buildHyImageSamples(
  baseUrl: string,
  operation: 'generate' | 'edit'
): CodeSamples {
  const path =
    operation === 'edit' ? '/v1/images/edits' : '/v1/images/generations'
  const url = `${baseUrl.replace(/\/+$/, '')}${path}`
  const body = {
    model: 'hy-image-v3.5-preview',
    prompt: 'A cinematic storyboard frame with consistent character details.',
    n: 1,
    size: '1536x1536',
    ...(operation === 'edit'
      ? {
          image: [
            'https://media.example.com/character.jpg',
            'https://media.example.com/scene.png',
          ],
        }
      : {}),
  }
  const json = JSON.stringify(body, null, 2)
  return {
    curl: `curl --fail-with-body ${JSON.stringify(url)} \\\n  -H "Authorization: Bearer $NEW_API_KEY" \\\n  -H 'Content-Type: application/json' \\\n  --data-binary @- <<'JSON'\n${json}\nJSON`,
    python: `import json\nimport os\nimport requests\n\npayload = json.loads(${JSON.stringify(json)})\nresponse = requests.post(\n    ${JSON.stringify(url)},\n    headers={"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"},\n    json=payload, timeout=300,\n)\nresponse.raise_for_status()\nprint(response.json()["data"][0]["url"])`,
    javascript: `const response = await fetch(${JSON.stringify(url)}, {\n  method: 'POST',\n  headers: { Authorization: \`Bearer \${process.env.NEW_API_KEY}\`, 'Content-Type': 'application/json' },\n  body: JSON.stringify(${json}),\n  signal: AbortSignal.timeout(300000),\n})\nif (!response.ok) throw new Error(await response.text())\nconst result = await response.json()\nconsole.log(result.data[0].url)`,
  }
}
