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

export type MidjourneyEndpointDoc = {
  method: 'GET' | 'POST'
  path: string
  purpose: string
  payload: string
  modelKey: string
}

export type MidjourneyParameterDoc = {
  name: string
  type: string
  required: string
  values: string
  description: string
}

export const midjourneyNavItems = [
  ['midjourney-overview', 'Midjourney overview'],
  ['midjourney-quickstart', 'Start with a text-only request'],
  ['midjourney-endpoints', 'Midjourney endpoints'],
  ['midjourney-parameters', 'Submission parameters'],
  ['midjourney-workflow', 'Submit and poll workflow'],
  ['midjourney-actions', 'Button actions and modal editing'],
  ['midjourney-responses', 'Task responses'],
] as const

export const midjourneyEndpoints: MidjourneyEndpointDoc[] = [
  {
    method: 'POST',
    path: '/mj/submit/imagine',
    purpose: 'Create an image task from a text prompt and optional references.',
    payload: 'prompt, botType, base64Array?, speedMode?',
    modelKey: 'mj_imagine',
  },
  {
    method: 'POST',
    path: '/mj/submit/upload-discord-images',
    purpose: 'Upload data-URL images for later Midjourney operations.',
    payload: 'base64Array',
    modelKey: 'mj_upload',
  },
  {
    method: 'POST',
    path: '/mj/submit/action',
    purpose: 'Run a button action returned by a completed task.',
    payload: 'taskId, customId, speedMode?',
    modelKey: 'Dynamic action key',
  },
  {
    method: 'POST',
    path: '/mj/submit/change',
    purpose:
      'Run an upscale, variation, reroll, pan, or zoom action explicitly.',
    payload: 'taskId, action, index, speedMode?',
    modelKey: 'mj_{action}',
  },
  {
    method: 'POST',
    path: '/mj/submit/simple-change',
    purpose: 'Run U1-U4, V1-V4, or reroll with the compact content syntax.',
    payload: 'content',
    modelKey: 'mj_{action}',
  },
  {
    method: 'POST',
    path: '/mj/submit/blend',
    purpose: 'Blend multiple input images into a new image task.',
    payload: 'base64Array, botType, dimensions?',
    modelKey: 'mj_blend',
  },
  {
    method: 'POST',
    path: '/mj/submit/describe',
    purpose: 'Generate prompt suggestions from an input image.',
    payload: 'base64, botType?',
    modelKey: 'mj_describe',
  },
  {
    method: 'POST',
    path: '/mj/submit/shorten',
    purpose: 'Ask a compatible Midjourney Plus channel to shorten a prompt.',
    payload: 'prompt',
    modelKey: 'mj_shorten',
  },
  {
    method: 'POST',
    path: '/mj/submit/edits',
    purpose: 'Forward a provider-compatible Midjourney edit request.',
    payload: 'prompt and provider edit fields',
    modelKey: 'mj_edits',
  },
  {
    method: 'GET',
    path: '/mj/task/{id}/fetch',
    purpose: 'Read the locally tracked task status and result metadata.',
    payload: 'Path: id',
    modelKey: 'No submit charge',
  },
  {
    method: 'POST',
    path: '/mj/task/list-by-condition',
    purpose: 'Read multiple locally tracked tasks by their task IDs.',
    payload: 'ids',
    modelKey: 'No submit charge',
  },
  {
    method: 'GET',
    path: '/mj/task/{id}/image-seed',
    purpose: 'Request the image seed through the original task channel.',
    payload: 'Path: id',
    modelKey: 'No submit charge',
  },
  {
    method: 'GET',
    path: '/mj/image/{id}',
    purpose:
      'Stream the task image through this gateway when image forwarding is enabled.',
    payload: 'Path: id',
    modelKey: 'No submit charge',
  },
]

export const midjourneyCommonParameters: MidjourneyParameterDoc[] = [
  {
    name: 'botType',
    type: 'string',
    required: 'Recommended',
    values: 'MID_JOURNEY | NIJI_JOURNEY',
    description:
      'Select the standard Midjourney bot or Niji. Omit only when the configured upstream supplies a default.',
  },
  {
    name: 'speedMode',
    type: 'string',
    required: 'Optional',
    values: 'fast | relax',
    description:
      'Select the fast or relax routing pool. Omit the field to use the existing default Midjourney model key.',
  },
  {
    name: 'notifyHook',
    type: 'string',
    required: 'Conditional',
    values: 'HTTPS callback URL',
    description:
      'Forwarded only when Midjourney callbacks are enabled by the administrator. Polling remains the recommended client workflow.',
  },
  {
    name: 'state',
    type: 'string',
    required: 'Optional',
    values: 'Client-defined string',
    description:
      'Opaque client state stored with the task and returned by task queries.',
  },
]

export const midjourneyTaskFields: MidjourneyParameterDoc[] = [
  {
    name: 'id',
    type: 'string',
    required: 'Always returned',
    values: 'Task ID',
    description:
      'Use this ID for polling, actions, seed lookup, and image forwarding.',
  },
  {
    name: 'status',
    type: 'string',
    required: 'Always returned',
    values: 'NOT_START | SUBMITTED | IN_PROGRESS | SUCCESS | FAILURE',
    description: 'The normalized task lifecycle state tracked by this gateway.',
  },
  {
    name: 'progress',
    type: 'string',
    required: 'Always returned',
    values: '0% - 100%',
    description:
      'Provider-reported progress. Treat status as the authoritative completion signal.',
  },
  {
    name: 'imageUrl',
    type: 'string',
    required: 'On image result',
    values: 'HTTPS URL',
    description:
      "The upstream image URL or this deployment's /mj/image/{id} forwarding URL, depending on server settings.",
  },
  {
    name: 'buttons',
    type: 'array',
    required: 'On supported results',
    values: '{ customId, label, emoji, type, style }[]',
    description:
      'Available follow-up actions. Send a returned customId together with the original task ID to /mj/submit/action.',
  },
  {
    name: 'failReason',
    type: 'string',
    required: 'On failure',
    values: 'Provider or gateway message',
    description:
      'Failure detail for diagnostics. Do not continue polling after a terminal failure.',
  },
]
