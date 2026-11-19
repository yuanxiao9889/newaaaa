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
import type { ContractParameter } from './contract-parameters'

export type GenerationContract = {
  id: string
  title: string
  model: string
  description: string
  endpoints: { method: 'POST' | 'GET'; path: string }[]
  parameters: ContractParameter[]
  notes: string[]
  examples: { title: string; path: string; body: Record<string, unknown> }[]
  responses: Record<string, unknown>[]
}

const imagePath = '/v1/images/generations?async=true'
const editPath = '/v1/images/edits?async=true'
const imageBody = {
  model: 'Midjourney v8.2',
  prompt: 'A cinematic mountain landscape at sunrise.',
  n: 1,
  size: '2048x1152',
  resolution: '2k',
  quality: '1',
  response_format: 'url',
}

export const generationContracts: GenerationContract[] = [
  {
    id: 'dou55-image',
    title: 'Dou 5.5',
    model: 'dou-5-5',
    description:
      'Dou 5.5 uses the Images API with exact pixel sizes and public reference URLs.',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/images/generations?async=true',
      },
      {
        method: 'POST',
        path: '/v1/images/edits?async=true',
      },
      {
        method: 'GET',
        path: '/v1/images/tasks/{task_id}',
      },
      {
        method: 'GET',
        path: '/v1/images/tasks/{task_id}/content?index=0',
      },
    ],
    parameters: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'Exact API model ID',
        values: 'dou-5-5',
      },
      {
        name: 'prompt',
        type: 'string',
        required: true,
        description:
          'Describe the subject, composition, style, lighting, and constraints.',
      },
      {
        name: 'n',
        type: 'integer',
        required: false,
        description: 'One image per request',
        values: '1',
      },
      {
        name: 'size',
        type: 'string',
        required: false,
        description: 'Aspect ratio to size mapping',
        values:
          '1:1→2048x2048; 16:9→2048x1152; 9:16→1152x2048; 4:3→2048x1536; 3:4→1536x2048; 21:9→2048x878',
      },
      {
        name: 'image',
        type: 'string[]',
        required: false,
        description: 'Reference image URLs',
        values: 'HTTPS URL[]; ≤9',
      },
      {
        name: 'watermark / response_format',
        type: 'boolean / string',
        required: false,
        description: 'Fixed values for this integration',
        values: 'watermark=false; response_format="url"',
      },
    ],
    notes: [
      'Use the shared 2K sizes above for portable Dou 5.5 requests. The canvas also offers 1K and additional ratios, but support varies by channel. Do not copy GPT quality, strength or GIF parameters.',
      'For Dou 5.5 edits, JSON image contains public URLs. The canvas uploads multipart image/image[] and may attach oopii_reference_urls_v1 for URL-only channels. These are two alternative transports; do not combine URL and file references in one request.',
      'Submit once with async=true and save task_id. Poll every 5 seconds until succeeded, failed or expired; progress alone is not completion.',
    ],
    examples: [
      {
        title: 'Text-to-image example',
        path: '/v1/images/generations?async=true',
        body: {
          model: 'dou-5-5',
          prompt: 'A cinematic mountain landscape at sunrise.',
          n: 1,
          size: '2048x1152',
          watermark: false,
          response_format: 'url',
        },
      },
      {
        title: 'Reference image editing',
        path: '/v1/images/edits?async=true',
        body: {
          model: 'dou-5-5',
          prompt: 'A cinematic mountain landscape at sunrise.',
          n: 1,
          size: '2048x1152',
          watermark: false,
          response_format: 'url',
          image: ['https://your-storage.example.com/reference.png'],
        },
      },
    ],
    responses: [
      {
        task_id: 'image_task_example',
        status: 'submitted',
        status_url: '/v1/images/tasks/image_task_example',
        content_url: '/v1/images/tasks/image_task_example/content',
      },
      {
        task_id: 'image_task_example',
        status: 'succeeded',
        data: [
          {
            url: 'https://your-storage.example.com/result.png',
          },
        ],
      },
    ],
  },
  {
    id: 'suno-music',
    title: 'Suno music',
    model: 'suno_music',
    description:
      'Generate songs from an idea or custom lyrics, then download every audio artifact.',
    endpoints: [
      {
        method: 'POST',
        path: '/jieapi-suno/submit/music',
      },
      {
        method: 'GET',
        path: '/jieapi-suno/fetch/{task_id}',
      },
      {
        method: 'GET',
        path: '/v1/tasks/{task_id}/artifacts',
      },
      {
        method: 'GET',
        path: '/v1/tasks/{task_id}/artifacts/{key}/content',
      },
    ],
    parameters: [
      {
        name: 'mv',
        type: 'string',
        required: true,
        description: 'Version',
        values:
          'chirp-v3.0 | chirp-v3.5 | chirp-v4 | chirp-auk | chirp-bluejay | chirp-crow',
      },
      {
        name: 'gpt_description_prompt',
        type: 'string',
        required: false,
        description: 'Describe the song in inspiration mode.',
        values: '≤3000',
      },
      {
        name: 'prompt / tags / title',
        type: 'string',
        required: false,
        description:
          'Custom mode uses lyrics in prompt, musical style in tags and a song title.',
        values: 'prompt≤5000; tags≤1000; title≤100',
      },
      {
        name: 'make_instrumental',
        type: 'boolean',
        required: false,
        description:
          'Use true for instrumental music; custom instrumental requests use empty prompt and non-empty tags.',
        values: 'true | false',
      },
    ],
    notes: [
      'The route selects suno_music; mv selects the Suno version. Do not send an OpenAI audio input field. Length limits shown here follow the canvas.',
      'Save the task ID from data. Poll until data.status is SUCCESS or FAILURE; NOT_START, SUBMITTED, QUEUED and IN_PROGRESS are pending. On success, list artifacts and download each audio key. Do not expect song URLs in the polling response.',
    ],
    examples: [
      {
        title: 'Request example',
        path: '/jieapi-suno/submit/music',
        body: {
          mv: 'chirp-crow',
          make_instrumental: false,
          gpt_description_prompt: 'A hopeful folk song about returning home.',
        },
      },
      {
        title: 'Custom lyrics',
        path: '/jieapi-suno/submit/music',
        body: {
          mv: 'chirp-crow',
          make_instrumental: false,
          prompt: '[Verse]\nThe morning light will lead us home.',
          tags: 'acoustic folk, warm vocals',
          title: 'Morning Light',
        },
      },
    ],
    responses: [
      {
        code: 'success',
        data: 'task_example',
      },
      {
        code: 'success',
        data: {
          task_id: 'task_example',
          status: 'SUCCESS',
          data: null,
        },
      },
      {
        task_id: 'task_example',
        artifacts: [
          {
            key: 'audio_example',
            type: 'audio',
            mime_type: 'audio/mpeg',
          },
        ],
      },
    ],
  },
  {
    id: 'suno-lyrics',
    title: 'Suno lyrics',
    model: 'suno_lyrics',
    description: 'Generate lyrics through the native Suno task API.',
    endpoints: [
      {
        method: 'POST',
        path: '/jieapi-suno/submit/lyrics',
      },
      {
        method: 'GET',
        path: '/jieapi-suno/fetch/{task_id}',
      },
    ],
    parameters: [
      {
        name: 'prompt',
        type: 'string',
        required: true,
        description: 'Prompt',
      },
    ],
    notes: [
      'The lyrics route selects suno_lyrics and takes only prompt. Poll the saved task ID; read the lyrics result from data.data after SUCCESS. Lyrics do not create audio artifacts.',
    ],
    examples: [
      {
        title: 'Request example',
        path: '/jieapi-suno/submit/lyrics',
        body: {
          prompt: 'Write hopeful lyrics about returning home.',
        },
      },
    ],
    responses: [
      {
        code: 'success',
        data: 'task_example',
      },
    ],
  },
  {
    id: 'flux3-image',
    title: 'FLUX 3 Image',
    model: 'flux-3-image',
    description:
      'Generate or edit one image with FLUX 3. Use the exact sizes accepted by the gateway.',
    endpoints: [
      {
        method: 'POST',
        path: '/v1/images/generations?async=true',
      },
      {
        method: 'POST',
        path: '/v1/images/edits?async=true',
      },
      {
        method: 'GET',
        path: '/v1/images/tasks/{task_id}',
      },
      {
        method: 'GET',
        path: '/v1/images/tasks/{task_id}/content?index=0',
      },
    ],
    parameters: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'Exact API model ID',
        values: 'flux-3-image',
      },
      {
        name: 'prompt',
        type: 'string',
        required: true,
        description:
          'Describe the subject, composition, style, lighting, and constraints.',
      },
      {
        name: 'n',
        type: 'integer',
        required: false,
        description: 'One image per request',
        values: '1',
      },
      {
        name: 'size',
        type: 'string',
        required: false,
        description:
          'Use one of these exact pixel sizes; portrait 2K and 4K are not accepted by the current gateway.',
        values:
          'auto | 1024x1024 | 1536x1024 | 1024x1536 | 2560x1440 | 3840x2160',
      },
      {
        name: 'image',
        type: 'string[]',
        required: false,
        description:
          'Up to 16 public image URLs or PNG/JPEG/WebP data URLs; each reference must fit within 20 MiB. Editing requires at least one reference.',
      },
      {
        name: 'output_format',
        type: 'string',
        required: false,
        description: 'Output format',
        values: 'png (default) | jpeg | webp',
      },
      {
        name: 'response_format',
        type: 'string',
        required: false,
        description: 'Response format',
        values: 'url | b64_json',
      },
      {
        name: 'quality',
        type: 'string',
        required: false,
        description: 'Quality',
        values: 'auto | low | medium | high',
      },
    ],
    notes: [
      'FLUX does not accept mask, background, images, resolution or aspect_ratio. Use image and size; GIF and SVG are not supported.',
      'Submit once with async=true and save task_id. Poll every 5 seconds until succeeded, failed or expired; progress alone is not completion.',
    ],
    examples: [
      {
        title: 'Request example',
        path: '/v1/images/generations?async=true',
        body: {
          model: 'flux-3-image',
          prompt: 'A cinematic mountain landscape at sunrise.',
          n: 1,
          size: '1024x1024',
          output_format: 'png',
          response_format: 'url',
        },
      },
      {
        title: 'Reference image editing',
        path: '/v1/images/edits?async=true',
        body: {
          model: 'flux-3-image',
          prompt: 'A cinematic mountain landscape at sunrise.',
          n: 1,
          size: '1024x1024',
          output_format: 'png',
          response_format: 'url',
          image: ['https://your-storage.example.com/reference.png'],
        },
      },
    ],
    responses: [
      {
        task_id: 'image_task_example',
        status: 'submitted',
        status_url: '/v1/images/tasks/image_task_example',
        content_url: '/v1/images/tasks/image_task_example/content',
      },
      {
        task_id: 'image_task_example',
        status: 'succeeded',
        data: [
          {
            url: 'https://your-storage.example.com/result.png',
          },
        ],
      },
    ],
  },
  {
    id: 'seed-audio',
    title: 'Audio generation',
    model: 'seed-audio-1.0',
    description:
      'Generate speech, music and sound effects from a text prompt. This route returns audio directly as JSON.',
    endpoints: [
      { method: 'POST', path: '/jieapi/v1/audio/speech?async=true' },
      { method: 'GET', path: '/v1/audio/tasks/{task_id}' },
      { method: 'GET', path: '/v1/audio/tasks/{task_id}/content' },
      { method: 'POST', path: '/jieapi/v1/audio/speech' },
    ],
    parameters: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'Exact API model ID',
        values: 'seed-audio-1.0',
      },
      {
        name: 'text_prompt',
        type: 'string',
        required: true,
        description: 'Describe the audio and reference speakers in the prompt.',
        values: '1–3000 Unicode characters; @音频1 / @音频2 / @音频3 / @图片1',
      },
      {
        name: 'refs',
        type: 'object[]',
        required: false,
        description:
          'Use up to three audio references or one image; do not mix audio and images.',
        values:
          'maxItems(audio)=3; maxItems(image)=1; audio ≤30s; file ≤10MiB; image=jpeg/png/webp',
      },
      {
        name: 'refs[]',
        type: 'object',
        required: false,
        description:
          'Each reference contains exactly one of these fields. Base64 data must not include a data URL prefix.',
        values: 'speaker | audio_data | audio_url | image_data | image_url',
      },
      {
        name: 'audio_config.format',
        type: 'string',
        required: false,
        description: 'Audio encoding format',
        values: 'mp3 (default) | wav | pcm | ogg_opus',
      },
      {
        name: 'audio_config.sample_rate',
        type: 'integer',
        required: false,
        description: 'Sample rate in Hz depends on the encoding format.',
        values:
          'mp3: 8000,16000,24000,32000,44100,48000; wav/pcm: +40000; ogg_opus: 48000',
      },
      {
        name: 'audio_config.speech_rate / loudness_rate',
        type: 'integer',
        required: false,
        description: 'Speech speed and loudness adjustments',
        values: '-50…100',
      },
      {
        name: 'audio_config.pitch_rate',
        type: 'integer',
        required: false,
        description: 'Pitch adjustment',
        values: '-12…12',
      },
      {
        name: 'audio_config.enable_subtitle',
        type: 'boolean',
        required: false,
        description: 'Include subtitles in the response.',
        values: 'true | false',
      },
    ],
    notes: [
      'Seed Audio also supports async=true. Save task_id, poll /v1/audio/tasks/{task_id} until succeeded, failed or expired, then download /content. Without async=true, read the synchronous audio response directly.',
      'Use /jieapi/v1/audio/speech, not /v1/audio/speech. Send text_prompt, not input. Do not send duration, seconds, n or stream.',
      'Read audio (base64) or url from the synchronous response. Save the media promptly; the supplier URL is temporary. No task polling is required.',
      'duration is the processed audio length; original_duration is the original length used for billing, greater than 0 and at most 120 seconds.',
    ],
    examples: [
      {
        title: 'Asynchronous request example',
        path: '/jieapi/v1/audio/speech?async=true',
        body: {
          model: 'seed-audio-1.0',
          text_prompt: 'A calm narrator says: Welcome to our story.',
          audio_config: { format: 'mp3', sample_rate: 24000 },
        },
      },
      {
        title: 'Request example',
        path: '/jieapi/v1/audio/speech',
        body: {
          model: 'seed-audio-1.0',
          text_prompt: 'A calm narrator says: Welcome to our story.',
          audio_config: {
            format: 'mp3',
            sample_rate: 24000,
            enable_subtitle: true,
          },
        },
      },
      {
        title: 'Audio reference example',
        path: '/jieapi/v1/audio/speech',
        body: {
          model: 'seed-audio-1.0',
          text_prompt: '@音频1 says: Welcome back.',
          refs: [{ audio_url: 'https://media.example.com/voice.mp3' }],
          audio_config: { format: 'mp3' },
        },
      },
    ],
    responses: [
      {
        audio: '<base64>',
        url: 'https://media.example.com/result.mp3',
        duration: 6.2,
        original_duration: 6.2,
      },
    ],
  },
  {
    id: 'midjourney-image',
    title: 'Midjourney independent image API',
    model: 'Midjourney v8.2',
    description:
      'This is the independent model in the AI image node. Use the Images API; the separate Midjourney task node uses /mj routes.',
    endpoints: [
      { method: 'POST', path: imagePath },
      { method: 'POST', path: editPath },
      { method: 'GET', path: '/v1/images/tasks/{task_id}' },
      { method: 'GET', path: '/v1/images/tasks/{task_id}/content?index=0' },
    ],
    parameters: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'Exact API model ID',
        values: 'Midjourney v8.2',
      },
      {
        name: 'prompt',
        type: 'string',
        required: true,
        description:
          'Use plain text in prompt; put controls in their own JSON fields.',
        values: '1–1024 characters; no -- commands',
      },
      {
        name: 'n / resolution / response_format',
        type: 'integer / string / string',
        required: true,
        description: 'Fixed values for this integration',
        values: 'n=1; resolution="2k"; response_format="url"',
      },
      {
        name: 'size',
        type: 'string',
        required: true,
        description: 'Aspect ratio to size mapping',
        values:
          '1:1→2048x2048; 16:9→2048x1152; 9:16→1152x2048; 4:3→2048x1536; 3:4→1536x2048; 3:2→2048x1360; 2:3→1360x2048; 4:5→1632x2040; 5:4→5:4; 21:9→2016x864',
      },
      {
        name: 'quality',
        type: 'string',
        required: false,
        description: 'Quality value must be a string.',
        values: '"0.25" | "0.5" | "1" | "2"',
      },
      {
        name: 'image',
        type: 'string',
        required: false,
        description:
          'Required for edits: one image URL or data URL. Edits also use application/json.',
        values: 'maxImages=1; file ≤10MiB; https://… | data:image/png;base64,…',
      },
      {
        name: 'iw',
        type: 'number',
        required: false,
        description: 'Image reference weight for edits',
        values: '0.5 | 1 | 2 | 3',
      },
      {
        name: 'style / stylize',
        type: 'string / integer',
        required: false,
        description: 'Style controls',
        values: 'style=""|"raw"; stylize=0…1000 (default 100)',
      },
      {
        name: 'chaos / weird / seed',
        type: 'integer',
        required: false,
        description: 'Variation and random seed controls',
        values: 'chaos=0…100; weird=0…3000; seed=0…4294967295',
      },
      {
        name: 'sref / sw',
        type: 'string / integer',
        required: false,
        description: 'Style reference and weight',
        values: 'sref=string; sw=0…1000',
      },
      {
        name: 'tile / negative_prompt / output_format',
        type: 'boolean / string / string',
        required: false,
        description: 'Optional output controls',
        values:
          'tile=true|false; negative_prompt=string; output_format=jpeg|png',
      },
      {
        name: 'extra',
        type: 'string',
        required: false,
        description:
          'Optional style codes; never send the canvas internal field mj_style_codes.',
        values: '--p | --profile | --sref',
      },
    ],
    notes: [
      'Use the exact model Midjourney v8.2 without the internal oopii/ prefix. Midjourney v8.1 is historical.',
      'Submit once with async=true and save task_id. Poll every 5 seconds until succeeded, failed or expired; progress alone is not completion.',
      'A single generation can return multiple images. Read the full data array and download each zero-based index after succeeded.',
      'On a polling timeout, resume GET with the saved task ID. Do not automatically retry a billed POST after an uncertain network failure.',
    ],
    examples: [
      { title: 'Text-to-image example', path: imagePath, body: imageBody },
      {
        title: 'Image editing example',
        path: editPath,
        body: {
          ...imageBody,
          image: 'https://media.example.com/reference.jpg',
          iw: 1,
        },
      },
    ],
    responses: [
      {
        task_id: 'img_example',
        status: 'submitted',
        status_url: '/v1/images/tasks/img_example',
        content_url: '/v1/images/tasks/img_example/content',
        expires_at: 0,
      },
      {
        task_id: 'img_example',
        status: 'succeeded',
        data: [
          { url: 'https://media.example.com/image-1.png' },
          { url: 'https://media.example.com/image-2.png' },
        ],
      },
    ],
  },
]
