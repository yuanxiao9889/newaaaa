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
export type ModelProtocol =
  | 'OpenAI Images'
  | 'Gemini generateContent'
  | 'OOpii Dou 5.0 Pro'

export type ParameterDoc = {
  name: string
  type: string
  required: string
  values: string
  description: string
}

export type EndpointDoc = {
  method: 'GET' | 'POST'
  path: string
  contentType: string
  purpose: string
}

export type ImageModelDoc = {
  id: string
  model: string
  protocol: ModelProtocol
  family: 'openai' | 'gemini' | 'oopii'
  summary: string
  recommendedFor: string
  endpoints: EndpointDoc[]
  ratios: string[]
  resolutions: string[]
  parameters: ParameterDoc[]
  supportsEditing: boolean
  responsePath: string
  requestNotes: string[]
}

export const gptImageRatios = [
  '1:1',
  '2:3',
  '3:2',
  '3:4',
  '4:3',
  '4:5',
  '5:4',
  '9:16',
  '16:9',
  '21:9',
  '9:21',
  '1:3',
  '3:1',
  '1:2',
  '2:1',
]

export const genericImageRatios = [
  '1:1',
  '1:4',
  '1:8',
  '2:3',
  '3:2',
  '3:4',
  '4:1',
  '4:3',
  '4:5',
  '5:4',
  '8:1',
  '9:16',
  '16:9',
  '21:9',
  '9:21',
]

const openAIEndpoints: EndpointDoc[] = [
  {
    method: 'POST',
    path: '/v1/images/generations',
    contentType: 'application/json',
    purpose: 'Generate an image from a text prompt.',
  },
  {
    method: 'POST',
    path: '/v1/images/edits',
    contentType: 'multipart/form-data',
    purpose: 'Edit or restyle one or more reference images.',
  },
]

const grokEndpoints: EndpointDoc[] = [
  {
    method: 'POST',
    path: '/v1/images/generations',
    contentType: 'application/json',
    purpose:
      'Generate an image or submit embedded reference images in one JSON request.',
  },
]

const douEndpoints: EndpointDoc[] = [
  {
    method: 'POST',
    path: '/v1/uploads/images',
    contentType: 'multipart/form-data',
    purpose: 'Upload a reference image and receive a reusable image URL.',
  },
  {
    method: 'POST',
    path: '/v1/images/generations',
    contentType: 'application/json',
    purpose: 'Submit a text-to-image or reference-image generation task.',
  },
  {
    method: 'GET',
    path: '/v1/images/generations/{id}',
    contentType: 'application/json',
    purpose: 'Poll the asynchronous task until it succeeds or fails.',
  },
  {
    method: 'GET',
    path: '/v1/images/generations/{id}/content',
    contentType: 'image/*',
    purpose: 'Download the completed image bytes.',
  },
]

const douParameters: ParameterDoc[] = [
  {
    name: 'model',
    type: 'string',
    required: 'Required',
    values: 'dou-5-0-pro',
    description:
      'Use the public transport model ID dou-5-0-pro. Do not send the canvas-internal oopii/dou-5-0-pro ID.',
  },
  {
    name: 'prompt',
    type: 'string',
    required: 'Required',
    values: 'UTF-8 text',
    description:
      'Describe the subject, composition, style, lighting, and constraints.',
  },
  {
    name: 'image_url',
    type: 'string',
    required: 'Optional reference',
    values: 'HTTPS URL',
    description:
      'Use for one reference image. Upload local files first with /v1/uploads/images and pass the returned URL here.',
  },
  {
    name: 'image_urls',
    type: 'string[]',
    required: 'Optional references',
    values: 'HTTPS URL[]',
    description:
      'Use for multiple reference images. Keep this as a top-level array in the generation request.',
  },
  {
    name: 'strength',
    type: 'string',
    required: 'Optional reference',
    values: 'HIGH',
    description: 'Optional reference adherence setting supported by Dou 5.0 Pro.',
  },
  {
    name: 'size',
    type: 'string',
    required: 'Required',
    values: 'Pixel size such as 2048x1152',
    description:
      'Send a real width-by-height pixel value. Do not send 1K or 2K as the size.',
  },
]

const allImageParameters: ParameterDoc[] = [
  {
    name: 'model',
    type: 'string',
    required: 'Required',
    values: 'Exact model ID',
    description: 'The model ID must exactly match the selected model.',
  },
  {
    name: 'prompt',
    type: 'string',
    required: 'Required',
    values: 'UTF-8 text',
    description:
      'Describe the subject, composition, style, lighting, and constraints.',
  },
  {
    name: 'n',
    type: 'integer',
    required: 'Required by client',
    values: '1',
    description: 'Storyboard Copilot sends exactly one image per task.',
  },
  {
    name: 'size',
    type: 'string',
    required: 'Required by client',
    values: '2048x1152 (width-by-height)',
    description:
      'Send a lowercase pixel string such as 2048x1152; the first number is width and the second is height.',
  },
  {
    name: 'quality',
    type: 'string',
    required: 'Required by client',
    values: 'low | medium | high',
    description:
      'Storyboard Copilot sends medium by default; its OOpii model controls expose low, medium, and high.',
  },
  {
    name: 'response_format',
    type: 'string',
    required: 'Not sent by client',
    values: 'Omitted',
    description:
      'Storyboard Copilot omits this field for both generations and multipart edits; read whichever url or b64_json field the upstream response provides.',
  },
  {
    name: 'image / image[]',
    type: 'file',
    required: 'Edits only',
    values: 'PNG upload',
    description:
      'Storyboard Copilot converts references to PNG. One reference uses image; multiple references use repeated image[].',
  },
]

const gptImage2Parameters: ParameterDoc[] = allImageParameters.map(
  (parameter) =>
    parameter.name === 'quality'
      ? {
          ...parameter,
          values: 'auto | low | medium | high',
          description:
            'Storyboard Copilot defaults to medium. gpt-image-2 also accepts auto, low, medium, and high.',
        }
      : parameter
)

const grokParameters: ParameterDoc[] = [
  {
    name: 'model',
    type: 'string',
    required: 'Required',
    values: 'Exact Grok model ID',
    description: 'Use the exact OOpii transport model ID.',
  },
  {
    name: 'prompt',
    type: 'string',
    required: 'Required',
    values: 'UTF-8 text',
    description:
      'Describe the requested image or the semantic changes to apply to the references.',
  },
  {
    name: 'n',
    type: 'integer',
    required: 'Required by client',
    values: '1',
    description: 'Storyboard Copilot sends exactly one image per task.',
  },
  {
    name: 'response_format',
    type: 'string',
    required: 'Required by client',
    values: 'b64_json',
    description:
      'Storyboard Copilot requests a base64 image response for Grok.',
  },
  {
    name: 'resolution',
    type: 'string',
    required: 'Required by client',
    values: '1k | 2k',
    description:
      'Use the lowercase 1k or 2k resolution tier; Grok does not use an exact pixel size field.',
  },
  {
    name: 'aspect_ratio',
    type: 'string',
    required: 'Required by client',
    values: 'Model-supported ratio',
    description:
      'Use the selected ratio directly. grok-imagine-image-2.0 also accepts auto.',
  },
  {
    name: 'image / images',
    type: 'object | object[]',
    required: 'Reference images only',
    values: '{ "url": "data:image/png;base64,..." }',
    description:
      'Use image for one embedded data URL or images for multiple references. Grok reference requests remain JSON generations requests.',
  },
]

const grokImage2Parameters: ParameterDoc[] = [
  ...grokParameters,
  {
    name: 'quality',
    type: 'string',
    required: 'Required by client',
    values: 'low | medium',
    description:
      'Controls output quality and cost. Storyboard Copilot defaults to medium.',
  },
]

const geminiEndpoints: EndpointDoc[] = [
  {
    method: 'POST',
    path: '/v1beta/models/{model}:generateContent',
    contentType: 'application/json',
    purpose: 'Generate or edit an image with Gemini-compatible content parts.',
  },
]

const geminiParameters: ParameterDoc[] = [
  {
    name: 'contents',
    type: 'array',
    required: 'Required',
    values: 'Content[]',
    description:
      'Conversation content. Storyboard Copilot sends one user content item.',
  },
  {
    name: 'contents[].parts[].text',
    type: 'string',
    required: 'Required',
    values: 'Prompt plus preference suffix',
    description:
      'The first request appends [size: 2K, aspect ratio: 16:9] using the selected values.',
  },
  {
    name: 'contents[].parts[].inlineData',
    type: 'object',
    required: 'Reference images only',
    values: '{ mimeType: "image/png", data }',
    description:
      'Storyboard Copilot converts each reference to PNG and adds one inlineData part per image.',
  },
  {
    name: 'generationConfig.topP',
    type: 'number',
    required: 'Required by client',
    values: '0.95',
    description: 'Storyboard Copilot sends 0.95 on its Gemini image attempts.',
  },
  {
    name: 'generationConfig.responseModalities',
    type: 'string[]',
    required: 'Required by client',
    values: '["IMAGE"]',
    description: 'Requests image output from the Gemini-compatible endpoint.',
  },
  {
    name: 'generationConfig.imageConfig.aspectRatio',
    type: 'string',
    required: 'First attempt',
    values: 'Selected supported ratio',
    description:
      'Sent on the first request and removed automatically on the no-aspect compatibility retry.',
  },
  {
    name: 'generationConfig.imageConfig.imageSize',
    type: 'string',
    required: 'Required by client',
    values: '1K | 2K | 4K',
    description: 'Sets the requested image resolution tier.',
  },
]

export type OopiiCatalogModel = {
  id: string
  owner: string
  endpoints: string[]
}

export const oopiiModelGroups: Record<
  'text' | 'image' | 'video' | 'midjourney',
  OopiiCatalogModel[]
> = {
  text: [
    {
      id: 'all-5.4',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'all-5.5',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'claude-fable-5',
      owner: 'claude',
      endpoints: ['anthropic', 'openai'],
    },
    {
      id: 'claude-opus-4-6',
      owner: 'claude',
      endpoints: ['anthropic', 'openai'],
    },
    {
      id: 'claude-opus-4-7',
      owner: 'claude',
      endpoints: ['anthropic', 'openai'],
    },
    {
      id: 'claude-opus-4-8',
      owner: 'claude',
      endpoints: ['anthropic', 'openai'],
    },
    {
      id: 'claude-sonnet-5',
      owner: 'claude',
      endpoints: ['anthropic', 'openai'],
    },
    {
      id: 'gemini-3.1-pro-preview',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'gemini-3.1-pro-preview-c',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'gemini-3.5-flash',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'gemini-3.5-flash-c',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'gpt-5.4',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'gpt-5.5',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'gpt-5.6-luna',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'gpt-5.6-sol',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'gpt-5.6-terra',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'grok-4.5',
      owner: 'openai',
      endpoints: ['openai'],
    },
  ],
  image: [
    {
      id: 'all-image-2',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'all-image-2-DD',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'all-image-2-H',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'gpt-image-2',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'grok-imagine-image-2.0',
      owner: 'xai',
      endpoints: ['openai', 'openai-response'],
    },
    {
      id: 'grok-imagine-image-pro',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'monkey-image-flash 2',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'monkey-image-pro',
      owner: 'google gemini',
      endpoints: ['gemini', 'openai'],
    },
    {
      id: 'dou-5-0-pro',
      owner: 'oopii',
      endpoints: ['openai-async'],
    },
  ],
  video: [
    {
      id: 'MiniMax-H3',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'MiniMax-H3-C',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'ok-imagine-video',
      owner: 'openai',
      endpoints: ['openai'],
    },
    {
      id: 'ok-video-1.5-preview',
      owner: 'openai',
      endpoints: ['openai-video', 'openai'],
    },
  ],
  midjourney: [
    {
      id: 'mj_fast_custom_zoom',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_high_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_imagine',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_low_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_modal',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_pan',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_upload',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_upscale',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_fast_zoom',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_custom_zoom',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_high_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_imagine',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_low_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_modal',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_pan',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_upload',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_upscale',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_variation',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_relax_zoom',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
    {
      id: 'mj_upload',
      owner: 'midjourneyplus',
      endpoints: ['openai'],
    },
  ],
}

export const imageModels: ImageModelDoc[] = [
  {
    id: 'all-image-2',
    model: 'all-image-2',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'Recommended OOpii aggregate image model with generation and multi-reference editing.',
    recommendedFor:
      'Storyboard frames, character consistency, and general production image tasks.',
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: allImageParameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'Storyboard Copilot sends n=1, exact size, and quality for both generations and multipart edits without aspect_ratio, image_backend, or response_format.',
      'For edits, one normalized PNG reference uses image; multiple references use repeated image[].',
    ],
  },
  {
    id: 'all-image-2-dd',
    model: 'all-image-2-DD',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'DD routing variant of all-image-2 with the same OpenAI Images request contract.',
    recommendedFor:
      'Projects that explicitly need the OOpii DD route while keeping the same client code.',
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: allImageParameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'The transport model ID remains all-image-2-DD, including the uppercase DD suffix.',
      'Storyboard Copilot sends n=1, exact size, and quality for both generations and multipart edits without aspect_ratio, image_backend, or response_format.',
    ],
  },
  {
    id: 'all-image-2-h',
    model: 'all-image-2-H',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'H routing variant of all-image-2 with the same OpenAI Images generation and editing contract.',
    recommendedFor:
      'Projects that explicitly select the OOpii H route while preserving the aggregate image workflow.',
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: allImageParameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'The transport model ID remains all-image-2-H, including the uppercase H suffix.',
      'Storyboard Copilot sends n=1, exact size, and quality for generation and multipart edits without aspect_ratio, image_backend, or response_format.',
    ],
  },
  {
    id: 'gpt-image-2',
    model: 'gpt-image-2',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'OOpii official-mode image route using exact pixel sizes and OpenAI Images semantics.',
    recommendedFor:
      'High-fidelity image generation where exact output pixels and quality must be explicit.',
    endpoints: openAIEndpoints,
    ratios: gptImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: gptImage2Parameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'Storyboard Copilot derives the ratio from size and never sends aspect_ratio, image_backend, or response_format for either generations or multipart edits.',
      'For edits, one normalized PNG reference uses image; multiple references use repeated image[].',
    ],
  },
  {
    id: 'grok-imagine-image-pro',
    model: 'grok-imagine-image-pro',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'Grok-compatible image generation and embedded-reference editing through OOpii.',
    recommendedFor:
      'Fast creative iteration with common landscape, portrait, and square ratios.',
    endpoints: grokEndpoints,
    ratios: ['1:1', '16:9', '9:16', '2:3', '3:2'],
    resolutions: ['1K', '2K'],
    parameters: grokParameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'Text-only and reference-image operations both use /v1/images/generations with application/json.',
      'Storyboard Copilot always sends response_format=b64_json and never sends quality for this model.',
      'Storyboard Copilot sends resolution=1k|2k and aspect_ratio, with no size or compatibility aliases.',
      'A single semantic reference uses image; multiple references use images. Both fields contain objects with data-URL values.',
      'Asynchronous mode appends async=true for text-to-image, while reference-image requests keep the plain generations URL so embedded references are preserved.',
    ],
  },
  {
    id: 'grok-imagine-image-2',
    model: 'grok-imagine-image-2.0',
    protocol: 'OpenAI Images',
    family: 'openai',
    summary:
      'Grok Imagine Image 2.0 generation and semantic reference-image editing through OOpii.',
    recommendedFor:
      'Flexible social, cinematic, and mobile-native framing with selectable cost and quality tiers.',
    endpoints: grokEndpoints,
    ratios: [
      'auto',
      '1:1',
      '16:9',
      '9:16',
      '4:3',
      '3:4',
      '3:2',
      '2:3',
      '2:1',
      '1:2',
      '19.5:9',
      '9:19.5',
      '20:9',
      '9:20',
    ],
    resolutions: ['1K', '2K'],
    parameters: grokImage2Parameters,
    supportsEditing: true,
    responsePath: 'data[0].url or data[0].b64_json',
    requestNotes: [
      'Storyboard Copilot sends resolution=1k|2k, aspect_ratio, and quality=low|medium; quality defaults to medium.',
      'A single semantic reference uses image and multiple references use images, with a maximum of three reference images.',
      'This model does not support native masks or exact output pixel sizes.',
    ],
  },
  {
    id: 'monkey-image-pro',
    model: 'monkey-image-pro',
    protocol: 'Gemini generateContent',
    family: 'gemini',
    summary:
      'Gemini-compatible high-quality image route with inlineData reference-image support.',
    recommendedFor:
      'Detailed image generation and edits that need broad aspect-ratio coverage.',
    endpoints: geminiEndpoints,
    ratios: genericImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: geminiParameters,
    supportsEditing: true,
    responsePath: 'candidates[].content.parts[].inlineData.data',
    requestNotes: [
      'The first request sends topP=0.95, responseModalities=[IMAGE], imageSize, aspectRatio, and a matching prompt suffix.',
      'If the first request is rejected, Storyboard Copilot retries without aspectRatio and the prompt suffix; reference images may also be resized to a 1536 or 1024 pixel longest edge.',
    ],
  },
  {
    id: 'monkey-image-flash-2',
    model: 'monkey-image-flash 2',
    protocol: 'Gemini generateContent',
    family: 'gemini',
    summary:
      'Faster Gemini-compatible OOpii image route with the same content-parts request shape.',
    recommendedFor:
      'Rapid previews, batch storyboard drafts, and lower-latency iterations.',
    endpoints: geminiEndpoints,
    ratios: genericImageRatios,
    resolutions: ['1K', '2K', '4K'],
    parameters: geminiParameters,
    supportsEditing: true,
    responsePath: 'candidates[].content.parts[].inlineData.data',
    requestNotes: [
      'The model ID contains a space, so the request path encodes it as monkey-image-flash%202.',
      'The first request and compatibility retries use the same topP, prompt-suffix, and reference-image rules as monkey-image-pro.',
    ],
  },
  {
    id: 'dou-5-0-pro',
    model: 'dou-5-0-pro',
    protocol: 'OOpii Dou 5.0 Pro',
    family: 'oopii',
    summary:
      'OOpii asynchronous Dou 5.0 Pro image generation with URL-based reference images.',
    recommendedFor:
      'Production storyboard images that need high-fidelity text-to-image and reference-image control.',
    endpoints: douEndpoints,
    ratios: ['1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3'],
    resolutions: ['2048x2048', '2048x1152', '1152x2048'],
    parameters: douParameters,
    supportsEditing: true,
    responsePath: 'id, status, poll_url, content_url',
    requestNotes: [
      'The API base URL is https://www.oopii.cc. Authenticate with Authorization: Bearer <API_KEY> and optionally X-API-Key: <API_KEY>.',
      'Generation normally returns HTTP 202 and must be polled. A 202 response means the task was accepted, not that the image is ready.',
      'Use top-level image_url for one reference and top-level image_urls for multiple references; do not nest them under parameters.',
      'Upload JPEG, PNG, or WebP references to /v1/uploads/images using the image multipart field (file is accepted for compatibility). Each image is limited to about 8 MiB.',
      'The canvas may use oopii/dou-5-0-pro internally, but API requests must use dou-5-0-pro.',
    ],
  },
]

export const sizeRows = [
  ['1K', '1:1', '1024x1024'],
  ['1K', '16:9', '1280x720'],
  ['1K', '9:16', '720x1280'],
  ['1K', '3:2', '1248x832'],
  ['1K', '2:3', '832x1248'],
  ['2K', '1:1', '2048x2048'],
  ['2K', '16:9', '2048x1152'],
  ['2K', '9:16', '1152x2048'],
  ['2K', '3:2', '2496x1664'],
  ['2K', '2:3', '1664x2496'],
  ['4K', '1:1', '2880x2880'],
  ['4K', '16:9', '3840x2160'],
  ['4K', '9:16', '2160x3840'],
  ['4K', '3:2', '3504x2336'],
  ['4K', '2:3', '2336x3504'],
] as const
