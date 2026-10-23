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

import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestamp } from '@/lib/format'

import type { BridgeManagementData } from '../types'

export function BridgeObservations(props: { data: BridgeManagementData }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const observation = props.data.client_observations
  const routes = [
    ['bridge_upstream', t('Bridge directly fetching upstream')],
    ['bridge_relay', t('Bridge fetching through relay')],
    ['origin_upstream', t('Original route directly fetching upstream')],
    ['origin_relay', t('Original route fetching through relay')],
  ] as const
  const reasons = [
    ['completed', t('Completed')],
    ['user_direct', t('Direct route selected by user')],
    ['bridge_unavailable', t('Bridge unavailable')],
    ['ticket_unavailable', t('Download ticket unavailable')],
    ['slow_route', t('Slow route')],
    ['stream_error', t('Stream error')],
    ['resumed', t('Resumed')],
    ['cancelled', t('Cancelled')],
    ['other', t('Other')],
  ] as const
  return (
    <SettingsCard title={t('Client reported download paths')}>
      <p className='text-muted-foreground mb-4 text-sm'>
        {t(
          'These are authenticated client reports, separate from gateway measurements and billing. Reports may be missing when clients are offline or outdated. Counters reset when the relay restarts; duplicate event IDs are ignored for 24 hours.'
        )}
      </p>
      {!observation ? (
        <p className='text-sm'>
          {t('Client download observations are unavailable')}
        </p>
      ) : (
        <div className='space-y-3 text-sm'>
          <p>
            {t('Observation window starts at {{time}}', {
              time: formatTimestamp(observation.started_at),
            })}
          </p>
          <div className='overflow-x-auto'>
            <table className='w-full text-left'>
              <thead>
                <tr>
                  <th>{t('Download route')}</th>
                  <th>{t('Reports')}</th>
                  <th>{t('Transferred MiB')}</th>
                  <th>{t('Resume count')}</th>
                  <th>{t('Completed / failed / cancelled')}</th>
                  <th>{t('Reported route reasons')}</th>
                </tr>
              </thead>
              <tbody>
                {routes.map(([key, label]) => {
                  const data = observation.routes[key]
                  if (!data) {
                    return (
                      <tr key={key}>
                        <td>{label}</td>
                        <td colSpan={5}>{t('Unknown')}</td>
                      </tr>
                    )
                  }
                  const labels = reasons
                    .filter(([reason]) => data.reasons[reason] > 0)
                    .map(
                      ([reason, text]) =>
                        `${text}: ${formatNumber(data.reasons[reason], locale)}`
                    )
                  return (
                    <tr key={key}>
                      <td className='py-2'>{label}</td>
                      <td>{formatNumber(data.reports, locale)}</td>
                      <td>{formatNumber(data.bytes / 1048576, locale)}</td>
                      <td>{formatNumber(data.resumes, locale)}</td>
                      <td>
                        {[data.completed, data.failed, data.cancelled]
                          .map((count) => formatNumber(count, locale))
                          .join(' / ')}
                      </td>
                      <td>
                        {labels.length > 0
                          ? labels.join('; ')
                          : t('No reports')}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </SettingsCard>
  )
}
