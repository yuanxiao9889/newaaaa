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
import {
  focusManager,
  QueryClientProvider,
  QueryClient,
} from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { LogCategory, TaskLog } from '../../types'
import { UsageLogsProvider } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

vi.mock('@lobehub/icons', () => ({}))

const clients: QueryClient[] = []
const submittedTask: TaskLog = {
  id: 1,
  user_id: 61,
  platform: 'internal_image',
  task_id: 'task-hy-just-submitted',
  model_name: 'hy-image-v3.5-preview',
  action: 'IMAGE_GENERATE',
  channel_id: 0,
  group: 'default',
  quota: 50000,
  submit_time: 1791557800,
  finish_time: 0,
  status: 'SUBMITTED',
}

function VisibilityFixture(props: { category: LogCategory }) {
  const [visible, setVisible] = useState(true)
  return (
    <>
      <button type='button' onClick={() => setVisible(!visible)}>
        {visible ? 'Leave logs' : 'Return to logs'}
      </button>
      {visible && <UsageLogsTable logCategory={props.category} />}
    </>
  )
}

async function renderLogs(category: LogCategory, role: number = ROLE.ADMIN) {
  useAuthStore.getState().auth.setUser({ id: 61, username: 'tester', role })
  focusManager.setFocused(true)
  const server = { tasks: [] as TaskLog[], listRequests: 0 }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = {}
    if (url.startsWith('/api/task?') || url.startsWith('/api/task/self?')) {
      server.listRequests++
      data = { items: server.tasks, total: server.tasks.length }
    } else if (
      url.startsWith('/api/log?') ||
      url.startsWith('/api/log/self?')
    ) {
      server.listRequests++
      data = { items: [], total: 0 }
    } else if (
      url === '/api/subscription/admin/plans' ||
      url === '/api/group/'
    ) {
      data = []
    }
    return { data: { success: true, data } }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => <VisibilityFixture category={category} />,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: [`/usage-logs/${category}`],
    }),
  })
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 10000, refetchOnWindowFocus: false },
    },
  })
  client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60000 })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <UsageLogsProvider>
        <RouterProvider router={router} />
      </UsageLogsProvider>
    </QueryClientProvider>
  )
  await screen.findByText('No Logs Found')
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return { server, client }
}

afterEach(() => {
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  focusManager.setFocused(undefined)
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  localStorage.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

test('shows a newly submitted Hy task when re-entering a recently cached list', async () => {
  const { server } = await renderLogs('task')
  fireEvent.click(screen.getByRole('button', { name: 'Leave logs' }))
  server.tasks = [submittedTask]
  fireEvent.click(screen.getByRole('button', { name: 'Return to logs' }))
  expect(await screen.findByText('hy-image-v3.5-preview')).toBeVisible()
})

test.each([ROLE.ADMIN, ROLE.USER])(
  'shows submitted tasks when returning from another app (role=%s)',
  async (role) => {
    const { server } = await renderLogs('task', role)
    act(() => focusManager.setFocused(false))
    server.tasks = [submittedTask]
    await act(async () => focusManager.setFocused(true))
    expect(await screen.findByText('hy-image-v3.5-preview')).toBeVisible()
  }
)

test('does not poll the task list while it remains open', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  const { server } = await renderLogs('task')
  const requests = server.listRequests
  server.tasks = [submittedTask]
  await act(async () => vi.advanceTimersByTimeAsync(60000))
  expect(server.listRequests).toBe(requests)
  expect(screen.queryByText('hy-image-v3.5-preview')).not.toBeInTheDocument()
})

test('preserves usage logs cache and focus behavior', async () => {
  const { server } = await renderLogs('common')
  const requests = server.listRequests
  act(() => focusManager.setFocused(false))
  await act(async () => focusManager.setFocused(true))
  fireEvent.click(screen.getByRole('button', { name: 'Leave logs' }))
  fireEvent.click(screen.getByRole('button', { name: 'Return to logs' }))
  await screen.findByText('No Logs Found')
  expect(server.listRequests).toBe(requests)
})
