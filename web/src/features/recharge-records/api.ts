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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  InvoiceHistoryItem,
  InvoiceMarkingPayload,
  InvoiceMarkingResult,
  RechargeRecordConfig,
  RechargeRecordPage,
  RechargeRecordQuery,
} from './types'

export interface ServerResponse<T> {
  success: boolean
  message?: string
  code?: string
  data?: T
}

export interface InvoiceMarkingConflict {
  source: string
  id: number
  reason: string
  current_version?: number
}

function buildParams(query: RechargeRecordQuery): Record<string, string> {
  const params: Record<string, string> = {
    p: String(query.page),
    page_size: String(query.pageSize),
  }
  if (query.keyword?.trim()) params.keyword = query.keyword.trim()
  if (query.userId && query.userId > 0) params.user_id = String(query.userId)
  if (query.source) params.source = query.source
  if (query.status) params.status = query.status
  if (query.invoiceStatus) params.invoice_status = query.invoiceStatus
  if (query.startTime && query.startTime > 0) {
    params.start_timestamp = String(query.startTime)
  }
  if (query.endTime && query.endTime > 0) {
    params.end_timestamp = String(query.endTime)
  }
  return params
}

export async function getRechargeRecordConfig(): Promise<
  ServerResponse<RechargeRecordConfig>
> {
  const res = await api.get('/api/recharge-records/config')
  return res.data
}

export async function getRechargeRecords(
  query: RechargeRecordQuery
): Promise<ServerResponse<RechargeRecordPage>> {
  const res = await api.get('/api/recharge-records/', {
    params: buildParams(query),
  })
  return res.data
}

export async function getSelfRechargeRecords(
  query: RechargeRecordQuery
): Promise<ServerResponse<RechargeRecordPage>> {
  const res = await api.get('/api/recharge-records/self', {
    params: buildParams(query),
  })
  return res.data
}

export async function markRechargeInvoices(
  payload: InvoiceMarkingPayload,
  idempotencyKey: string
): Promise<ServerResponse<InvoiceMarkingResult>> {
  const res = await api.post(
    '/api/recharge-records/invoice-markings',
    payload,
    {
      headers: { 'Idempotency-Key': idempotencyKey },
      skipErrorHandler: true,
      skipBusinessError: true,
    }
  )
  return res.data
}

export async function getRechargeInvoiceHistory(
  source: string,
  id: number,
  beforeId?: number
): Promise<ServerResponse<InvoiceHistoryItem[]>> {
  const res = await api.get(
    `/api/recharge-records/${encodeURIComponent(source)}/${id}/invoice-history`,
    { params: beforeId ? { before_id: beforeId } : undefined }
  )
  return res.data
}

export function getInvoiceMarkingConflicts(
  error: unknown
): InvoiceMarkingConflict[] {
  if (!error || typeof error !== 'object') return []
  const response = (error as { response?: { data?: unknown } }).response
  if (!response || typeof response.data !== 'object' || !response.data) {
    return []
  }
  const body = response.data as {
    data?: { conflicts?: InvoiceMarkingConflict[] }
  }
  return Array.isArray(body.data?.conflicts) ? body.data.conflicts : []
}

export interface HistoricalRecharge {
  id: number
  source_key: string
  user_id: number
  quota: number
  occurred_at: number
  evidence: string
  status: string
}

export interface RechargeImportJob {
  id: number
  start_time: number
  end_time: number
  cursor: number
  done: boolean
}

export async function getRechargeImportReview(beforeId?: number) {
  const res = await api.get<
    ServerResponse<{
      jobs: RechargeImportJob[]
      items: HistoricalRecharge[]
      next_before_id: number
    }>
  >('/api/recharge-records/historical-review', {
    params: { before_id: beforeId },
  })
  return requireImportSuccess(res.data)
}

// The import operations are reviewed, explicit batches. They never call the
// payment or balance-adjustment APIs.
export async function createRechargeImportJob(start: number, end: number) {
  const res = await api.post<ServerResponse<RechargeImportJob>>(
    '/api/recharge-records/historical-jobs',
    { start_timestamp: start, end_timestamp: end }
  )
  return requireImportSuccess(res.data)
}

export async function advanceRechargeImportJob(id: number) {
  const res = await api.post<ServerResponse<RechargeImportJob>>(
    `/api/recharge-records/historical-jobs/${id}/advance`
  )
  return requireImportSuccess(res.data)
}

export async function reviewHistoricalRecharge(
  id: number,
  payload: {
    user_id: number
    quota: number
    note: string
    reject: boolean
    confirmed: boolean
  }
) {
  const res = await api.post<ServerResponse<null>>(
    `/api/recharge-records/historical-review/${id}`,
    payload
  )
  return requireImportSuccess(res.data)
}

function requireImportSuccess<T>(response: ServerResponse<T>): T {
  return requireServerSuccess(response).data as T
}
