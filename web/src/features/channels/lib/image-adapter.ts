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
import { z } from 'zod'

const fieldName = z.string().regex(/^[A-Za-z_][A-Za-z0-9_]*$/)
const responsePath = z
  .string()
  .regex(/^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*$/)
const upstreamPath = z
  .string()
  .refine(
    (value) =>
      value.startsWith('/') && !value.startsWith('//') && !/[\s\\#]/.test(value)
  )
const taskURL = upstreamPath.refine((value) => value.includes('{task_id}'))
const fields = z.array(fieldName)
const strings = z.array(z.string().min(1))
const stringMap = z.record(fieldName, z.string())

export const imageAdapterConfigSchema = z.strictObject({
  models: strings.optional(),
  request: z.strictObject({
    generations_path: upstreamPath.optional(),
    edits_path: upstreamPath.optional(),
    field_map: z.record(fieldName, fieldName).optional(),
    value_map: z.record(fieldName, z.record(z.string(), z.string())).optional(),
    size_map: z.record(z.string().regex(/^\d+x\d+$/), stringMap).optional(),
    drop_fields: fields.optional(),
    reject_fields: fields.optional(),
  }),
  references: z
    .strictObject({
      field: fieldName,
      encoding: z.enum(['url_array', 'image_url_content']),
      max_images: z.number().int().min(1).max(16),
      upload_local: z.boolean(),
    })
    .optional(),
  task: z
    .strictObject({
      id_paths: z.array(responsePath).min(1),
      status_path: responsePath,
      result_url_path: responsePath.optional(),
      result_b64_path: responsePath.optional(),
      error_path: responsePath.optional(),
      poll_url_template: taskURL,
      content_url_template: taskURL.optional(),
      pending_states: strings.optional(),
      success_states: strings.min(1),
      failure_states: strings.min(1),
      poll_interval_seconds: z
        .union([z.literal(0), z.number().int().min(3).max(60)])
        .optional(),
    })
    .refine((task) =>
      Boolean(
        task.result_url_path ||
        task.result_b64_path ||
        task.content_url_template
      )
    )
    .optional(),
})

export type ImageAdapterConfig = z.infer<typeof imageAdapterConfigSchema>

/** Parse the editor body; the separate switch owns the enabled setting. */
export function parseImageAdapterConfig(
  value: string | undefined
): ImageAdapterConfig | undefined {
  if (!value?.trim()) return undefined
  return imageAdapterConfigSchema.parse(JSON.parse(value))
}

export function getImageAdapterConfigError(
  value: string | undefined,
  enabled: boolean
): string | undefined {
  if (!value?.trim()) {
    return enabled
      ? 'Image conversion requires a JSON configuration'
      : undefined
  }
  try {
    const parsed: unknown = JSON.parse(value)
    if (!isRecord(parsed)) {
      return 'Image conversion configuration must be a JSON object'
    }
    if (enabled) imageAdapterConfigSchema.parse(parsed)
    return undefined
  } catch (error) {
    if (error instanceof SyntaxError) {
      return 'Image conversion configuration must be valid JSON'
    }
    return 'Invalid image conversion configuration. Check field names, relative paths, task paths, and numeric limits'
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Keep custom rules and only add image-specific constants with no existing owner. */
export function mergeImageAdapterParameterOverride(value: string | undefined): {
  value: string
  preservedFields: string[]
} {
  const parsed: unknown = value?.trim() ? JSON.parse(value) : {}
  if (
    !isRecord(parsed) ||
    (parsed.operations !== undefined &&
      (!Array.isArray(parsed.operations) ||
        !parsed.operations.every(
          (operation) =>
            isRecord(operation) &&
            typeof operation.mode === 'string' &&
            operation.mode.trim().length > 0
        )))
  ) {
    throw new Error(
      'Parameter override must be a JSON object with valid operations'
    )
  }
  const operations: Record<string, unknown>[] = Array.isArray(parsed.operations)
    ? parsed.operations
    : []
  const preservedFields: string[] = []
  const additions: Record<string, unknown>[] = []
  for (const [path, fieldValue] of Object.entries({
    async: true,
    n: 1,
    stream: false,
  })) {
    const controlled =
      Object.keys(parsed).some((key) => key === path || key === '*') ||
      operations.some((operation) =>
        [operation.path, operation.to].some(
          (target) =>
            typeof target === 'string' &&
            (target === path || target === '*' || target === '')
        )
      )
    if (controlled) {
      preservedFields.push(path)
      continue
    }
    additions.push({
      path,
      mode: 'set',
      value: fieldValue,
      conditions: [
        { path: 'request_path', mode: 'prefix', value: '/v1/images/' },
        { path: 'image_adapter_enabled', mode: 'full', value: true },
      ],
      logic: 'AND',
    })
  }
  return {
    value: JSON.stringify(
      { ...parsed, operations: [...operations, ...additions] },
      null,
      2
    ),
    preservedFields,
  }
}
