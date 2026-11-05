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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import {
  getRechargeRecords,
  getRechargeRecordConfig,
  getSelfRechargeRecords,
} from '../api'
import { RechargeRecordsTable } from '../components/recharge-records-table'
import type { RechargeRecord } from '../types'

vi.mock('../api', () => ({
  getRechargeRecordConfig: vi.fn(),
  getRechargeRecords: vi.fn(),
  getSelfRechargeRecords: vi.fn(),
}))

const config = {
  success: true,
  data: {
    feature_enabled: true,
    writes_enabled: true,
    default_days: 30,
    max_days: 366,
    max_page_size: 100,
  },
}

function record(overrides: Partial<RechargeRecord>): RechargeRecord {
  return {
    source: 'topup',
    source_id: 1,
    user_id: 10,
    username: 'alice',
    source_kind: 'online',
    record_no: 'ONLINE-1',
    recharge_time: 1_760_000_000,
    credited_quota: 5000,
    paid_amount: 10,
    currency: 'USD',
    paid_amount_confirmed: true,
    amount_source: 'payment',
    status: 'success',
    invoice_status: 'unmarked',
    invoice_version: 0,
    can_mark: true,
    requires_receipt_verification: false,
    ...overrides,
  }
}

function renderTable(mode: 'admin' | 'self') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RechargeRecordsTable mode={mode} />
    </QueryClientProvider>
  )
}

afterEach(() => {
  vi.clearAllMocks()
  useAuthStore.getState().auth.reset()
})

beforeEach(() => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: 10,
    permissions: {
      admin_permissions: { recharge_record: { read: true, write: true } },
    },
  })
})

it('hides invoice mutation controls for a read-only administrator', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'reader',
    role: 10,
    permissions: {
      admin_permissions: { recharge_record: { read: true, write: false } },
    },
  })
  vi.mocked(getRechargeRecordConfig).mockResolvedValue(config)
  vi.mocked(getRechargeRecords).mockResolvedValue({
    success: true,
    data: { page: 1, page_size: 20, total: 1, items: [record({})] },
  })
  renderTable('admin')
  await screen.findByText('ONLINE-1')
  expect(
    screen.queryByRole('button', { name: 'Mark selected as invoiced' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
  expect(screen.queryByText('Mark invoiced')).not.toBeInTheDocument()
})

it('disables batch marking when selected records belong to different users', async () => {
  vi.mocked(getRechargeRecordConfig).mockResolvedValue(config)
  vi.mocked(getRechargeRecords).mockResolvedValue({
    success: true,
    data: {
      page: 1,
      page_size: 20,
      total: 2,
      items: [
        record({ source_id: 1, user_id: 10, username: 'alice' }),
        record({
          source_id: 2,
          user_id: 11,
          username: 'bob',
          record_no: 'ONLINE-2',
        }),
      ],
    },
  })
  const user = userEvent.setup()
  renderTable('admin')
  await screen.findByText('ONLINE-1')
  await user.click(screen.getAllByRole('checkbox')[0])
  await waitFor(() => {
    expect(
      screen.getByRole('button', { name: 'Mark selected as invoiced' })
    ).toBeDisabled()
  })
  await user.click(screen.getAllByRole('checkbox')[0])
  await waitFor(() => {
    expect(
      screen.queryByRole('button', { name: 'Mark selected as invoiced' })
    ).not.toBeInTheDocument()
  })
  await user.click(screen.getAllByRole('checkbox')[1])
  await waitFor(() => {
    expect(
      screen.getByRole('button', { name: 'Mark selected as invoiced' })
    ).toBeEnabled()
  })
})

it('shows only the signed-in user records without admin-only fields or actions', async () => {
  vi.mocked(getRechargeRecordConfig).mockResolvedValue(config)
  vi.mocked(getSelfRechargeRecords).mockResolvedValue({
    success: true,
    data: {
      page: 1,
      page_size: 20,
      total: 1,
      items: [
        record({
          user_id: 10,
          username: 'alice',
          invoice_status: 'marked',
          invoice_no: 'INV-9',
          invoice_date: '2026-10-01',
          invoice_version: 1,
          internal_note: 'internal-only-note',
          verification_note: 'evidence-only-note',
          marked_by_name: 'root-admin',
        }),
      ],
    },
  })
  renderTable('self')
  await screen.findByText('ONLINE-1')
  expect(screen.getByText(/INV-9/)).toBeInTheDocument()
  expect(screen.queryByText('internal-only-note')).not.toBeInTheDocument()
  expect(screen.queryByText('evidence-only-note')).not.toBeInTheDocument()
  expect(screen.queryByText('root-admin')).not.toBeInTheDocument()
  expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Open menu' })
  ).not.toBeInTheDocument()
})
