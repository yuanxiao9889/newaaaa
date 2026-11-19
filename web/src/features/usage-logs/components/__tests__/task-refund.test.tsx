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
import { act, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { UsageLog } from '../../data/schema'
import { getAsyncTaskBillingStateLabel } from '../../lib/format'
import type { LogOtherData } from '../../types'
import { useCommonLogsColumns } from '../columns/common-logs-columns'
import { DetailsDialog } from '../dialogs/details-dialog'

vi.mock('@lobehub/icons', () => ({}))

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
const previousConfig = useSystemConfigStore.getState().config

afterEach(() => {
  queryClient.clear()
  useSystemConfigStore.getState().setConfig(previousConfig)
})

function RefundPreview(props: { log: UsageLog }) {
  const table = useReactTable({
    data: [props.log],
    columns: useCommonLogsColumns(false, false),
    getCoreRowModel: getCoreRowModel(),
  })
  const cell = table
    .getRowModel()
    .rows[0].getAllCells()
    .find((item) => item.column.id === 'content')
  if (!cell) throw new Error('Refund logs must have a content column')
  return (
    <>
      <div data-testid='refund-preview'>
        {flexRender(cell.column.columnDef.cell, cell.getContext())}
      </div>
      <DetailsDialog
        log={props.log}
        isAdmin={false}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </>
  )
}

test.each([
  {
    state: 'pending',
    actual: 1520000,
    quota: 4370000,
    content: 'task usage expression settlement',
    en: 'Succeeded · difference refunded',
    zh: '成功 · 已退差额',
  },
  {
    state: 'refunded',
    actual: 0,
    quota: 5890000,
    content: 'Upstream generation failed',
    en: 'Failed · fully refunded',
    zh: '失败 · 已全额退款',
  },
])(
  'shows $en in both preview and details, with language switching',
  async (scenario) => {
    useSystemConfigStore
      .getState()
      .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
    queryClient.setQueryData(['status'], {}, { updatedAt: Date.now() + 60000 })
    const i18n = createInstance()
    await i18n.init({
      lng: 'en',
      fallbackLng: 'en',
      resources: { en, zh },
      interpolation: { escapeValue: false },
    })
    const log: UsageLog = {
      id: 1,
      user_id: 1,
      created_at: 1,
      type: 6,
      content: scenario.content,
      username: 'tester',
      token_name: '',
      model_name: 'wan3.0',
      quota: scenario.quota,
      prompt_tokens: 0,
      completion_tokens: 0,
      use_time: 0,
      is_stream: false,
      channel: 201,
      channel_name: '',
      token_id: 0,
      group: 'default',
      ip: '',
      request_id: 'refund-request',
      upstream_request_id: '',
      other: JSON.stringify({
        task_id: 'task-refund',
        is_task: true,
        billing_state: scenario.state,
        pre_consumed_quota: 5890000,
        actual_quota: scenario.actual,
      }),
    }
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <RefundPreview log={log} />
        </QueryClientProvider>
      </I18nextProvider>
    )
    expect(
      within(screen.getByTestId('refund-preview')).getByText(scenario.en)
    ).toBeInTheDocument()
    const dialog = within(screen.getByRole('dialog'))
    expect(dialog.getByText(scenario.en)).toBeVisible()
    expect(dialog.getByText('Refund amount')).toBeVisible()
    expect(dialog.getByText('Refund amount').parentElement).toHaveTextContent(
      scenario.state === 'pending' ? '$8.74' : '$11.78'
    )
    const preview = within(screen.getByTestId('refund-preview')).getByText(
      scenario.en
    )
    if (scenario.state === 'pending') {
      expect(preview).not.toHaveClass('text-red-600')
    } else {
      expect(preview).toHaveClass('text-red-600')
      expect(dialog.getByText(scenario.content)).toBeVisible()
    }
    await act(() => i18n.changeLanguage('zh'))
    expect(
      within(screen.getByTestId('refund-preview')).getByText(scenario.zh)
    ).toBeInTheDocument()
    expect(dialog.getByText(scenario.zh)).toBeVisible()
    expect(dialog.queryByText('已退款')).not.toBeInTheDocument()
  }
)

test.each<[LogOtherData, number, string]>([
  [
    { billing_state: 'pending', pre_consumed_quota: 100, actual_quota: 0 },
    6,
    'Succeeded · difference refunded',
  ],
  [
    { pre_consumed_quota: 100, actual_quota: 40 },
    6,
    'Succeeded · difference refunded',
  ],
  [{}, 6, 'Refunded'],
  [{ billing_state: 'refunded' }, 6, 'Refunded'],
  [{ actual_quota: 0 }, 6, 'Refunded'],
  [{ pre_consumed_quota: 100, actual_quota: 0 }, 6, 'Refunded'],
  [{ pre_consumed_quota: 100, actual_quota: -1 }, 6, 'Refunded'],
  [{ pre_consumed_quota: 100, actual_quota: 200 }, 6, 'Refunded'],
  [{ billing_state: 'refunded', actual_quota: 0 }, 8, 'Pre-consume rollback'],
  [{ billing_state: 'settled' }, 2, 'Settled'],
  [{ billing_state: 'pending' }, 2, 'Pre-charged'],
])(
  'classifies billing metadata %j (type %i) as %s',
  (other, type, expected) => {
    expect(getAsyncTaskBillingStateLabel(other, type)).toBe(expected)
  }
)
