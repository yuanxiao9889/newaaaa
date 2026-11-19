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
    id: 'all-image-2.6-sunburst-h',
    title: 'ALL Image 2.6 · sunburst-H',
    modelIds: ['all-image-2.6-sunburst-H'],
    qualities: ['low', 'medium', 'high', 'xhigh', 'max'],
    resolutions: ['1K', '2K', '4K'],
    summary:
      'H models support PNG, GIF and SVG. Static output supports low, medium, high, xhigh and max quality.',
    billing: 'apiDocs.image25.perRequest',
    outputFormats: ['png', 'gif', 'svg'],
  },
  {
    id: 'all-image-2.6-flare-h',
    title: 'ALL Image 2.6 · flare-H',
    modelIds: ['all-image-2.6-flare-H'],
    qualities: ['low', 'medium', 'high', 'xhigh', 'max'],
    resolutions: ['1K', '2K', '4K'],
    summary:
      'H models support PNG, GIF and SVG. Static output supports low, medium, high, xhigh and max quality.',
    billing: 'apiDocs.image25.perRequest',
    outputFormats: ['png', 'gif', 'svg'],
  },
  {
    id: 'all-image-2.6',
    title: 'ALL Image 2.6 · flare / sunburst',
    modelIds: ['all-image-2.6-flare', 'all-image-2.6-sunburst'],
    qualities: ['low', 'medium', 'high'],
    resolutions: ['1K', '2K', '4K'],
    summary:
      'ALL Image 2.6 supports GIF animation. Quality is low, medium or high; xhigh and max are unsupported.',
    billing: 'apiDocs.image25.perRequest',
    outputFormats: ['png', 'gif'],
  },
  {
    id: 'all-image-2.6-dd-1k',
    title: 'ALL Image 2.6 · flare-DD / sunburst-DD',
    modelIds: ['all-image-2.6-flare-DD', 'all-image-2.6-sunburst-DD'],
    qualities: ['low', 'medium', 'high'],
    resolutions: ['1K'],
    summary:
      'ALL Image 2.6 supports GIF animation. Quality is low, medium or high; xhigh and max are unsupported.',
    billing: 'apiDocs.image25.perRequest',
    outputFormats: ['png', 'gif', 'svg'],
  },
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
    outputFormats: group.outputFormats,
    responsePath: 'data[0].url or data[0].b64_json',
    parameters: [
      ...(group.outputFormats
        ? [
            {
              name: 'frame_count',
              type: 'integer',
              required: 'Optional',
              values: '2–16; default: 9',
              description: 'GIF frame count. Send only with output_format=gif.',
            },
            {
              name: 'frame_duration_ms',
              type: 'integer',
              required: 'Optional',
              values: '20–2000 ms; default: 200 ms',
              description:
                'Duration of each GIF frame in milliseconds. Send only with output_format=gif.',
            },
          ]
        : []),
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
          if (group.outputFormats) {
            return {
              ...parameter,
              values: 'GIF: auto; PNG/SVG: width x height',
              description:
                'GIF uses size=auto; the model chooses frame dimensions and aspect ratio. The listed 1K/2K/4K presets apply to static output.',
            }
          }
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
        values: group.outputFormats?.join(' | ') ?? 'png',
        description: group.outputFormats
          ? 'Select an output format supported by this exact model. PNG is the default.'
          : 'apiDocs.image25.outputFormatDescription',
      },
    ],
    requestNotes: [
      ...(group.id === 'all-image-2.6-sunburst-h'
        ? [
            'For sunburst-H GIF, omit quality and background. Reference editing uses JSON images: [{image_url: "data:image/png;base64,..."}] at /v1/images/edits?async=true; do not use multipart for this GIF route. PNG and SVG keep the normal image request format.',
          ]
        : []),
      ...(group.outputFormats
        ? [
            'For GIF, use size=auto and n=1. frame_count must be an integer from 2 to 16 (default 9); frame_duration_ms must be an integer from 20 to 2000 (default 200), rounded to 10 ms by the upstream. Omit frame fields for PNG or SVG.',
            'GIF size is the size of each frame. Width × height × frame_count × n consumes the total pixel budget. Do not reuse static 2K/4K sizes: 2880 × 2880 × 9 already totals 74,649,600 frame pixels. The budget depends on the upstream model; this example is not a limit.',
            'If GIF generation fails with a pixel-budget error, use size=auto, reduce frame_count, or reduce the number of animations. Do not retry unchanged parameters or only switch channels. For manual sizes, both dimensions must be positive multiples of 16 and still satisfy the upstream budget.',
            'Save the returned animation as .gif. Decode b64_json directly or download the original result bytes; converting the result to PNG loses animation. The GIF auto-size rule does not change static PNG/SVG sizing.',
            'Use background=transparent only with PNG. DD models are limited to 1K. DD and H models support SVG; standard 2.6 models support PNG and GIF.',
          ]
        : []),
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
