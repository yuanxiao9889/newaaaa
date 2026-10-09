import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { Profit } from '..'
import type {
  ProfitCostPrice,
  ProfitBreakdownItem,
  ProfitSeriesItem,
} from '../types'

let client: QueryClient
let summaryRequests: Record<string, unknown>[]
let prices: ProfitCostPrice[]
let reportFails: boolean
let passwordConfigured: boolean
let breakdown: ProfitBreakdownItem[] | null
let series: ProfitSeriesItem[] | null

const savedPrice: ProfitCostPrice = {
  id: 1,
  channel_id: 7,
  channel_name: 'Primary provider',
  model_name: 'text-model',
  current_version_id: 1,
  price_type: 'simple_token',
  input_price: 0.00000012,
  cache_read_price: 0.1,
  effective_from: 1704067200,
  created_at: 1704067200,
  updated_at: 1704067200,
  disabled: false,
  disabled_at: 0,
  price_configured: true,
}

const metric = {
  revenue: 10,
  priced_revenue: 10,
  unpriced_revenue: 0,
  cost: 2,
  profit: 8,
  profit_margin: 0.8,
  coverage_rate: 1,
  request_count: 4,
  priced_count: 4,
  unpriced_count: 0,
  error_count: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
}

beforeEach(() => {
  summaryRequests = []
  prices = []
  reportFails = false
  passwordConfigured = true
  breakdown = []
  series = []
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    let data: unknown
    if (url === '/api/channel') {
      const secondPage = config?.params?.p === 2
      data = {
        total: 101,
        page_size: 100,
        items: secondPage
          ? [{ id: 101, name: 'Archive provider', models: 'video-model' }]
          : [
              {
                id: 7,
                name: 'Primary provider',
                models: 'text-model,video-model',
              },
            ],
      }
    } else if (url === '/api/profit/summary') {
      summaryRequests.push(config?.params ?? {})
      if (reportFails) {
        return { data: { success: false, message: 'Report unavailable' } }
      }
      data = {
        summary: {
          revenue: 10,
          cost: 2,
          profit: 8,
          profit_margin: 0.8,
          coverage_rate: 1,
          unpriced_count: (breakdown ?? []).reduce(
            (sum, item) => sum + item.unpriced_count,
            0
          ),
        },
        series,
        breakdown,
      }
    } else if (url === '/api/profit/password/status') {
      data = { configured: passwordConfigured }
    } else if (url === '/api/profit/cost-price-prefill') {
      data = {
        channel_id: 7,
        model_name: 'text-model',
        has_pricing: true,
        pricing_mode: 'simple_token',
        input_price: 5,
        output_price: 10,
      }
    } else if (url === '/api/profit/cost-prices') {
      data = prices
    } else {
      data = []
    }
    return { data: { success: true, data } }
  })
})

afterEach(() => {
  client.clear()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

function renderProfit() {
  return render(
    <QueryClientProvider client={client}>
      <Profit />
    </QueryClientProvider>
  )
}

it('requests verification when another administrator sets the profit password first', async () => {
  passwordConfigured = false
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce({
      response: {
        status: 403,
        data: {
          success: false,
          code: 'PROFIT_VERIFICATION_REQUIRED',
          message: 'Enter the existing profit password',
        },
      },
    })
    .mockResolvedValueOnce({ data: { success: true } })
  renderProfit()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Set profit access password' })
  )
  await user.type(
    screen.getByLabelText('Profit access password'),
    ' new password '
  )
  await user.type(
    screen.getByLabelText('Confirm profit access password'),
    ' new password '
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))

  const dialog = await screen.findByRole('dialog', {
    name: 'Enter profit access password',
  })
  expect(post).toHaveBeenNthCalledWith(
    1,
    '/api/profit/password',
    { password: ' new password ' },
    expect.any(Object)
  )
  expect(
    within(dialog).queryByLabelText('Confirm profit access password')
  ).not.toBeInTheDocument()
  expect(within(dialog).getByLabelText('Profit access password')).toHaveValue(
    ''
  )
  await user.type(
    within(dialog).getByLabelText('Profit access password'),
    ' existing password '
  )
  await user.click(within(dialog).getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenNthCalledWith(
    2,
    '/api/profit/verify',
    { password: ' existing password ' },
    expect.any(Object)
  )
})

it('shows password policy and request errors while allowing setup to be retried', async () => {
  passwordConfigured = false
  const errorToast = vi.spyOn(toast, 'error')
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: {
        success: false,
        message: 'Password must contain at least 8 characters',
      },
    })
    .mockRejectedValueOnce({
      response: { status: 429, data: { message: 'Too many attempts' } },
    })
    .mockResolvedValueOnce({ data: { success: true } })
  renderProfit()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', { name: 'Set profit access password' })
  )
  await user.type(screen.getByLabelText('Profit access password'), 'short')
  await user.type(
    screen.getByLabelText('Confirm profit access password'),
    'short'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(errorToast).toHaveBeenLastCalledWith(
    'Password must contain at least 8 characters'
  )
  await user.clear(screen.getByLabelText('Profit access password'))
  await user.clear(screen.getByLabelText('Confirm profit access password'))
  await user.type(
    screen.getByLabelText('Profit access password'),
    ' valid password '
  )
  await user.type(
    screen.getByLabelText('Confirm profit access password'),
    ' valid password '
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(errorToast).toHaveBeenLastCalledWith('Too many attempts')
  expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(post).toHaveBeenNthCalledWith(
    3,
    '/api/profit/password',
    { password: ' valid password ' },
    expect.any(Object)
  )
})

it('separates the report and cost management into tabs while retaining cost search', async () => {
  prices = [savedPrice]
  renderProfit()
  const user = userEvent.setup()
  expect(screen.getByRole('tab', { name: 'Profit overview' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(
    screen.queryByRole('region', { name: 'Cost prices' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  expect(await screen.findByText('text-model')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Filters' })
  ).not.toBeInTheDocument()
  await user.type(
    screen.getByRole('textbox', { name: 'Search cost prices' }),
    'missing'
  )
  await user.click(screen.getByRole('tab', { name: 'Profit overview' }))
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  expect(
    screen.getByRole('textbox', { name: 'Search cost prices' })
  ).toHaveValue('missing')
})

it('discards unapplied filter edits when the filter dialog is reopened', async () => {
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Filters' }))
  await user.type(screen.getByPlaceholderText('Model name'), 'discarded-model')
  await user.click(
    within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' })
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Filters' }))
  expect(screen.getByPlaceholderText('Model name')).toHaveValue('')
  expect(summaryRequests).toHaveLength(1)
})

it('finds a channel beyond the first page by ID and selects its only model', async () => {
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Cost price' }))
  const dialog = screen.getByRole('dialog')
  const channel = within(dialog).getByRole('combobox', { name: 'Channel' })
  await user.type(channel, '101')
  await user.click(
    await screen.findByRole('option', { name: '#101 Archive provider' })
  )
  expect(channel).toHaveValue('#101 Archive provider')
  expect(within(dialog).getByRole('combobox', { name: 'Model' })).toHaveValue(
    'video-model'
  )
})

it('keeps selling prices out of cost inputs and groups token prices by modality', async () => {
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Cost price' }))
  const dialog = within(screen.getByRole('dialog'))
  await user.type(dialog.getByRole('combobox', { name: 'Channel' }), 'Primary')
  await user.click(
    await screen.findByRole('option', { name: '#7 Primary provider' })
  )
  await user.type(dialog.getByRole('combobox', { name: 'Model' }), 'text')
  await user.click(await screen.findByRole('option', { name: 'text-model' }))
  await screen.findByText('Selling price reference')
  const textGroup = dialog.getByRole('group', { name: 'Text tokens' })
  expect(
    within(textGroup).getByRole('spinbutton', { name: 'Input' })
  ).toHaveValue(null)
  expect(dialog.getByRole('group', { name: 'Prompt cache' })).toBeVisible()
  expect(dialog.getByRole('group', { name: 'Image tokens' })).toBeVisible()
  expect(dialog.getByRole('group', { name: 'Audio tokens' })).toBeVisible()
})

it('only applies the draft model filter when Apply filters is clicked', async () => {
  renderProfit()
  await waitFor(() => expect(summaryRequests).toHaveLength(1))
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Filters' }))
  await user.type(screen.getByPlaceholderText('Model name'), 'text-model')
  expect(summaryRequests).toHaveLength(1)
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(summaryRequests.at(-1)?.model_name).toBe('text-model')
  )
})

it('searches saved prices by channel ID and switches between active and disabled records', async () => {
  prices = [
    savedPrice,
    {
      ...savedPrice,
      id: 2,
      channel_id: 101,
      model_name: 'video-model',
      disabled: true,
    },
  ]
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  const panel = within(screen.getByRole('region', { name: 'Cost prices' }))
  expect(await panel.findByText('text-model')).toBeVisible()
  expect(panel.queryByText('video-model')).not.toBeInTheDocument()
  await user.type(
    panel.getByRole('textbox', { name: 'Search cost prices' }),
    '101'
  )
  expect(panel.getByText('No cost prices found')).toBeVisible()
  await user.click(panel.getByRole('combobox', { name: 'Cost price status' }))
  await user.click(screen.getByRole('option', { name: 'Disabled' }))
  expect(panel.getByText('video-model')).toBeVisible()
  expect(panel.getByRole('button', { name: 'Disable' })).toBeDisabled()
})

it('preserves sub-millionth cost precision when editing and submitting', async () => {
  prices = [savedPrice]
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true } })
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  await user.click(await screen.findByRole('button', { name: 'Edit' }))
  const dialog = within(screen.getByRole('dialog'))
  expect(dialog.getByRole('spinbutton', { name: 'Input' })).toHaveValue(
    0.00000012
  )
  expect(dialog.getByRole('combobox', { name: 'Channel' })).toBeDisabled()
  await user.click(dialog.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/profit/cost-prices',
      expect.objectContaining({ input_price: 0.00000012 }),
      expect.anything()
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

it('retains token inputs when switching billing modes and submits only the active unit in USD', async () => {
  prices = [savedPrice]
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7,
    },
  })
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true } })
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  await user.click(await screen.findByRole('button', { name: 'Edit' }))
  const dialog = within(screen.getByRole('dialog'))
  expect(dialog.getByRole('spinbutton', { name: 'Cache Read' })).toHaveValue(
    0.7
  )
  await user.click(dialog.getByRole('button', { name: 'Fixed per second' }))
  expect(
    dialog.queryByRole('group', { name: 'Text tokens' })
  ).not.toBeInTheDocument()
  await user.type(
    dialog.getByRole('spinbutton', { name: 'Fixed per second (CNY)' }),
    '14'
  )
  await user.click(dialog.getByRole('button', { name: 'Token unit price' }))
  expect(
    (dialog.getByRole('spinbutton', { name: 'Input' }) as HTMLInputElement)
      .valueAsNumber
  ).toBeCloseTo(0.00000084, 15)
  await user.click(dialog.getByRole('button', { name: 'Fixed per second' }))
  await user.click(dialog.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(post).toHaveBeenCalled())
  expect(post.mock.calls[0][1]).toMatchObject({
    price_type: 'fixed_price',
    fixed_unit: 'second',
    fixed_amount: 2,
  })
  expect(post.mock.calls[0][1]).not.toHaveProperty('input_price')
})

it('prevents repeated saves while pending and keeps the entered price after a failed save', async () => {
  prices = [savedPrice]
  let resolveSave!: (value: {
    data: { success: boolean; message: string }
  }) => void
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveSave = resolve
      })
  )
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  await user.click(await screen.findByRole('button', { name: 'Edit' }))
  const dialog = within(screen.getByRole('dialog'))
  await user.click(dialog.getByRole('button', { name: 'Save' }))
  expect(dialog.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  expect(dialog.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  expect(dialog.getByRole('spinbutton', { name: 'Input' })).toBeDisabled()
  await user.click(dialog.getByRole('button', { name: 'Saving...' }))
  expect(post).toHaveBeenCalledTimes(1)
  resolveSave({ data: { success: false, message: 'Save unavailable' } })
  await waitFor(() =>
    expect(dialog.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(dialog.getByRole('spinbutton', { name: 'Input' })).toHaveValue(
    0.00000012
  )
})

it('does not disable a cost price until confirmed and retains confirmation after failure', async () => {
  prices = [savedPrice]
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Cannot disable' },
    })
    .mockResolvedValueOnce({ data: { success: true } })
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  await user.click(await screen.findByRole('button', { name: 'Disable' }))
  const dialog = within(screen.getByRole('alertdialog'))
  expect(remove).not.toHaveBeenCalled()
  await user.click(dialog.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(remove).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Disable' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Disable',
    })
  )
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
  expect(screen.getByRole('alertdialog')).toBeVisible()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Disable',
    })
  )
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
})

it('shows a retry action instead of zero profit when the report fails', async () => {
  reportFails = true
  renderProfit()
  const user = userEvent.setup()
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.queryByText('Usage revenue')).not.toBeInTheDocument()
  reportFails = false
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('region', { name: 'Usage revenue' })
  ).toBeVisible()
})

it('paginates cost prices and returns to the first page when searching', async () => {
  prices = Array.from({ length: 11 }, (_, index) => ({
    ...savedPrice,
    id: index + 1,
    model_name: `model-${index + 1}`,
  }))
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  const panel = within(screen.getByRole('region', { name: 'Cost prices' }))
  expect(await panel.findByText('model-1')).toBeVisible()
  expect(panel.queryByText('model-11')).not.toBeInTheDocument()
  await user.click(panel.getByRole('button', { name: 'Go to next page' }))
  expect(panel.getByText('model-11')).toBeVisible()
  await user.type(
    panel.getByRole('textbox', { name: 'Search cost prices' }),
    'model-1'
  )
  expect(panel.getByText('model-1')).toBeVisible()
  expect(panel.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
})

it('applies quick date ranges while retaining the selected model filter', async () => {
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Filters' }))
  await user.type(screen.getByPlaceholderText('Model name'), 'text-model')
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Last 7 days' }))
  await waitFor(() =>
    expect(summaryRequests.at(-1)?.model_name).toBe('text-model')
  )
  const start = new Date(Number(summaryRequests.at(-1)?.start_timestamp) * 1000)
  const expected = new Date()
  expected.setDate(expected.getDate() - 6)
  expected.setHours(0, 0, 0, 0)
  expect(start.getTime()).toBe(expected.getTime())
  await user.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(summaryRequests.at(-1)?.model_name).toBeUndefined()
  )
})

it('filters unpriced and loss-making usage and opens the matching cost form', async () => {
  breakdown = [
    {
      ...metric,
      channel_id: 7,
      channel_name: 'Primary provider',
      model_name: 'text-model',
    },
    {
      ...metric,
      channel_id: 101,
      channel_name: 'Archive provider',
      model_name: 'video-model',
      unpriced_count: 4,
      priced_count: 0,
      coverage_rate: 0,
      profit: 0,
    },
    {
      ...metric,
      channel_id: 8,
      channel_name: 'Expensive provider',
      model_name: 'loss-model',
      profit: -2,
      profit_margin: -0.2,
    },
  ]
  renderProfit()
  const user = userEvent.setup()
  const panel = within(
    await screen.findByRole('region', { name: 'Channel model breakdown' })
  )
  await user.click(screen.getByRole('button', { name: 'View unpriced usage' }))
  expect(panel.getByRole('textbox', { name: 'Search breakdown' })).toHaveFocus()
  expect(panel.getByText('video-model')).toBeVisible()
  expect(panel.queryByText('text-model')).not.toBeInTheDocument()
  await user.click(panel.getByRole('button', { name: 'Configure cost price' }))
  const dialog = within(screen.getByRole('dialog'))
  expect(dialog.getByRole('combobox', { name: 'Channel' })).toHaveValue(
    '#101 Archive provider'
  )
  expect(dialog.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'video-model'
  )
  await user.click(dialog.getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(panel.getByRole('button', { name: 'Loss-making' }))
  expect(panel.getByText('loss-model')).toBeVisible()
  expect(panel.queryByText('video-model')).not.toBeInTheDocument()
  await user.click(panel.getByRole('button', { name: /^All$/ }))
  await user.type(
    panel.getByRole('textbox', { name: 'Search breakdown' }),
    '#7'
  )
  expect(panel.getByText('text-model')).toBeVisible()
  expect(panel.queryByText('loss-model')).not.toBeInTheDocument()
})

it('shows a saved cost price independently of historical coverage and opens its existing record', async () => {
  prices = [
    savedPrice,
    { ...savedPrice, id: 2, channel_id: 8, channel_name: 'Other provider' },
  ]
  breakdown = [
    {
      ...metric,
      channel_id: 7,
      channel_name: 'Primary provider',
      model_name: 'text-model',
      unpriced_count: 1,
      priced_count: 3,
      coverage_rate: 0.75,
    },
  ]
  renderProfit()
  const user = userEvent.setup()
  const row = within(
    await screen.findByRole('row', { name: /text-model.*Primary provider/ })
  )
  expect(await row.findByText('Cost price configured')).toBeVisible()
  expect(row.getByText('75.00%')).toBeVisible()
  expect(row.getByText('Unpriced requests: 1')).toBeVisible()
  expect(
    row.queryByRole('button', { name: 'Configure cost price' })
  ).not.toBeInTheDocument()
  await user.click(row.getByRole('button', { name: 'View cost price' }))
  const costs = within(
    await screen.findByRole('region', { name: 'Cost prices' })
  )
  expect(costs.getByText('#7 · Primary provider')).toBeVisible()
  expect(costs.queryByText('#8 · Other provider')).not.toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('keeps cost price status unknown while loading or after a fetch failure', async () => {
  breakdown = [
    {
      ...metric,
      channel_id: 7,
      channel_name: 'Primary provider',
      model_name: 'text-model',
      unpriced_count: 1,
    },
  ]
  let rejectPrices!: (reason: Error) => void
  const pendingPrices = new Promise<never>((_resolve, reject) => {
    rejectPrices = reject
  })
  const original = vi.mocked(api.get).getMockImplementation()
  if (!original) throw new Error('API fixture was not initialized')
  vi.mocked(api.get).mockImplementation((url, config) =>
    url === '/api/profit/cost-prices' ? pendingPrices : original(url, config)
  )
  renderProfit()
  const row = within(
    await screen.findByRole('row', { name: /text-model.*Primary provider/ })
  )
  expect(
    row.queryByRole('button', { name: 'Configure cost price' })
  ).not.toBeInTheDocument()
  expect(row.getByText('Loading...')).toBeVisible()
  await act(async () => {
    rejectPrices(new Error('Price service unavailable'))
  })
  expect(await row.findByText('Cost price status unavailable')).toBeVisible()
  expect(
    row.queryByRole('button', { name: 'Configure cost price' })
  ).not.toBeInTheDocument()
})

it('distinguishes disabled prices from missing prices for the same model on another channel', async () => {
  prices = [{ ...savedPrice, disabled: true }]
  breakdown = [7, 8].map((channel_id) => ({
    ...metric,
    channel_id,
    channel_name: `Provider ${channel_id}`,
    model_name: 'text-model',
    unpriced_count: 1,
  }))
  renderProfit()
  const disabled = within(
    await screen.findByRole('row', { name: /text-model.*Provider 7/ })
  )
  expect(await disabled.findByText('Disabled')).toBeVisible()
  expect(
    disabled.getByRole('button', { name: 'View cost price' })
  ).toBeVisible()
  expect(
    disabled.queryByRole('button', { name: 'Configure cost price' })
  ).not.toBeInTheDocument()
  const missing = within(
    screen.getByRole('row', { name: /text-model.*Provider 8/ })
  )
  expect(
    await missing.findByRole('button', { name: 'Configure cost price' })
  ).toBeVisible()
})

it('refreshes quick-range reports through the current time', async () => {
  renderProfit()
  const user = userEvent.setup()
  await screen.findByRole('region', { name: 'Usage revenue' })
  const nextNow = Date.now() + 60_000
  vi.spyOn(Date, 'now').mockReturnValue(nextNow)
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(summaryRequests.at(-1)?.end_timestamp).toBe(
      Math.floor(nextNow / 1000)
    )
  )
})

it('shows empty states when the report returns null series and breakdown', async () => {
  series = null
  breakdown = null
  renderProfit()
  expect(await screen.findByRole('region', { name: 'Profit' })).toBeVisible()
  expect(screen.getByRole('region', { name: 'Trend' })).toBeVisible()
  expect(screen.getByText('No matching usage')).toBeVisible()
})

it('shows chronological trend data and preserves negative profit', async () => {
  series = [
    {
      ...metric,
      period_start: 1704153600,
      period_label: '2024-01-02',
      profit: -2,
    },
    { ...metric, period_start: 1704067200, period_label: '2024-01-01' },
  ]
  renderProfit()
  const user = userEvent.setup()
  const trend = within(await screen.findByRole('region', { name: 'Trend' }))
  await user.click(trend.getByRole('tab', { name: 'Data' }))
  const rows = within(trend.getByRole('table')).getAllByRole('row')
  expect(rows[1]).toHaveTextContent('2024-01-01')
  expect(rows[2]).toHaveTextContent('2024-01-02')
  expect(rows[2]).toHaveTextContent('-$2')
})

it('defers channel and cost queries when the overview has no usage rows', async () => {
  renderProfit()
  const user = userEvent.setup()
  await screen.findByRole('region', { name: 'Usage revenue' })
  expect(api.get).not.toHaveBeenCalledWith('/api/channel', expect.anything())
  expect(api.get).not.toHaveBeenCalledWith(
    '/api/profit/cost-prices',
    expect.anything()
  )
  await user.click(screen.getByRole('tab', { name: 'Cost management' }))
  await screen.findByText('No cost prices found')
  expect(api.get).toHaveBeenCalledWith(
    '/api/profit/cost-prices',
    expect.anything()
  )
  expect(api.get).not.toHaveBeenCalledWith('/api/channel', expect.anything())
  await user.click(screen.getByRole('button', { name: 'Cost price' }))
  await waitFor(() =>
    expect(
      within(screen.getByRole('dialog')).getByRole('combobox', {
        name: 'Channel',
      })
    ).toBeEnabled()
  )
  expect(api.get).toHaveBeenCalledWith('/api/channel', expect.anything())
})

it('aborts the obsolete report request when switching date ranges', async () => {
  const get = vi.mocked(api.get)
  const fallback = get.getMockImplementation()
  if (!fallback) throw new Error('API fixture was not initialized')
  let initialSignal: AbortSignal | undefined
  get.mockImplementation((url, config) => {
    if (url === '/api/profit/summary' && !initialSignal) {
      initialSignal = config?.signal as AbortSignal | undefined
      return new Promise(() => {})
    }
    return fallback(url, config)
  })
  renderProfit()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Last 7 days' }))
  expect(initialSignal?.aborted).toBe(true)
  expect(
    await screen.findByRole('region', { name: 'Usage revenue' })
  ).toBeVisible()
})

it('updates memoized profit figures when the display currency changes', async () => {
  renderProfit()
  const profit = within(await screen.findByRole('region', { name: 'Profit' }))
  expect(profit.getByText('$8')).toBeVisible()
  act(() =>
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })
  )
  expect(profit.getByText('\u00a556')).toBeVisible()
  expect(summaryRequests).toHaveLength(1)
})
