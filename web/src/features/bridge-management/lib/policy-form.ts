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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import type { FieldSpec, TransferPolicy } from '../types'

export function createBridgePolicySchema(
  fields: FieldSpec[],
  savedPolicy: TransferPolicy,
  t: TFunction
) {
  return z.object({
    reason: z
      .string()
      .trim()
      .min(1, t('Enter a reason for this change'))
      .refine(
        (value) => [...value].length <= 500,
        t('The change reason must not exceed 500 characters')
      ),
    policy: z
      .object({
        enabled: z.boolean(),
        origin_base_url: z.literal(savedPolicy.origin_base_url),
        bridge_base_url: z.literal(savedPolicy.bridge_base_url),
      })
      .catchall(z.union([z.number(), z.string(), z.boolean()]))
      .superRefine((policy, context) => {
        for (const field of fields) {
          const value = policy[field.key]
          if (
            typeof value !== 'number' ||
            !Number.isFinite(value) ||
            value < field.min ||
            value > field.max ||
            (field.unit !== 'ratio' && !Number.isInteger(value))
          ) {
            context.addIssue({
              code: 'custom',
              path: [field.key],
              message: t('Enter a valid value between {{min}} and {{max}}', {
                min: field.min,
                max: field.max,
              }),
            })
          }
        }
      }),
  })
}

export type BridgePolicyFormValues = z.infer<
  ReturnType<typeof createBridgePolicySchema>
>
