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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestamp } from '@/lib/format'

import { getChannelAutoSwitchStatus } from '../api'

type ChannelAutoSwitchStatusProps = {
  channelId: number | null
  open: boolean
  savedEnabled: boolean
}

export function ChannelAutoSwitchStatus(props: ChannelAutoSwitchStatusProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['channels', 'auto-switch', props.channelId],
    queryFn: ({ signal }) =>
      getChannelAutoSwitchStatus(props.channelId ?? 0, signal),
    enabled: props.open && props.channelId !== null && props.savedEnabled,
    refetchInterval: props.open ? 30_000 : false,
    meta: { errorToast: false },
  })

  if (!props.channelId || !props.savedEnabled) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('Save this channel to view automatic switching status.')}
      </p>
    )
  }

  const statusLabels = {
    waiting: t('Waiting for a compatible backup'),
    ready: t('Ready for automatic switching'),
    healthy: t('Healthy'),
    cooling: t('Cooling down'),
    probing: t('Verifying recovery'),
  }
  const policy = query.data?.policy

  return (
    <section aria-label={t('Automatic switching status')} className='space-y-3'>
      <div className='flex items-center justify-between gap-2'>
        <h4 className='text-sm font-medium'>
          {t('Automatic switching status')}
        </h4>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Status reflects saved settings. Backup compatibility is checked for each request.'
        )}
      </p>
      {query.isPending && <LoadingState inline message={t('Loading...')} />}
      {query.isError && (
        <ErrorState
          className='min-h-0 py-4'
          title={t('Unable to load automatic switching status')}
          onRetry={() => void query.refetch()}
        />
      )}
      {!query.isError && query.data && query.data.scopes.length === 0 && (
        <EmptyState
          className='min-h-0 py-4'
          title={t('No routing scopes available')}
        />
      )}
      {!query.isError && query.data && (
        <ul className='space-y-2'>
          {query.data.scopes.map((scope) => (
            <li
              key={JSON.stringify([
                scope.group,
                scope.model,
                scope.request_path,
              ])}
              className='min-w-0 space-y-2 rounded-lg border p-3 text-xs'
            >
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <span className='min-w-0 font-medium break-all'>
                  {scope.group} / {scope.model}
                </span>
                <Badge
                  variant={scope.status === 'cooling' ? 'warning' : 'secondary'}
                  className='h-auto max-w-full whitespace-normal'
                >
                  {statusLabels[scope.status]}
                </Badge>
              </div>
              {scope.request_path && (
                <p className='text-muted-foreground break-all'>
                  {scope.request_path}
                </p>
              )}
              <p>
                {t('Participating channels: {{participants}}', {
                  participants: formatNumber(scope.participant_count, locale),
                })}
              </p>
              <p className='text-muted-foreground'>
                {t(
                  'Consecutive failures: {{failures}} / {{threshold}}; recovery successes: {{successes}} / {{required}}',
                  {
                    failures: formatNumber(scope.consecutive_failures, locale),
                    threshold: formatNumber(
                      policy?.failure_threshold ?? 0,
                      locale
                    ),
                    successes: formatNumber(scope.probe_successes, locale),
                    required: formatNumber(
                      policy?.recovery_successes ?? 0,
                      locale
                    ),
                  }
                )}
              </p>
              {scope.cooldown_until > 0 && scope.status === 'cooling' && (
                <p>
                  {t('Recovery probe available after {{time}}', {
                    time: formatTimestamp(scope.cooldown_until),
                  })}
                </p>
              )}
              {scope.last_reason && (
                <p className='text-muted-foreground break-words'>
                  {t('Last reason')}: {scope.last_reason}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
