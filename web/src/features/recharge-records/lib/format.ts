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
import { formatNumber } from '@/lib/format'

import type { RechargeRecord } from '../types'

export function formatPaidAmount(
  amount: number | null | undefined,
  currency: string,
  locale: string | undefined
): string | null {
  if (amount == null || Number.isNaN(amount) || !currency) return null
  try {
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      currencyDisplay: 'narrowSymbol',
      maximumFractionDigits: 2,
    }).format(amount)
  } catch {
    return `${formatNumber(amount, locale)} ${currency}`
  }
}

export function getRecordPaidAmount(
  record: RechargeRecord,
  locale: string | undefined
): string | null {
  if (!record.paid_amount_confirmed) return null
  return formatPaidAmount(record.paid_amount, record.currency, locale)
}

export function getRecordDisplayName(record: RechargeRecord): string {
  if (record.username) return record.username
  return `#${record.user_id}`
}

export function buildIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID()
  }
  return `recharge-invoice-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
