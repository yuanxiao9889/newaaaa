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

const pool = z
  .object({
    global: z.number().int().min(1).max(32),
    per_account: z.number().int().min(1).max(32),
    queue: z.number().int().min(0).max(64),
    per_account_queue: z.number().int().min(0).max(64),
    wait_ms: z.number().int().min(0).max(3000),
  })
  .refine(
    (p) =>
      p.per_account <= p.global &&
      p.per_account_queue <= p.queue &&
      (p.queue === 0
        ? p.per_account_queue === 0 && p.wait_ms === 0
        : p.per_account_queue > 0 && p.wait_ms > 0)
  )

export const capacitySchema = z
  .object({
    upload: pool,
    download: pool,
    bandwidth_bytes_per_second: z
      .number()
      .int()
      .min(128 * 1024)
      .max(1024 ** 3),
    api_reserved_bytes_per_second: z
      .number()
      .int()
      .min(64 * 1024),
  })
  .refine(
    (c) =>
      c.upload.global + c.download.global <= 48 &&
      c.api_reserved_bytes_per_second <=
        c.bandwidth_bytes_per_second - 64 * 1024
  )

export type BridgeCapacityConfig = z.infer<typeof capacitySchema>
