import {
  pendingVideoModels,
  pendingVideoNote,
  videoCommonParameters,
  videoGroups,
  videoGuideKeys,
  videoResponseExamples,
} from './video-data'
import {
  buildVideoLifecycleSamples,
  buildVideoSubmitSamples,
} from './video-samples'

export function buildVideoMarkdown(
  baseUrl: string,
  translate: (key: string) => string
): string {
  const lines = [
    `# ${translate('apiDocs.video.title')}`,
    '',
    `Base URL: ${baseUrl}`,
    '',
  ]
  for (const key of videoGuideKeys) {
    lines.push(translate(key), '')
  }
  for (const field of videoCommonParameters) {
    lines.push(
      `- \`${field.name}\` (${field.type}): ${translate(field.description)}`
    )
  }
  for (const group of videoGroups) {
    lines.push('', `## ${translate(group.title)}`, '')
    for (const variant of group.variants) {
      lines.push(
        `### ${variant.models.map((model) => `\`${model}\``).join(' / ')}`,
        '',
        `- ${translate('apiDocs.video.duration')}: ${variant.seconds}`,
        `- ${translate('apiDocs.video.resolution')}: ${variant.resolution}`,
        `- ${translate('apiDocs.video.ratios')}: ${variant.ratios}`,
        `- ${translate(variant.limits)}`,
        '```json',
        JSON.stringify(variant.constraints, null, 2),
        '```',
        ''
      )
    }
    for (const field of group.parameters) {
      lines.push(
        `- \`${field.name}\` (${field.type}; ${translate(field.required ? 'apiDocs.video.required' : 'apiDocs.video.optional')}): ${translate(field.description)}`
      )
    }
    for (const note of group.notes) {
      lines.push('', translate(note))
    }
    for (const example of group.examples) {
      lines.push(
        '',
        `### ${translate(example.title)}`,
        '',
        'POST /v1/videos',
        '',
        '```json',
        JSON.stringify(example.body, null, 2),
        '```',
        '',
        '```bash',
        buildVideoSubmitSamples(baseUrl, example.body).curl,
        '```'
      )
    }
  }
  lines.push('', `## ${translate('apiDocs.video.responses')}`)
  for (const [name, response] of Object.entries(videoResponseExamples)) {
    lines.push(
      '',
      translate(`apiDocs.video.${name}`),
      '',
      '```json',
      JSON.stringify(response, null, 2),
      '```'
    )
  }
  lines.push('', `## ${translate('apiDocs.video.lifecycle')}`)
  for (const [language, sample] of Object.entries(
    buildVideoLifecycleSamples(baseUrl)
  )) {
    lines.push(
      '',
      `### ${language}`,
      '',
      `\`\`\`${language === 'curl' ? 'bash' : language}`,
      sample,
      '```'
    )
  }
  lines.push(
    '',
    `## ${translate('Model mappings to confirm')}`,
    translate(pendingVideoNote),
    ...pendingVideoModels.map((model) => `- ${model}`),
    '',
    `## ${translate('apiDocs.video.legacy')}`,
    '',
    translate('apiDocs.video.legacyNote'),
    ''
  )
  return lines.join('\n')
}
