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
import type { StatusVariant } from '@/components/status-badge'

import type {
  InvoiceAction,
  InvoiceStatus,
  RechargeSourceKind,
  RechargeStatus,
} from './types'

export const RECHARGE_SOURCE_OPTIONS: {
  value: RechargeSourceKind
  labelKey: string
  variant: StatusVariant
}[] = [
  { value: 'online', labelKey: 'Online recharge', variant: 'success' },
  { value: 'admin', labelKey: 'Admin credit', variant: 'info' },
  {
    value: 'redemption',
    labelKey: 'Redemption code',
    variant: 'purple',
  },
]

export const RECHARGE_STATUS_OPTIONS: {
  value: RechargeStatus
  labelKey: string
  variant: StatusVariant
}[] = [
  { value: 'success', labelKey: 'Success', variant: 'success' },
  {
    value: 'pending',
    labelKey: 'Awaiting payment confirmation',
    variant: 'warning',
  },
  { value: 'failed', labelKey: 'Failed', variant: 'danger' },
  { value: 'expired', labelKey: 'Expired', variant: 'neutral' },
  { value: 'used', labelKey: 'Used', variant: 'success' },
]

export const INVOICE_STATUS_OPTIONS: {
  value: InvoiceStatus
  labelKey: string
  variant: StatusVariant
}[] = [
  { value: 'unmarked', labelKey: 'Unmarked', variant: 'neutral' },
  { value: 'marked', labelKey: 'Marked invoiced', variant: 'success' },
  { value: 'revoked', labelKey: 'Mark revoked', variant: 'warning' },
]

export const INVOICE_ACTION_LABEL_KEYS: Record<InvoiceAction, string> = {
  mark: 'Mark invoiced',
  correct: 'Correct marking',
  revoke: 'Revoke marking',
}

export function getSourceConfig(source: RechargeSourceKind) {
  return (
    RECHARGE_SOURCE_OPTIONS.find((option) => option.value === source) ?? {
      value: source,
      labelKey: source,
      variant: 'neutral' as const,
    }
  )
}

export function getStatusConfig(status: RechargeStatus) {
  return (
    RECHARGE_STATUS_OPTIONS.find((option) => option.value === status) ?? {
      value: status,
      labelKey: status,
      variant: 'neutral' as const,
    }
  )
}

export function getInvoiceStatusConfig(status: InvoiceStatus) {
  return (
    INVOICE_STATUS_OPTIONS.find((option) => option.value === status) ?? {
      value: status,
      labelKey: status,
      variant: 'neutral' as const,
    }
  )
}
