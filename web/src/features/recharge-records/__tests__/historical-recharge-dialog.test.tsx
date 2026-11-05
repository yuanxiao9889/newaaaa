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
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { HistoricalRechargeDialog } from '../components/historical-recharge-dialog'

afterEach(() => vi.restoreAllMocks())

it('requires source review and explicit confirmation before importing one record', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/recharge-records/config'
          ? { feature_enabled: true, imports_enabled: true }
          : {
              jobs: [],
              items: [
                {
                  id: 99,
                  source_key: 'HIST-REVIEW-99',
                  user_id: 5,
                  quota: 5000,
                  occurred_at: 1760000000,
                  evidence: 'Original admin credit evidence',
                  status: 'pending',
                },
              ],
              next_before_id: 0,
            },
    },
  }))
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: null } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <HistoricalRechargeDialog open onOpenChange={() => {}} />
    </QueryClientProvider>
  )
  const event = userEvent.setup()
  await event.click(await screen.findByRole('button', { name: 'Review' }))
  const review = screen.getByRole('dialog', {
    name: 'Review historical recharge',
  })
  expect(
    within(review).getByText('Original admin credit evidence')
  ).toBeInTheDocument()
  expect(within(review).getByRole('button', { name: 'Confirm' })).toBeDisabled()
  await event.type(
    within(review).getByLabelText('Verification note'),
    'Verified source and owner'
  )
  await event.click(within(review).getByRole('button', { name: 'Confirm' }))
  expect(post).not.toHaveBeenCalled()
  const confirmation = screen.getByRole('alertdialog')
  expect(within(confirmation).getByText(/HIST-REVIEW-99/)).toBeInTheDocument()
  await event.click(
    within(confirmation).getByRole('button', { name: 'Continue' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/recharge-records/historical-review/99',
      {
        user_id: 5,
        quota: 5000,
        note: 'Verified source and owner',
        reject: false,
        confirmed: true,
      }
    )
  )
})
