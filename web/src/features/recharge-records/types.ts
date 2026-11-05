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
export type RechargeSource = 'topup' | 'redemption' | 'historical'
export type RechargeSourceKind = 'online' | 'admin' | 'redemption'
export type RechargeStatus =
  | 'success'
  | 'pending'
  | 'failed'
  | 'expired'
  | 'used'
export type InvoiceStatus = 'unmarked' | 'marked' | 'revoked'
export type InvoiceAction = 'mark' | 'correct' | 'revoke'

export interface RechargeRecord {
  source: RechargeSource
  source_id: number
  user_id: number
  username: string
  source_kind: RechargeSourceKind
  record_no: string
  recharge_time: number
  credited_quota: number
  paid_amount: number | null
  currency: string
  paid_amount_confirmed: boolean
  amount_source: 'payment' | 'quota_conversion' | 'none'
  status: RechargeStatus
  invoice_status: InvoiceStatus
  invoice_no?: string
  invoice_date?: string
  marked_at?: number
  invoice_version: number
  can_mark: boolean
  requires_receipt_verification: boolean
  internal_note?: string
  verification_note?: string
  marked_by_name?: string
}

export interface RechargeRecordPage {
  page: number
  page_size: number
  total: number
  items: RechargeRecord[]
}

export interface RechargeRecordConfig {
  feature_enabled: boolean
  writes_enabled: boolean
  imports_enabled?: boolean
  default_days: number
  max_days: number
  max_page_size: number
}

export interface InvoiceMarkingRecordRef {
  source: RechargeSource
  id: number
  expected_version: number
}

export interface InvoiceMarkingPayload {
  action: InvoiceAction
  records: InvoiceMarkingRecordRef[]
  invoice_no?: string
  invoice_date?: string
  internal_note?: string
  verification_note?: string
  reason?: string
  receipt_confirmed?: boolean
}

export interface InvoiceMarkingResult {
  action: InvoiceAction
  count: number
  replayed: boolean
  records: {
    source: RechargeSource
    id: number
    invoice_status: InvoiceStatus
    invoice_version: number
    invoice_no?: string
    invoice_date?: string
  }[]
}

export interface InvoiceSnapshot {
  status: InvoiceStatus
  invoice_no: string
  invoice_date: string
  internal_note: string
  verification_note: string
  receipt_confirmed: boolean
  version: number
  marked_at: number
  marked_by: number
  marked_by_name: string
}

export interface InvoiceHistoryItem {
  id: number
  source: RechargeSource
  source_id: number
  action: InvoiceAction
  request_id: string
  operator_id: number
  operator_name: string
  reason: string
  created_at: number
  before: InvoiceSnapshot
  after: InvoiceSnapshot
}

export interface RechargeRecordFilters {
  keyword: string
  userId: string
  source: string
  status: string
  invoiceStatus: string
  startDate?: Date
  endDate?: Date
}

export interface RechargeRecordQuery {
  page: number
  pageSize: number
  keyword?: string
  userId?: number
  source?: string
  status?: string
  invoiceStatus?: string
  startTime?: number
  endTime?: number
}
