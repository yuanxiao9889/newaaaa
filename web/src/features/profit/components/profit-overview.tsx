import type { ColumnDef } from '@tanstack/react-table'
import { AlertTriangle, BarChart3, DollarSign, ShieldCheck } from 'lucide-react'
import {
  lazy,
  memo,
  Suspense,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTableColumnHeader,
  DataTablePagination,
  DataTableView,
  StaticDataTable,
  useDataTable,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { cn } from '@/lib/utils'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { formatMoney, formatPercent } from '../lib/format'
import type {
  ProfitBreakdownItem,
  ProfitCostPrice,
  ProfitReport,
} from '../types'

const ProfitTrendChart = lazy(() => import('./profit-trend-chart'))

export const ProfitOverview = memo(function ProfitOverview(props: {
  report: ProfitReport
  costPrices?: ProfitCostPrice[]
  costPricesLoading: boolean
  onViewPrice: (price: ProfitCostPrice) => void
  scrollContainerRef: RefObject<HTMLDivElement | null>
  onConfigure: (channel: string, model: string) => void
}) {
  const { t } = useTranslation()
  // Currency formatting reads this store; keep memoized displays reactive.
  useSystemConfigStore((state) => state.config.currency)
  const { onConfigure, onViewPrice, costPricesLoading } = props
  const costPricesByModel = useMemo(() => {
    if (!props.costPrices) return undefined
    return new Map(
      props.costPrices.map((price) => [
        JSON.stringify([price.channel_id, price.model_name.trim()]),
        price,
      ])
    )
  }, [props.costPrices])
  const breakdownRef = useRef<HTMLElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const [search, setSearch] = useState('')
  const [scope, setScope] = useState('all')
  const summary = props.report.summary
  const series = useMemo(
    () =>
      [...(props.report.series ?? [])].sort(
        (a, b) => a.period_start - b.period_start
      ),
    [props.report.series]
  )
  const rows = useMemo(
    () =>
      (props.report.breakdown ?? []).filter((item) => {
        if (scope === 'unpriced' && item.unpriced_count === 0) return false
        if (scope === 'loss' && item.profit >= 0) return false
        return `#${item.channel_id} ${item.channel_name} ${item.model_name}`
          .toLowerCase()
          .includes(search.trim().toLowerCase())
      }),
    [props.report.breakdown, search, scope]
  )
  const columns = useMemo<ColumnDef<ProfitBreakdownItem>[]>(
    () => [
      {
        accessorKey: 'model_name',
        header: t('Channel / model'),
        enableSorting: false,
        cell: ({ row }) => (
          <div className='max-w-72 space-y-1'>
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
      ...(
        [
          ['request_count', t('Requests')],
          ['revenue', t('Usage revenue')],
          ['cost', t('Cost')],
          ['profit', t('Profit')],
        ] as const
      ).map(
        ([key, title]): ColumnDef<ProfitBreakdownItem> => ({
          accessorKey: key,
          enableHiding: false,
          header: ({ column }) => (
            <DataTableColumnHeader
              column={column}
              title={title}
              className='justify-end'
            />
          ),
          cell: ({ row }) => (
            <span
              className={cn(
                'block text-right tabular-nums',
                key === 'profit' && 'font-semibold',
                key === 'profit' &&
                  row.original.profit < 0 &&
                  'text-destructive'
              )}
            >
              {key === 'request_count'
                ? row.original[key].toLocaleString()
                : formatMoney(row.original[key])}
            </span>
          ),
        })
      ),
      {
        accessorKey: 'coverage_rate',
        enableHiding: false,
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('Coverage')}
            className='justify-end'
          />
        ),
        cell: ({ row }) => (
          <div className='text-right tabular-nums'>
            {formatPercent(row.original.coverage_rate)}
            {row.original.unpriced_count > 0 && (
              <div className='text-muted-foreground text-xs'>
                {t('Unpriced requests: {{count}}', {
                  count: row.original.unpriced_count,
                })}
              </div>
            )}
          </div>
        ),
      },
      {
        id: 'actions',
        header: t('Cost price status'),
        cell: ({ row }) => {
          if (!costPricesByModel) {
            return (
              <span className='text-muted-foreground text-xs'>
                {costPricesLoading
                  ? t('Loading...')
                  : t('Cost price status unavailable')}
              </span>
            )
          }
          const price = costPricesByModel.get(
            JSON.stringify([
              row.original.channel_id,
              row.original.model_name.trim(),
            ])
          )
          if (price?.disabled || price?.price_configured) {
            return (
              <div className='flex flex-wrap items-center gap-2'>
                <StatusBadge
                  label={
                    price.disabled ? t('Disabled') : t('Cost price configured')
                  }
                  variant={price.disabled ? 'neutral' : 'success'}
                  size='sm'
                  copyable={false}
                />
                <Button
                  size='sm'
                  variant='ghost'
                  onClick={() => onViewPrice(price)}
                >
                  {t('View cost price')}
                </Button>
              </div>
            )
          }
          return (
            <Button
              size='sm'
              variant='outline'
              onClick={() =>
                onConfigure(
                  String(row.original.channel_id),
                  row.original.model_name
                )
              }
            >
              {t('Configure cost price')}
            </Button>
          )
        },
      },
    ],
    [t, onConfigure, onViewPrice, costPricesByModel, costPricesLoading]
  )
  const { table } = useDataTable({
    data: rows,
    columns,
    initialPagination: { pageIndex: 0, pageSize: 10 },
    initialSorting: [{ id: 'profit', desc: true }],
    enableRowSelection: false,
  })

  return (
    <div className='space-y-5'>
      <div className='grid grid-cols-2 gap-3 xl:grid-cols-4'>
        <section
          aria-label={t('Profit')}
          className={cn(
            'rounded-xl border bg-primary/5 p-4',
            summary.profit < 0 && 'border-destructive/30 bg-destructive/5'
          )}
        >
          <StatCard
            title={t('Profit')}
            value={formatMoney(summary.profit)}
            description={t('Profit from priced usage')}
            icon={BarChart3}
            details={[
              {
                label: t('Margin'),
                value: formatPercent(summary.profit_margin),
                tone: summary.profit < 0 ? 'destructive' : 'default',
              },
            ]}
          />
          <p className='text-muted-foreground mt-2 text-xs tabular-nums sm:hidden'>
            {t('Margin')}: {formatPercent(summary.profit_margin)}
          </p>
        </section>
        <section
          aria-label={t('Usage revenue')}
          className='rounded-xl border p-4'
        >
          <StatCard
            title={t('Usage revenue')}
            value={formatMoney(summary.revenue)}
            description={t('Calculated from usage logs, not top-up payments')}
            icon={DollarSign}
            details={[
              {
                label: t('Requests'),
                value: (summary.request_count ?? 0).toLocaleString(),
              },
            ]}
          />
          <p className='text-muted-foreground mt-2 text-xs tabular-nums sm:hidden'>
            {t('Requests')}: {(summary.request_count ?? 0).toLocaleString()}
          </p>
        </section>
        <section aria-label={t('Cost')} className='rounded-xl border p-4'>
          <StatCard
            title={t('Cost')}
            value={formatMoney(summary.cost)}
            description={t('Configured upstream costs')}
            icon={DollarSign}
            details={[
              {
                label: t('Priced requests'),
                value: (summary.priced_count ?? 0).toLocaleString(),
              },
            ]}
          />
          <p className='text-muted-foreground mt-2 text-xs tabular-nums sm:hidden'>
            {t('Priced requests')}:{' '}
            {(summary.priced_count ?? 0).toLocaleString()}
          </p>
        </section>
        <section aria-label={t('Coverage')} className='rounded-xl border p-4'>
          <StatCard
            title={t('Coverage')}
            value={formatPercent(summary.coverage_rate)}
            description={t('Requests included in profit calculations')}
            icon={ShieldCheck}
            details={[
              {
                label: t('Unpriced requests'),
                value: (summary.unpriced_count ?? 0).toLocaleString(),
                tone: summary.unpriced_count > 0 ? 'warning' : 'muted',
              },
            ]}
          />
          <p className='text-muted-foreground mt-2 text-xs tabular-nums sm:hidden'>
            {t('Unpriced requests')}:{' '}
            {(summary.unpriced_count ?? 0).toLocaleString()}
          </p>
        </section>
      </div>
      {summary.unpriced_count > 0 && (
        <div className='border-warning/30 bg-warning/5 flex flex-wrap items-center justify-between gap-3 rounded-lg border px-4 py-3 text-sm'>
          <div className='flex min-w-0 items-start gap-2'>
            <AlertTriangle className='text-warning mt-0.5 size-4 shrink-0' />
            <p>
              {t(
                'Cost coverage reflects usage in the selected period, not the current cost price configuration. Profit only includes usage with calculated costs.'
              )}
            </p>
          </div>
          <Button
            size='sm'
            variant='outline'
            onClick={() => {
              setScope('unpriced')
              setSearch('')
              table.setPageIndex(0)
              const container = props.scrollContainerRef.current
              const breakdown = breakdownRef.current
              if (container && breakdown) {
                // Keep the dashboard shell fixed when jumping to the breakdown.
                container.scrollTop +=
                  breakdown.getBoundingClientRect().top -
                  container.getBoundingClientRect().top -
                  16
              }
              searchRef.current?.focus({ preventScroll: true })
            }}
          >
            {t('View unpriced usage')}
          </Button>
        </div>
      )}
      <section
        aria-label={t('Trend')}
        className='min-w-0 rounded-xl border p-4 sm:p-5'
      >
        <Tabs defaultValue='chart'>
          <div className='mb-3 flex flex-wrap items-center justify-between gap-3'>
            <div>
              <h3 className='font-semibold'>{t('Trend')}</h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Compare revenue, cost and profit over the selected period.'
                )}
              </p>
            </div>
            <TabsList aria-label={t('Trend display')}>
              <TabsTrigger value='chart'>{t('Chart')}</TabsTrigger>
              <TabsTrigger value='data'>{t('Data')}</TabsTrigger>
            </TabsList>
          </div>
          <TabsContent value='chart'>
            {series.length === 0 ? (
              <EmptyState className='min-h-48' />
            ) : (
              <Suspense fallback={<LoadingState className='h-64' />}>
                <ProfitTrendChart series={series} />
              </Suspense>
            )}
          </TabsContent>
          <TabsContent value='data'>
            <StaticDataTable
              className='max-h-80 overflow-auto'
              data={series}
              getRowKey={(item) => item.period_start}
              emptyContent={t('No Data')}
              columns={[
                {
                  id: 'period',
                  header: t('Period'),
                  cell: (item) => item.period_label,
                },
                {
                  id: 'revenue',
                  header: t('Usage revenue'),
                  className: 'text-right',
                  cell: (item) => formatMoney(item.revenue),
                },
                {
                  id: 'cost',
                  header: t('Cost'),
                  className: 'text-right',
                  cell: (item) => formatMoney(item.cost),
                },
                {
                  id: 'profit',
                  header: t('Profit'),
                  className: 'text-right',
                  cellClassName: (item) =>
                    item.profit < 0
                      ? 'text-destructive tabular-nums'
                      : 'tabular-nums',
                  cell: (item) => formatMoney(item.profit),
                },
                {
                  id: 'coverage',
                  header: t('Coverage'),
                  className: 'text-right',
                  cell: (item) => formatPercent(item.coverage_rate),
                },
              ]}
            />
          </TabsContent>
        </Tabs>
      </section>
      <section
        ref={breakdownRef}
        aria-label={t('Channel model breakdown')}
        className='min-w-0 scroll-mt-4 space-y-4 rounded-xl border p-4 sm:p-5'
      >
        <div className='flex flex-wrap items-start justify-between gap-3'>
          <div>
            <h3 className='font-semibold'>{t('Channel model breakdown')}</h3>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t('Search, sort and inspect the channels that need attention.')}
            </p>
          </div>
          <ToggleGroup
            aria-label={t('Breakdown filter')}
            variant='outline'
            value={[scope]}
            onValueChange={(values) => {
              if (values[0]) {
                setScope(values[0])
                table.setPageIndex(0)
              }
            }}
          >
            <ToggleGroupItem value='all'>{t('All')}</ToggleGroupItem>
            <ToggleGroupItem value='unpriced'>
              {t('Unpriced usage')}
            </ToggleGroupItem>
            <ToggleGroupItem value='loss'>{t('Loss-making')}</ToggleGroupItem>
          </ToggleGroup>
        </div>
        <Input
          ref={searchRef}
          aria-label={t('Search breakdown')}
          placeholder={t('Search channel, ID or model')}
          className='sm:max-w-sm'
          value={search}
          onChange={(event) => {
            setSearch(event.target.value)
            table.setPageIndex(0)
          }}
        />
        <DataTableView
          table={table}
          tableClassName='min-w-[760px]'
          emptyTitle={t('No matching usage')}
          emptyDescription={t('Try another search or switch to all usage.')}
        />
        <DataTablePagination table={table} />
      </section>
    </div>
  )
})
