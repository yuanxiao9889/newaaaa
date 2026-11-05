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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { getCurrencyDisplay } from '@/lib/currency'
import { formatLogQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS, LOG_TYPE_ENUM } from '../constants'
import { buildApiParams } from '../lib/utils'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
}) {
  return (
    <span className='border-border/60 bg-muted/25 inline-flex h-7 items-center gap-2 rounded-md border px-2.5 text-xs shadow-xs'>
      <span className={cn('h-3.5 w-0.5 rounded-full', props.accent)} />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

export function CommonLogsStats() {
  const { t, i18n } = useTranslation()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()
  useSystemConfigStore((state) => state.config.currency)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const params = useMemo(
    () =>
      buildApiParams({
        page: 1,
        pageSize: 1,
        searchParams,
        columnFilters: [],
        isAdmin,
      }),
    [searchParams, isAdmin]
  )
  const showTopup = params.type === LOG_TYPE_ENUM.TOPUP

  const { data: stats, isLoading } = useQuery({
    queryKey: ['usage-logs-stats', isAdmin, params],
    queryFn: async () => {
      const result = isAdmin
        ? requireServerSuccess(await getLogStats(params))
        : requireServerSuccess(await getUserLogStats(params))

      return result.success
        ? result.data || DEFAULT_LOG_STATS
        : DEFAULT_LOG_STATS
    },
  })

  const amounts = new Map(
    Object.entries(stats?.topup?.amounts ?? {}).map(([symbol, amount]) => [
      symbol,
      Number(amount),
    ])
  )
  const quota = Number(stats?.topup?.quota ?? 0)
  const { config, meta } = getCurrencyDisplay()
  const symbol = meta.kind === 'tokens' ? '' : meta.symbol
  const creditedAmount =
    meta.kind === 'tokens'
      ? quota
      : (quota / config.quotaPerUnit) * meta.exchangeRate
  if (creditedAmount !== 0 || amounts.size === 0) {
    amounts.set(symbol, (amounts.get(symbol) ?? 0) + creditedAmount)
  }
  // The shared number formatter is limited to two decimals; log amounts need six.
  const numberFormat = new Intl.NumberFormat(locale, {
    maximumFractionDigits: 6,
  })
  const topupTotal = [...amounts]
    .map(([currency, amount]) => `${currency}${numberFormat.format(amount)}`)
    .join(' + ')
  let topupValue = stats?.topup ? topupTotal : '—'
  if (!sensitiveVisible) topupValue = '••••'

  if (isLoading) {
    return (
      <div className='flex flex-wrap items-center gap-2'>
        <Skeleton className='h-7 w-[150px] rounded-md' />
        {showTopup && <Skeleton className='h-7 w-[150px] rounded-md' />}
        <Skeleton className='h-7 w-[100px] rounded-md' />
        <Skeleton className='h-7 w-[120px] rounded-md' />
      </div>
    )
  }

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <StatBadge
        label={t('Usage')}
        value={sensitiveVisible ? formatLogQuota(stats?.quota || 0) : '••••'}
        accent='bg-sky-500/70'
      />
      {showTopup && (
        <Tooltip>
          <TooltipTrigger render={<span tabIndex={0} />}>
            <StatBadge
              label={t('Top-up total')}
              value={topupValue}
              accent='bg-emerald-500/70'
            />
          </TooltipTrigger>
          <TooltipContent>
            {t(
              'Total credited across all pages for the selected filters. Excludes balance deductions and subscription purchases. Historical amounts retain their recorded currency.'
            )}
          </TooltipContent>
        </Tooltip>
      )}
      {showTopup && stats?.topup && stats.topup.uncounted > 0 && (
        <span className='text-muted-foreground text-xs'>
          {t('{{count}} records could not be included', {
            count: stats.topup.uncounted,
          })}
        </span>
      )}
      <StatBadge
        label={t('RPM')}
        value={stats?.rpm || 0}
        accent='bg-rose-500/65'
      />
      <StatBadge
        label={t('TPM')}
        value={stats?.tpm || 0}
        accent='bg-slate-400/70'
      />
    </div>
  )
}
