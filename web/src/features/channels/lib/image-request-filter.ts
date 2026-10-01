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

const imageRequestFilterSchema = z.strictObject({
  rules: z
    .array(
      z.strictObject({
        models: z
          .array(
            z
              .string()
              .min(1)
              .max(256)
              .refine((value) => value === value.trim())
          )
          .min(1)
          .max(256),
        qualities: z
          .array(
            z
              .string()
              .regex(/^[a-z][a-z0-9_-]{0,63}$/)
              .refine((value) => value !== 'auto')
          )
          .min(1)
          .max(32)
          .refine((values) => new Set(values).size === values.length),
        sizes: z
          .array(z.string().regex(/^[1-9]\d{0,5}x[1-9]\d{0,5}$/))
          .min(1)
          .max(256)
          .refine((values) => new Set(values).size === values.length),
      })
    )
    .min(1)
    .max(64)
    .refine((rules) => {
      const models = rules.flatMap((rule) => rule.models)
      return models.length <= 256 && new Set(models).size === models.length
    }),
})

const GPT_IMAGE_4K_SIZES = [
  '2880x2880',
  '3200x2560',
  '2160x3840',
  '3696x1584',
  '1584x3696',
  '1280x3840',
  '3840x1280',
  '1920x3840',
  '3840x1920',
  '3840x2160',
  '3264x2448',
  '3504x2336',
  '2560x3200',
  '2448x3264',
  '2336x3504',
]

export const GPT_IMAGE_4K_FILTER_PRESET = {
  rules: [
    {
      models: ['gpt-image-2'],
      qualities: ['medium', 'high'],
      sizes: GPT_IMAGE_4K_SIZES,
    },
    {
      models: ['gpt-image-2.5-sunburst', 'gpt-image-2.5-flare'],
      qualities: ['high', 'xhigh', 'max'],
      sizes: GPT_IMAGE_4K_SIZES,
    },
  ],
} satisfies z.infer<typeof imageRequestFilterSchema>

export function getImageRequestFilterError(
  value: string | undefined,
  enabled: boolean
): string | undefined {
  if (!value?.trim()) {
    return enabled
      ? 'Image request filtering requires a JSON configuration'
      : undefined
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    return 'Image request filter configuration must be valid JSON'
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return 'Image request filter configuration must be a JSON object'
  }
  if (enabled && !imageRequestFilterSchema.safeParse(parsed).success) {
    return 'Each rule requires unique models, allowed qualities, and exact sizes such as 2880x2880'
  }
  return undefined
}
