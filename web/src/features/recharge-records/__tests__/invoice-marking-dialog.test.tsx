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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { InvoiceMarkingDialog } from '../components/invoice-marking-dialog'
import type { RechargeRecord } from '../types'

const adminRecord: RechargeRecord = {
  source: 'topup',
  source_id: 7,
  user_id: 2,
  username: 'invoice-user',
  source_kind: 'admin',
  record_no: 'ADMUSR7NO1',
  recharge_time: 1_760_000_000,
  credited_quota: 5000,
  paid_amount: null,
  currency: '',
  paid_amount_confirmed: false,
  amount_source: 'quota_conversion',
  status: 'success',
  invoice_status: 'unmarked',
  invoice_version: 0,
  can_mark: true,
  requires_receipt_verification: true,
}

function renderMarkDialog() {
  const onCompleted = vi.fn()
  const onOpenChange = vi.fn()
  function Harness() {
    const [open, setOpen] = useState(true)
    return (
      <InvoiceMarkingDialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          onOpenChange(next)
        }}
        action='mark'
        records={[adminRecord]}
        onCompleted={onCompleted}
      />
    )
  }
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>
  )
  return { onCompleted, onOpenChange, event: userEvent.setup() }
}

afterEach(() => {
  vi.restoreAllMocks()
})

it('requires receipt verification before marking an admin credit and submits explicit IDs', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: { count: 1 } } })
  const { onCompleted, onOpenChange, event } = renderMarkDialog()

  const submit = screen.getByRole('button', { name: 'Review and submit' })
  expect(submit).toBeDisabled()

  await event.click(screen.getByRole('checkbox'))
  await event.type(screen.getByLabelText('Verification note'), 'bank slip 42')
  await waitFor(() => expect(submit).toBeEnabled())
  await event.click(submit)

  const confirm = screen.getByRole('alertdialog')
  expect(confirm).toHaveTextContent('ADMUSR7NO1')
  await event.click(
    within(confirm).getByRole('button', { name: 'Mark invoiced' })
  )

  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  const [url, payload, config] = post.mock.calls[0]
  expect(url).toBe('/api/recharge-records/invoice-markings')
  expect(payload).toMatchObject({
    action: 'mark',
    records: [{ source: 'topup', id: 7, expected_version: 0 }],
    receipt_confirmed: true,
    verification_note: 'bank slip 42',
  })
  expect(config?.headers).toMatchObject({
    'Idempotency-Key': expect.any(String),
  })
  await waitFor(() => expect(onCompleted).toHaveBeenCalledOnce())
  expect(onOpenChange).toHaveBeenCalledWith(false)
})
