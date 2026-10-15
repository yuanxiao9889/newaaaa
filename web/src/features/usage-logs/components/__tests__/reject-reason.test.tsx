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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

const queryClients: QueryClient[] = []

function makeLog(other: LogOtherData): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 5,
    content: 'request rejected',
    username: 'user',
    token_name: 'token',
    model_name: 'gpt-test',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 1,
    channel_name: 'channel',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDetails(
  isAdmin: boolean,
  prompt?: NonNullable<LogOtherData['admin_info']>['moderation_prompt']
): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClients.push(queryClient)

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog({
          admin_info: {
            reject_reason: 'blocked by channel policy',
            moderation_prompt: prompt,
          },
        })}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) {
    queryClient.clear()
  }
  queryClients.length = 0
})

describe('usage log reject reason', () => {
  test('shows the nested admin reject reason to admins', () => {
    renderDetails(true)

    expect(screen.getByText('Reject Reason')).toBeInTheDocument()
    expect(screen.getByText('blocked by channel policy')).toBeInTheDocument()
  })

  test('hides the nested admin reject reason from non-admin users', () => {
    renderDetails(false)

    expect(screen.queryByText('Reject Reason')).toBeNull()
    expect(screen.queryByText('blocked by channel policy')).toBeNull()
  })

  test('admins can read and copy retained text without executing markup', async () => {
    const user = userEvent.setup()
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
    const text = '<script>alert(1)</script>\nA flower'
    renderDetails(true, {
      text,
      status: 'captured',
      truncated: true,
      redacted: true,
    })
    expect(
      screen.getByText('Safety-rejected prompt (admin only)')
    ).toBeInTheDocument()
    expect(screen.getByText(/<script>alert\(1\)<\/script>/)).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
    expect(
      screen.getByText('Prompt truncated at the retention limit')
    ).toBeInTheDocument()
    expect(
      screen.getByText('Detected credentials were redacted')
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: 'Copy retained prompt' })
    )
    expect(copy).toHaveBeenCalledWith(text)
    copy.mockRestore()
  })

  test('ordinary users cannot see or copy prompts even if supplied in props', () => {
    renderDetails(false, {
      text: 'private retained prompt',
      status: 'captured',
      truncated: true,
    })
    expect(screen.queryByText('private retained prompt')).toBeNull()
    expect(screen.queryByText('Safety-rejected prompt (admin only)')).toBeNull()
    expect(
      screen.queryByRole('button', { name: 'Copy retained prompt' })
    ).toBeNull()
    expect(
      screen.queryByText('Prompt truncated at the retention limit')
    ).toBeNull()
  })

  test('admins see why a rejected request has no retained text', () => {
    renderDetails(true, { status: 'body_too_large' })
    expect(
      screen.getByText('Prompt text could not be retained')
    ).toBeInTheDocument()
    expect(screen.getByText('body_too_large')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Copy retained prompt' })
    ).toBeNull()
  })
})
