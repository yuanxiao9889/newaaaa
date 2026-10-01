import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts'

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { formatMoney } from '../lib/format'
import type { ProfitSeriesItem } from '../types'

export default memo(function ProfitTrendChart(props: {
  series: ProfitSeriesItem[]
}) {
  const { t } = useTranslation()
  useSystemConfigStore((state) => state.config.currency)
  return (
    <ChartContainer
      className='h-64 w-full sm:h-72'
      aria-label={t('Revenue, cost and profit trend')}
      config={{
        revenue: { label: t('Usage revenue'), color: 'var(--chart-1)' },
        cost: { label: t('Cost'), color: 'var(--chart-3)' },
        profit: { label: t('Profit'), color: 'var(--chart-2)' },
      }}
    >
      <LineChart
        accessibilityLayer
        data={props.series}
        margin={{ left: 12, right: 20, top: 12 }}
      >
        <CartesianGrid vertical={false} strokeDasharray='3 3' />
        <XAxis
          dataKey='period_label'
          axisLine={false}
          tickLine={false}
          minTickGap={40}
          tickMargin={10}
        />
        <YAxis
          tickFormatter={formatMoney}
          axisLine={false}
          tickLine={false}
          width={80}
        />
        <ReferenceLine y={0} stroke='var(--border)' />
        <ChartTooltip
          isAnimationActive={false}
          content={
            <ChartTooltipContent
              formatter={(value, name) => (
                <div className='flex w-full items-center justify-between gap-5'>
                  <span className='text-muted-foreground'>{name}</span>
                  <span className='font-mono font-medium tabular-nums'>
                    {formatMoney(Number(value))}
                  </span>
                </div>
              )}
            />
          }
        />
        <ChartLegend content={<ChartLegendContent />} />
        <Line
          dataKey='revenue'
          name={t('Usage revenue')}
          stroke='var(--color-revenue)'
          strokeWidth={2}
          dot={props.series.length === 1}
          isAnimationActive={false}
        />
        <Line
          dataKey='cost'
          name={t('Cost')}
          stroke='var(--color-cost)'
          strokeWidth={2}
          strokeDasharray='4 4'
          dot={props.series.length === 1}
          isAnimationActive={false}
        />
        <Line
          dataKey='profit'
          name={t('Profit')}
          stroke='var(--color-profit)'
          strokeWidth={3}
          dot={props.series.length === 1}
          isAnimationActive={false}
        />
      </LineChart>
    </ChartContainer>
  )
})
