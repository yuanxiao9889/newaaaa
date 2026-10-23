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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatNumber, formatTimestamp } from '@/lib/format'

import type { BridgeManagementData } from '../types'

export function BridgeOverview(props: { data: BridgeManagementData }) {
  const { t, i18n } = useTranslation()
  const data = props.data
  const check = data.last_check
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  return (
    <SettingsCard title={t('Bridge overview')}>
      <div className='grid gap-5 text-sm md:grid-cols-2'>
        <div className='space-y-2'>
          <p>
            {t('Saved server policy: v{{version}}', { version: data.version })}
          </p>
          <Badge variant='secondary'>
            {data.policy.enabled
              ? t('Bridge admission enabled')
              : t('Bridge admission disabled')}
          </Badge>
          <p className='text-muted-foreground'>
            {t(
              'Saved on the relay does not mean applied by every client. Client fetched version and active task version are unknown here. Existing tasks keep their frozen policy.'
            )}
          </p>
          <p>
            {t('Request recovery')}:{' '}
            {data.supported.request_recovery
              ? t('Supported')
              : t('Not supported')}
          </p>
          <p>
            {t('Download route switching')}:{' '}
            {data.supported.download_routing
              ? t('Supported')
              : t('Not supported')}
          </p>
        </div>
        <div className='space-y-2'>
          <p className='font-medium'>{t('Last bridge check')}</p>
          {!check && (
            <p className='text-muted-foreground'>{t('Not checked yet')}</p>
          )}
          {check && (
            <>
              <Badge variant={check.ok ? 'secondary' : 'destructive'}>
                {check.ok ? t('Reachable') : t('Check failed')}
              </Badge>
              <p>
                {typeof check.checked_at === 'number'
                  ? formatTimestamp(check.checked_at)
                  : dayjs(check.checked_at).format('YYYY-MM-DD HH:mm:ss')}
              </p>
              {check.ok && (
                <p>
                  {t('Latency: {{value}} ms', {
                    value: formatNumber(check.latency_ms, locale),
                  })}
                </p>
              )}
              {!check.ok && (
                <p className='break-words'>
                  {check.error || t('Bridge check did not succeed')}
                </p>
              )}
            </>
          )}
          <p className='text-muted-foreground'>
            {t(
              'This rate-limited check sends no generation request and incurs no generation charge. It measures relay-to-bridge reachability, not the user download speed.'
            )}
          </p>
        </div>
      </div>
    </SettingsCard>
  )
}
