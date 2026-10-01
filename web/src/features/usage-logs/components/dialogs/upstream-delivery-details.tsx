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

import { CopyButton } from '@/components/copy-button'

import type { UpstreamDelivery } from '../../types'
import { DetailRow, DetailSection } from './log-detail-layout'

export function UpstreamDeliveryDetails(props: {
  attempts: UpstreamDelivery[]
}) {
  const { t } = useTranslation()
  if (props.attempts.length === 0) return null
  return (
    <DetailSection label={t('Upstream delivery evidence')}>
      <p className='text-muted-foreground text-xs'>
        {t(
          'A local write does not confirm upstream acceptance. An HTTP response may come from a gateway.'
        )}
      </p>
      <CopyButton
        value={JSON.stringify(props.attempts, null, 2)}
        aria-label={t('Copy upstream delivery evidence')}
      />
      <ol aria-label={t('Upstream delivery evidence')} className='space-y-3'>
        {props.attempts.map((attempt) => (
          <li
            key={`${attempt.worker_attempt}-${attempt.attempt}`}
            className='space-y-1'
          >
            <DetailRow
              label={t('Channel')}
              value={`#${attempt.attempt} / ${attempt.channel_id}`}
            />
            <DetailRow
              label={t('Start Time')}
              value={new Date(attempt.started_at).toLocaleString()}
            />
            <DetailRow
              label={t('Last transport stage')}
              value={
                attempt.failure_stage
                  ? `${attempt.stage} (${attempt.failure_stage})`
                  : attempt.stage
              }
              mono
            />
            <DetailRow
              label={t('Connected to upstream')}
              value={
                attempt.connection_ready ? t('Observed') : t('Not observed')
              }
            />
            <DetailRow
              label={t('Request written locally')}
              value={
                attempt.request_written ? t('Observed') : t('Not observed')
              }
            />
            <DetailRow
              label={t('Upstream HTTP response')}
              value={
                attempt.response_received
                  ? String(attempt.status_code)
                  : t('Not observed')
              }
            />
            <DetailRow
              label={t('Response body completed')}
              value={
                attempt.response_complete ? t('Observed') : t('Not observed')
              }
            />
            <DetailRow
              label={t('Elapsed time (ms)')}
              value={attempt.elapsed_ms.toLocaleString()}
            />
            {attempt.host && (
              <DetailRow
                label={t('Upstream host')}
                value={
                  attempt.peer_address
                    ? `${attempt.host} (${attempt.peer_address})`
                    : attempt.host
                }
                mono
              />
            )}
            {attempt.upstream_request_id && (
              <DetailRow
                label={t('Upstream Request ID')}
                value={attempt.upstream_request_id}
                mono
              />
            )}
            {attempt.error_kind && (
              <DetailRow
                label={t('Type')}
                value={attempt.error_kind}
                mono
              />
            )}
          </li>
        ))}
      </ol>
    </DetailSection>
  )
}
