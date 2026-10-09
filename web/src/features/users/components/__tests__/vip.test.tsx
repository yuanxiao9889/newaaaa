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
import type { Row } from '@tanstack/react-table'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { DataTableRowActions } from '../data-table-row-actions'
import { UserVIPDialog } from '../user-vip-dialog'
import { UsersProvider } from '../users-provider'

const member: User = {
  id: 2,
  username: 'inviter',
  display_name: 'Inviter',
  role: 1,
  status: 1,
  quota: 1000,
  used_quota: 0,
  request_count: 0,
  group: 'default',
  is_vip: false,
}

function renderVIPDialog(isVIP = false) {
  const onSuccess = vi.fn()
  function DialogHarness() {
    const [open, setOpen] = useState(true)
    return (
      <UserVIPDialog
        user={{ ...member, is_vip: isVIP }}
        open={open}
        onOpenChange={setOpen}
        onSuccess={onSuccess}
      />
    )
  }
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <DialogHarness />
    </QueryClientProvider>
  )
  return { onSuccess, event: userEvent.setup() }
}

afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it.each([false, true])(
  'confirms the target and sends explicit VIP state when current VIP is %s',
  async (isVIP) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const { onSuccess, event } = renderVIPDialog(isVIP)
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent('inviter (ID: 2)')
    expect(dialog).toHaveTextContent(
      isVIP ? 'The 3-reward limit' : 'earn 10% rewards without a count limit'
    )
    expect(dialog).toHaveTextContent(
      isVIP
        ? 'Existing rewards are kept.'
        : 'Past skipped rewards will not be paid retroactively.'
    )
    expect(put).not.toHaveBeenCalled()
    await event.click(
      within(dialog).getByRole('button', {
        name: isVIP ? 'Cancel VIP' : 'Enable VIP',
      })
    )
    await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce())
    expect(put).toHaveBeenCalledWith('/api/user/2/vip', { is_vip: !isVIP })
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
  }
)

it('keeps the dialog open on server failure and permits retry', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Membership update failed' },
    })
    .mockResolvedValueOnce({ data: { success: true } })
  const { onSuccess, event } = renderVIPDialog()
  await event.click(screen.getByRole('button', { name: 'Enable VIP' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  expect(onSuccess).not.toHaveBeenCalled()
  expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Enable VIP' })).toBeEnabled()
  )
  await event.click(screen.getByRole('button', { name: 'Enable VIP' }))
  await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce())
})

it('disables confirmation and cancellation while the update is pending', async () => {
  let resolvePending!: (value: { data: { success: boolean } }) => void
  const pending = new Promise<{ data: { success: boolean } }>((resolve) => {
    resolvePending = resolve
  })
  const put = vi.spyOn(api, 'put').mockReturnValue(pending)
  const { onSuccess, event } = renderVIPDialog()
  await event.click(screen.getByRole('button', { name: 'Enable VIP' }))
  expect(screen.getByRole('button', { name: 'Enable VIP' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  await event.keyboard('{Escape}')
  expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  expect(put).toHaveBeenCalledOnce()
  resolvePending({ data: { success: true } })
  await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce())
})

it('cancels without changing membership', async () => {
  const put = vi.spyOn(api, 'put')
  const { onSuccess, event } = renderVIPDialog()
  await event.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(put).not.toHaveBeenCalled()
  expect(onSuccess).not.toHaveBeenCalled()
})

it.each([
  [1, 1, false],
  [10, 1, true],
  [10, 10, false],
  [10, 100, false],
  [100, 10, true],
])(
  'shows the VIP menu only when viewer role %s may manage target role %s',
  async (viewerRole, targetRole, allowed) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'operator', role: viewerRole })
    const client = new QueryClient()
    render(
      <QueryClientProvider client={client}>
        <UsersProvider>
          <DataTableRowActions
            row={{ original: { ...member, role: targetRole } } as Row<User>}
          />
        </UsersProvider>
      </QueryClientProvider>
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Open menu' }))
    if (allowed) {
      expect(
        screen.getByRole('menuitem', { name: 'Enable VIP' })
      ).toBeInTheDocument()
      await userEvent
        .setup()
        .click(screen.getByRole('menuitem', { name: 'Enable VIP' }))
      expect(
        screen.getByRole('alertdialog', { name: 'Enable VIP' })
      ).toHaveTextContent('inviter (ID: 2)')
    } else {
      expect(
        screen.queryByRole('menuitem', { name: 'Enable VIP' })
      ).not.toBeInTheDocument()
    }
  }
)
