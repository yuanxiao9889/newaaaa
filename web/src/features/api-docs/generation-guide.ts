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
import { imageModels, oopiiModelGroups } from './data'
import { generationContracts } from './generation-data'
import { buildImageSamples } from './image-samples'
import { buildImage26GifBody } from './openai-samples'
import { buildJsonRequestSamples } from './request-samples'
import { textExamples } from './text-data'
import { buildVideoMarkdown } from './video-guide'

export function buildGenerationMarkdown(
  baseUrl: string,
  t: (key: string) => string
) {
  const lines = [
    `# ${t('Audio, image and video integration')}`,
    '',
    `Base URL: ${baseUrl}`,
    'Authorization: Bearer $NEW_API_KEY',
    'Content-Type: application/json',
    '',
    t(
      'This export covers the current text model IDs, image parameters including GIF, Seed Audio, Midjourney and video contracts.'
    ),
    t(
      'Documentation updated on 2026-11-18 from the software catalog and gateway request contracts. Confirm availability with GET /v1/models using your own API key.'
    ),
  ]
  lines.push('', `## ${t('Text and multimodal API routes')}`, '')
  for (const model of oopiiModelGroups.text) {
    lines.push(`- ${model.id}: ${model.endpoints.join(', ')}`)
  }
  for (const example of textExamples) {
    lines.push(
      '',
      `### ${t(example.title)}`,
      `${baseUrl}${example.path}`,
      '```bash',
      buildJsonRequestSamples(baseUrl, example.body, example.path).curl,
      '```'
    )
  }
  for (const model of imageModels) {
    lines.push(
      '',
      `## ${model.title ?? model.model}`,
      (model.modelIds ?? [model.model]).join(' / ')
    )
    for (const endpoint of model.endpoints) {
      lines.push(
        `- ${endpoint.method} ${endpoint.path} (${endpoint.contentType})`
      )
    }
    lines.push(`- ${t('Response format')}: ${model.responsePath}`)
    for (const field of model.parameters) {
      lines.push(`- ${field.name}: ${field.values}; ${t(field.description)}`)
    }
    for (const note of model.requestNotes) lines.push('', t(note))
    for (const operation of ['generate', 'edit'] as const) {
      lines.push(
        '',
        '```bash',
        buildImageSamples(model, baseUrl, operation).curl,
        '```'
      )
    }
    if (model.outputFormats?.includes('gif')) {
      lines.push(
        '',
        `### ${t('GIF generation example')}`,
        '```bash',
        buildJsonRequestSamples(
          baseUrl,
          buildImage26GifBody(model),
          '/v1/images/generations?async=true'
        ).curl,
        '```'
      )
      if (model.model === 'all-image-2.6-sunburst-H') {
        lines.push(
          '',
          `### GIF · ${t('Reference image editing')}`,
          '```bash',
          buildJsonRequestSamples(
            baseUrl,
            buildImage26GifBody(model, 'edit'),
            '/v1/images/edits?async=true'
          ).curl,
          '```'
        )
      }
    }
  }
  for (const contract of generationContracts) {
    lines.push(
      '',
      `## ${t(contract.title)}: ${contract.model}`,
      '',
      t(contract.description),
      ''
    )
    for (const endpoint of contract.endpoints) {
      lines.push(`- ${endpoint.method} ${endpoint.path}`)
    }
    for (const parameter of contract.parameters) {
      lines.push(
        `- \`${parameter.name}\` (${parameter.type}; ${t(parameter.required ? 'apiDocs.video.required' : 'apiDocs.video.optional')}): ${t(parameter.description)} ${parameter.values ?? ''}`
      )
    }
    for (const note of contract.notes) lines.push('', t(note))
    for (const example of contract.examples) {
      lines.push(
        '',
        `### ${t(example.title)}`,
        `${baseUrl}${example.path}`,
        '```json',
        JSON.stringify(example.body, null, 2),
        '```'
      )
      for (const [language, code] of Object.entries(
        buildJsonRequestSamples(baseUrl, example.body, example.path)
      )) {
        lines.push('', `\`\`\`${language}`, code, '```')
      }
    }
    lines.push('', `### ${t('Response examples')}`)
    for (const response of contract.responses) {
      lines.push('```json', JSON.stringify(response, null, 2), '```')
    }
  }
  lines.push('', buildVideoMarkdown(baseUrl, t))
  return lines.join('\n')
}
