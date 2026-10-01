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
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test } from 'vitest'

import { usageLogSchema, type UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { useCommonLogsColumns } from '../columns/common-logs-columns'
import { DetailsDialog } from '../dialogs/details-dialog'
import { UsageLogsProvider } from '../usage-logs-provider'

const clients: QueryClient[] = []
const retryDetails = [
  {
    attempt: 1,
    channel_id: 143,
    channel_name: 'first',
    status_code: 429,
    error_code: 'rate_limit',
    error_type: 'upstream_error',
    error: 'Upstream rate limited',
  },
  {
    attempt: 2,
    channel_id: 66,
    channel_name: 'last',
    status_code: 502,
    error: 'Upstream unavailable',
  },
  {
    attempt: 3,
    channel_id: 66,
    channel_name: 'last',
    status_code: 503,
    error: 'No available channel',
  },
]

function renderDetails(other: LogOtherData, isAdmin = true, logType = 5): void {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60_000 })
  clients.push(client)
  const log: UsageLog = {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: logType,
    content: 'Final failure',
    username: 'user',
    token_name: '',
    model_name: 'gpt-image-2',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 20,
    is_stream: false,
    channel: 66,
    channel_name: 'last',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'task-retry',
    upstream_request_id: '',
  }
  render(
    <QueryClientProvider client={client}>
      <DetailsDialog
        log={log}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const client of clients) client.clear()
  clients.length = 0
})

function RetryChannelCell(props: { other: LogOtherData }) {
  const columns = useCommonLogsColumns(true, false)
  const table = useReactTable({
    columns,
    data: [
      usageLogSchema.parse({
        id: 1,
        user_id: 1,
        created_at: 1,
        type: 5,
        content: 'Final failure',
        model_name: 'gpt-image-2',
        channel: 66,
        other: JSON.stringify(props.other),
      }),
    ],
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0].getAllCells()
    .find((item) => item.column.id === 'channel')
  if (!cell) throw new Error('Channel column is missing')
  return flexRender(cell.column.columnDef.cell, cell.getContext())
}

test.each(['admin', 'legacy'] as const)(
  'opens the %s retry chain in the channel column',
  async (format) => {
    const user = userEvent.setup()
    const metadata = { async_channel_retry_path: ['143', '66', '66'] }
    render(
      <UsageLogsProvider>
        <RetryChannelCell
          other={format === 'admin' ? { admin_info: metadata } : metadata}
        />
      </UsageLogsProvider>
    )
    await user.click(screen.getByRole('button', { name: 'Retry Chain' }))
    expect(screen.getByText('143 → 66 → 66')).toBeVisible()
  }
)

test.each(['admin', 'legacy'] as const)(
  'shows %s retry errors in order, including repeated channels',
  (format) => {
    const metadata = {
      async_channel_retry_path: ['143', '66', '66'],
      async_channel_retry_details: retryDetails,
    }
    renderDetails(format === 'admin' ? { admin_info: metadata } : metadata)
    expect(screen.getByText('143 → 66 → 66')).toBeVisible()
    const list = screen.getByRole('list', { name: 'Retry Failure Details' })
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent(
      '#1, channel=143 (first), status_code=429, error_code=rate_limit, error_type=upstream_error'
    )
    expect(items[0]).toHaveTextContent('Upstream rate limited')
    expect(items[1]).toHaveTextContent('#2, channel=66 (last), status_code=502')
    expect(items[1]).toHaveTextContent('Upstream unavailable')
    expect(items[2]).toHaveTextContent('#3, channel=66 (last), status_code=503')
    expect(items[2]).toHaveTextContent('No available channel')
  }
)

test('prefers current admin retry metadata over legacy fields', () => {
  renderDetails({
    admin_info: {
      async_channel_retry_path: ['143'],
      async_channel_retry_details: [retryDetails[0]],
    },
    async_channel_retry_path: ['99'],
    async_channel_retry_details: [{ channel_id: 99, error: 'Stale error' }],
  })
  expect(screen.getByText('143')).toBeVisible()
  expect(screen.getByText(/Upstream rate limited/)).toBeVisible()
  expect(screen.queryByText(/Stale error/)).not.toBeInTheDocument()
})

test.each(['admin', 'legacy'] as const)(
  'hides %s retry metadata from ordinary users',
  (format) => {
    const metadata = {
      async_channel_retry_path: ['143', '66'],
      async_channel_retry_details: retryDetails,
    }
    renderDetails(
      format === 'admin' ? { admin_info: metadata } : metadata,
      false
    )
    expect(screen.queryByText('Retry Chain')).not.toBeInTheDocument()
    expect(screen.queryByText('Retry Failure Details')).not.toBeInTheDocument()
    expect(screen.queryByText(/Upstream rate limited/)).not.toBeInTheDocument()
  }
)

test('keeps the existing channel chain without inventing missing error details', () => {
  renderDetails({ admin_info: { use_channel: ['143', '66', '66'] } })
  expect(screen.getByText('143 → 66 → 66')).toBeVisible()
  expect(screen.queryByText('Retry Failure Details')).not.toBeInTheDocument()
})

test('copies all retry errors in their original order', async () => {
  const user = userEvent.setup()
  renderDetails({ admin_info: { async_channel_retry_details: retryDetails } })
  await user.click(
    screen.getByRole('button', { name: 'Copy Retry Failure Details' })
  )
  expect(await navigator.clipboard.readText()).toBe(
    '#1, channel=143 (first), status_code=429, error_code=rate_limit, error_type=upstream_error\nUpstream rate limited\n\n' +
      '#2, channel=66 (last), status_code=502\nUpstream unavailable\n\n' +
      '#3, channel=66 (last), status_code=503\nNo available channel'
  )
})

const deliveryEvidence = {
  task_id: 'delivery-test',
  worker_attempt: 1,
  attempt: 1,
  channel_id: 133,
  stage: 'transport_failed',
  started_at: 1000,
  updated_at: 2000,
  elapsed_ms: 1000,
  connection_ready: true,
  connection_reused: false,
  request_written: true,
  write_count: 1,
  response_received: false,
  response_complete: false,
}

test('shows local write evidence without claiming upstream acceptance', () => {
  renderDetails({ admin_info: { upstream_delivery: [deliveryEvidence] } })
  const list = screen.getByRole('list', { name: 'Upstream delivery evidence' })
  expect(list).toHaveTextContent('Request written locallyObserved')
  expect(list).toHaveTextContent('Upstream HTTP responseNot observed')
  expect(
    screen.getByText(
      'A local write does not confirm upstream acceptance. An HTTP response may come from a gateway.'
    )
  ).toBeVisible()
})

test('keeps upstream delivery evidence admin-only', () => {
  renderDetails(
    { admin_info: { upstream_delivery: [deliveryEvidence] } },
    false
  )
  expect(
    screen.queryByRole('list', { name: 'Upstream delivery evidence' })
  ).not.toBeInTheDocument()
})

test('shows the timeout reason for rollback logs without the legacy is_task flag', () => {
  renderDetails(
    {
      task_id: 'timed-out-task',
      reason: 'async image task timed out after 1600 seconds',
      billing_state: 'refunded',
    },
    true,
    8
  )
  expect(
    screen.getAllByText('async image task timed out after 1600 seconds').length
  ).toBeGreaterThan(0)
})
