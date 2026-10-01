import type { ColumnDef } from '@tanstack/react-table'
import { Pencil, RefreshCw, Trash2 } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTableColumnHeader,
  DataTablePagination,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'

import { formatMoney, toDateInput } from '../lib/format'
import type { ProfitCostPrice } from '../types'

export function CostPriceTable(props: {
  prices: ProfitCostPrice[]
  loading: boolean
  error: boolean
  refreshing: boolean
  onRefresh: () => void
  search: string
  onSearchChange: (value: string) => void
  status: string
  onStatusChange: (value: string) => void
  onEdit: (item: ProfitCostPrice) => void
  onDisable: (item: ProfitCostPrice) => void
}) {
  const { t } = useTranslation()
  const { onEdit, onDisable } = props
  const rows = useMemo(
    () =>
      props.prices.filter((item) => {
        if (props.status === 'active' && item.disabled) return false
        if (props.status === 'disabled' && !item.disabled) return false
        return `#${item.channel_id} ${item.channel_name ?? ''} ${item.model_name}`
          .toLowerCase()
          .includes(props.search.trim().toLowerCase())
      }),
    [props.prices, props.status, props.search]
  )
  const columns = useMemo<ColumnDef<ProfitCostPrice>[]>(
    () => [
      {
        accessorKey: 'model_name',
        enableHiding: false,
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Channel / model')} />
        ),
        cell: ({ row }) => (
          <div className='max-w-64 space-y-1'>
            <div
              className='truncate font-medium'
              title={row.original.model_name}
            >
              {row.original.model_name}
            </div>
            <div
              className='text-muted-foreground truncate text-xs'
              title={row.original.channel_name}
            >
              #{row.original.channel_id} · {row.original.channel_name}
            </div>
          </div>
        ),
      },
      {
        id: 'price',
        header: t('Price summary'),
        cell: ({ row }) => (
          <div className='max-w-96 min-w-48 space-y-1'>
            <div className='text-xs font-medium'>
              {priceTypeLabel(row.original.price_type, t)}
            </div>
            <div className='text-muted-foreground text-xs leading-relaxed whitespace-normal'>
              {priceSummary(row.original, t)}
            </div>
          </div>
        ),
      },
      {
        accessorKey: 'effective_from',
        enableHiding: false,
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Effective from')} />
        ),
        cell: ({ row }) => (
          <span className='tabular-nums'>
            {toDateInput(row.original.effective_from)}
          </span>
        ),
      },
      {
        accessorKey: 'disabled',
        header: t('Cost price status'),
        enableSorting: false,
        cell: ({ row }) => (
          <StatusBadge
            label={row.original.disabled ? t('Disabled') : t('Active')}
            variant={row.original.disabled ? 'neutral' : 'success'}
            size='sm'
            copyable={false}
          />
        ),
      },
      {
        id: 'actions',
        header: t('Actions'),
        cell: ({ row }) => (
          <div className='flex items-center gap-1'>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Edit')}
              title={
                row.original.price_type === 'tiered_expr'
                  ? t('Advanced expression cost prices cannot be edited here')
                  : t('Edit')
              }
              disabled={row.original.price_type === 'tiered_expr'}
              onClick={() => onEdit(row.original)}
            >
              <Pencil className='size-4' />
            </Button>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Disable')}
              title={t('Disable')}
              disabled={row.original.disabled}
              onClick={() => onDisable(row.original)}
            >
              <Trash2 className='size-4' />
            </Button>
          </div>
        ),
      },
    ],
    [t, onEdit, onDisable]
  )
  const { table } = useDataTable({
    data: rows,
    columns,
    initialPagination: { pageIndex: 0, pageSize: 10 },
    initialSorting: [{ id: 'effective_from', desc: true }],
    enableRowSelection: false,
  })
  return (
    <section
      aria-label={t('Cost prices')}
      className='min-w-0 space-y-4 rounded-xl border p-4 sm:p-5'
    >
      <div className='flex items-start justify-between gap-3'>
        <div>
          <h3 className='font-semibold'>{t('Cost prices')}</h3>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Manage upstream costs by channel and model. New prices apply from their effective date.'
            )}
          </p>
        </div>
        <Button
          variant='ghost'
          size='icon-sm'
          aria-label={t('Refresh')}
          title={t('Refresh')}
          disabled={props.refreshing}
          onClick={props.onRefresh}
        >
          <RefreshCw
            className={props.refreshing ? 'size-4 animate-spin' : 'size-4'}
          />
        </Button>
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <Input
          aria-label={t('Search cost prices')}
          placeholder={t('Search channel, ID or model')}
          className='w-full sm:w-80'
          value={props.search}
          onChange={(event) => {
            props.onSearchChange(event.target.value)
            table.setPageIndex(0)
          }}
        />
        <Combobox
          aria-label={t('Cost price status')}
          className='w-40'
          value={props.status}
          onValueChange={(value) => {
            props.onStatusChange(value ?? 'active')
            table.setPageIndex(0)
          }}
          options={[
            { value: 'active', label: t('Active') },
            { value: 'disabled', label: t('Disabled') },
            { value: 'all', label: t('All') },
          ]}
        />
      </div>
      {props.error ? (
        <ErrorState className='min-h-32' onRetry={props.onRefresh} />
      ) : (
        <>
          <DataTableView
            table={table}
            tableClassName='min-w-[760px]'
            isLoading={props.loading}
            emptyTitle={t('No cost prices found')}
            emptyDescription={t('Try another search or add a cost price.')}
          />
          <DataTablePagination table={table} />
        </>
      )}
    </section>
  )
}

function priceTypeLabel(
  type: ProfitCostPrice['price_type'],
  t: (key: string) => string
) {
  if (type === 'simple_token') return t('Unit pricing')
  if (type === 'tiered_expr') return t('Advanced expression')
  return t('Fixed cost')
}

function priceSummary(item: ProfitCostPrice, t: (key: string) => string) {
  if (item.price_type === 'tiered_expr') return t('Advanced expression')
  if (item.price_type === 'fixed_price') {
    return item.fixed_unit === 'second'
      ? `${formatMoney(item.second_price ?? 0)} / ${t('Per second')}`
      : `${formatMoney(item.request_price ?? 0)} / ${t('Per request')}`
  }
  const parts: string[] = []
  if ((item.input_price ?? 0) > 0) {
    parts.push(`${t('Input')} ${formatMoney(item.input_price ?? 0)}`)
  }
  if ((item.cache_read_price ?? 0) > 0) {
    parts.push(`${t('Cache Read')} ${formatMoney(item.cache_read_price ?? 0)}`)
  }
  if ((item.cache_creation_price ?? 0) > 0) {
    parts.push(
      `${t('Cache Creation')} ${formatMoney(item.cache_creation_price ?? 0)}`
    )
  }
  if ((item.cache_creation_1h_price ?? 0) > 0) {
    parts.push(
      `${t('Cache Creation (1h)')} ${formatMoney(item.cache_creation_1h_price ?? 0)}`
    )
  }
  if ((item.image_input_price ?? 0) > 0) {
    parts.push(
      `${t('Image input')} ${formatMoney(item.image_input_price ?? 0)}`
    )
  }
  if ((item.image_output_price ?? 0) > 0) {
    parts.push(
      `${t('Image Output')} ${formatMoney(item.image_output_price ?? 0)}`
    )
  }
  if ((item.audio_input_price ?? 0) > 0) {
    parts.push(
      `${t('Audio Input')} ${formatMoney(item.audio_input_price ?? 0)}`
    )
  }
  if ((item.audio_output_price ?? 0) > 0) {
    parts.push(
      `${t('Audio Output')} ${formatMoney(item.audio_output_price ?? 0)}`
    )
  }
  if ((item.output_price ?? 0) > 0) {
    parts.push(`${t('Output')} ${formatMoney(item.output_price ?? 0)}`)
  }
  const summaries: string[] = []
  if (parts.length) {
    summaries.push(`${parts.join(' \u00b7 ')} / ${t('1M tokens')}`)
  }
  if ((item.request_price ?? 0) > 0) {
    summaries.push(
      `${formatMoney(item.request_price ?? 0)} / ${t('Per request')}`
    )
  }
  if ((item.second_price ?? 0) > 0) {
    summaries.push(
      `${formatMoney(item.second_price ?? 0)} / ${t('Per second')}`
    )
  }
  return summaries.join(' \u00b7 ') || '-'
}
