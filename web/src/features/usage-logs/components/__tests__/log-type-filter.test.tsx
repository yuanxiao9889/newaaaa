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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { LogStatistics } from '../../types'
import { CommonLogsFilterBar } from '../common-logs-filter-bar'
import { UsageLogsProvider } from '../usage-logs-provider'

function FilterFixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilter(
  initialEntry = '/usage-logs/common',
  stats: LogStatistics = { quota: 0, rpm: 0, tpm: 0 }
) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/group/') return { data: { success: true, data: [] } }
    if (url === '/api/status') {
      const currency = useSystemConfigStore.getState().config.currency
      return {
        data: {
          success: true,
          data: {
            quota_display_type: currency.quotaDisplayType,
            quota_per_unit: currency.quotaPerUnit,
            usd_exchange_rate: currency.usdExchangeRate,
            custom_currency_symbol: currency.customCurrencySymbol,
            custom_currency_exchange_rate: currency.customCurrencyExchangeRate,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: url === '/api/user/self/groups' ? {} : stats,
      },
    }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: FilterFixture,
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('combobox', { name: 'Type' })
  return router
}

afterEach(async () => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.setUser(null)
  useSystemConfigStore
    .getState()
    .setConfig({ currency: DEFAULT_CURRENCY_CONFIG })
  await i18next.changeLanguage('en')
})

it.each([1, 10])(
  'shows cross-page top-up totals for role %s and sends the selected time range',
  async (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'viewer', role })
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })
    const router = await renderFilter(
      '/usage-logs/common?type=%5B%221%22%5D&startTime=100000&endTime=200000&page=3',
      {
        quota: 500000,
        rpm: 3,
        tpm: 100,
        topup: {
          quota: '1000000',
          amounts: { '¥': '21.125', $: '2.5' },
          uncounted: 0,
        },
      }
    )
    expect(await screen.findByText('¥35.125 + $2.5')).toBeVisible()
    const usage = screen.getByText('Usage')
    const topup = screen.getByText('Top-up total')
    expect(
      usage.compareDocumentPosition(topup) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    expect(
      topup.compareDocumentPosition(screen.getByText('RPM')) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    const endpoint = role === 10 ? '/api/log/stat' : '/api/log/self/stat'
    const request = vi
      .mocked(api.get)
      .mock.calls.find(([url]) => url.startsWith(endpoint))
    expect(request).toBeDefined()
    const params = new URL(request?.[0] ?? '', 'http://localhost').searchParams
    expect(params.get('start_timestamp')).toBe('100')
    expect(params.get('end_timestamp')).toBe('200')
    expect(params.get('type')).toBe('1')

    await act(async () => {
      await router.navigate({
        to: '/usage-logs/$section',
        params: { section: 'common' },
        search: { type: ['1'], startTime: 100000, endTime: 200000, page: 4 },
      })
    })
    expect(screen.getByText('¥35.125 + $2.5')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Hide' }))
    expect(screen.queryByText('¥35.125 + $2.5')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show' }))
    expect(screen.getByText('¥35.125 + $2.5')).toBeVisible()

    vi.mocked(api.get).mockImplementation(async () => ({
      data: {
        success: true,
        data: {
          quota: 0,
          rpm: 0,
          tpm: 0,
          topup: { quota: '0', amounts: {}, uncounted: 0 },
        },
      },
    }))
    await act(async () => {
      await router.navigate({
        to: '/usage-logs/$section',
        params: { section: 'common' },
        search: { type: ['1'], startTime: 300000, endTime: 400000 },
      })
    })
    await waitFor(() =>
      expect(screen.getByText('Top-up total').parentElement).toHaveTextContent(
        '¥0'
      )
    )
    expect(
      vi
        .mocked(api.get)
        .mock.calls.some(
          ([url]) =>
            url.startsWith(endpoint) &&
            url.includes('start_timestamp=300') &&
            url.includes('end_timestamp=400')
        )
    ).toBe(true)
  }
)

it('shows a partial-total notice for unrecognized historical records only after applying the top-up filter', async () => {
  await renderFilter('/usage-logs/common', {
    quota: 0,
    rpm: 0,
    tpm: 0,
    topup: { quota: '500000', amounts: {}, uncounted: 2 },
  })
  expect(screen.queryByText('Top-up total')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('combobox', { name: 'Type' }))
  await userEvent.click(screen.getByRole('option', { name: 'Top-up' }))
  expect(screen.queryByText('Top-up total')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(
    await screen.findByText('2 records could not be included')
  ).toBeVisible()
  expect(screen.getByText('Top-up total').parentElement).toHaveTextContent('$1')
})

it('shows unavailable instead of zero when the selected top-up statistics request fails', async () => {
  const router = await renderFilter('/usage-logs/common?type=%5B%221%22%5D', {
    quota: 0,
    rpm: 0,
    tpm: 0,
    topup: { quota: '500000', amounts: {}, uncounted: 0 },
  })
  expect(await screen.findByText('$1')).toBeVisible()
  vi.mocked(api.get).mockRejectedValue(new Error('Statistics unavailable'))
  await act(async () => {
    await router.navigate({
      to: '/usage-logs/$section',
      params: { section: 'common' },
      search: { type: ['1'], startTime: 300000, endTime: 400000 },
    })
  })
  await waitFor(() =>
    expect(screen.getByText('Top-up total').parentElement).toHaveTextContent(
      '—'
    )
  )
  expect(screen.queryByText('$1')).not.toBeInTheDocument()
})

it.each([
  ['en', '$1,234.56'],
  ['zhCN', '$1,234.56'],
  ['zhTW', '$1,234.56'],
  ['fr', '$1\u202f234,56'],
  ['ja', '$1,234.56'],
  ['ru', '$1\u00a0234,56'],
  ['vi', '$1.234,56'],
  ['invalid_locale', '$1,234.56'],
])(
  'formats top-up totals for %s and responds to language changes',
  async (language, expected) => {
    await i18next.changeLanguage(language)
    await renderFilter('/usage-logs/common?type=%5B%221%22%5D', {
      quota: 0,
      rpm: 0,
      tpm: 0,
      topup: { quota: '0', amounts: { $: '1234.56' }, uncounted: 0 },
    })
    await waitFor(() =>
      expect(screen.getByText('Top-up total').parentElement?.textContent).toBe(
        `Top-up total${expected}`
      )
    )
    await act(async () => {
      await i18next.changeLanguage('vi')
    })
    expect(screen.getByText('Top-up total').parentElement?.textContent).toBe(
      'Top-up total$1.234,56'
    )
  }
)

it('marks only retired log types as deprecated while keeping historical filters selectable', async () => {
  const router = await renderFilter()
  await userEvent.click(screen.getByRole('combobox', { name: 'Type' }))
  for (const label of ['Manage', 'Login']) {
    expect(
      within(
        screen.getByRole('option', { name: new RegExp(`^${label}`) })
      ).getByText('Deprecated')
    ).toBeVisible()
  }
  for (const label of [
    'All Types',
    'Top-up',
    'Consume',
    'System',
    'Error',
    'Refund',
  ]) {
    expect(
      within(screen.getByRole('option', { name: label })).queryByText(
        'Deprecated'
      )
    ).not.toBeInTheDocument()
  }
  await userEvent.click(screen.getByRole('option', { name: /^Manage/ }))
  expect(screen.getByRole('combobox', { name: 'Type' })).toHaveTextContent(
    'Deprecated'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ type: ['3'], page: 1 })
  )
  await userEvent.click(screen.getByRole('combobox', { name: 'Type' }))
  await userEvent.click(screen.getByRole('option', { name: /^Login/ }))
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ type: ['7'], page: 1 })
  )
})
