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
import type { EndpointDoc, ImageModelDoc, ParameterDoc } from './data'

export const image25Groups = [
  {
    id: 'all-image-2.5',
    title: 'ALL Image 2.5 · flare / sunburst / DD',
    modelIds: [
      'all-image-2.5-flare',
      'all-image-2.5-sunburst',
      'all-image-2.5-flare-DD',
      'all-image-2.5-sunburst-DD',
    ],
    qualities: ['low', 'medium', 'high'],
    resolutions: ['1K', '2K', '4K'],
    summary: 'apiDocs.image25.standardSummary',
    billing: 'apiDocs.image25.perRequest',
  },
  {
    id: 'all-image-2.5-1k',
    title: 'ALL Image 2.5 · flare-1k / sunburst-1k',
    modelIds: ['all-image-2.5-flare-1k', 'all-image-2.5-sunburst-1k'],
    qualities: ['low', 'medium', 'high'],
    resolutions: ['1K'],
    summary: 'apiDocs.image25.oneKSummary',
    billing: 'apiDocs.image25.perRequest',
  },
  {
    id: 'all-image-2.5-h',
    title: 'ALL Image 2.5 · flare-H / sunburst-H',
    modelIds: ['all-image-2.5-flare-H', 'all-image-2.5-sunburst-H'],
    qualities: ['low', 'medium', 'high', 'xhigh', 'max'],
    resolutions: ['1K', '2K', '4K'],
    summary: 'apiDocs.image25.hSummary',
    billing: 'apiDocs.image25.perRequest',
  },
  {
    id: 'gpt-image-2.5',
    title: 'GPT Image 2.5 · flare / sunburst',
    modelIds: ['gpt-image-2.5-flare', 'gpt-image-2.5-sunburst'],
    qualities: ['medium', 'high', 'xhigh', 'max'],
    resolutions: ['1K', '2K', '4K'],
    summary: 'apiDocs.image25.gptSummary',
    billing: 'apiDocs.image25.perToken',
  },
  {
    id: 'gpt-image-2.5-opi',
    title: 'GPT Image 2.5 · flare-opi / sunburst-opi',
    modelIds: ['gpt-image-2.5-flare-opi', 'gpt-image-2.5-sunburst-opi'],
    qualities: ['low', 'medium', 'high', 'xhigh', 'max'],
    resolutions: ['1K', '2K', '4K'],
    summary: 'apiDocs.image25.opiSummary',
    billing: 'apiDocs.image25.perRequest',
  },
]

export function buildImage25Docs(
  parameters: ParameterDoc[],
  endpoints: EndpointDoc[],
  ratios: string[]
): ImageModelDoc[] {
  return image25Groups.map((group) => ({
    id: group.id,
    model: group.modelIds[0],
    title: group.title,
    modelIds: group.modelIds,
    protocol: 'OpenAI Images',
    family: 'openai',
    summary: group.summary,
    recommendedFor: 'apiDocs.image25.recommendedFor',
    endpoints,
    ratios,
    resolutions: group.resolutions,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    parameters: [
      ...parameters.map((parameter) => {
        if (parameter.name === 'model') {
          return {
            ...parameter,
            values: group.modelIds.join(' | '),
            description: 'apiDocs.image25.modelDescription',
          }
        }
        if (parameter.name === 'quality') {
          return {
            ...parameter,
            values: group.qualities.join(' | '),
            description: 'apiDocs.image25.qualityDescription',
          }
        }
        if (parameter.name === 'size') {
          return {
            ...parameter,
            values:
              group.resolutions.length === 1
                ? '1024x1024 / 1280x720 / 720x1280'
                : '2048x1152 (width x height)',
            description:
              group.resolutions.length === 1
                ? 'apiDocs.image25.oneKSize'
                : 'apiDocs.image25.sizeDescription',
          }
        }
        if (parameter.name === 'image / image[]') {
          return {
            ...parameter,
            values: 'PNG / JPEG / WebP',
            description: 'apiDocs.image25.referenceDescription',
          }
        }
        return parameter
      }),
      {
        name: 'background',
        type: 'string',
        required: 'Optional',
        values: 'transparent',
        description: 'apiDocs.image25.backgroundDescription',
      },
      {
        name: 'output_format',
        type: 'string',
        required: 'Optional',
        values: 'png',
        description: 'apiDocs.image25.outputFormatDescription',
      },
    ],
    requestNotes: [
      'apiDocs.image25.groupNote',
      group.billing,
      'apiDocs.image25.qualityDescription',
      'apiDocs.image25.wireFields',
      'apiDocs.image25.transparentNote',
      'apiDocs.image25.referenceDescription',
      group.resolutions.length === 1
        ? 'apiDocs.image25.oneKSize'
        : 'apiDocs.image25.customSizeNote',
      'apiDocs.image25.asyncNote',
    ],
  }))
}
