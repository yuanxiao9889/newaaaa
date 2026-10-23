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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { applyBridgeRuntime, bridgeManagementQueryKey } from '../api'
import { capacitySchema } from '../lib/capacity-form'
import type { BridgeManagementData, BridgeRuntimeStatus } from '../types'
import { BridgeCapacityFields } from './capacity-fields'

export function BridgeRuntime(props: {
  data: BridgeManagementData
  canEdit: boolean
}) {
  const { t } = useTranslation()
  const runtime = props.data.runtime
  return (
    <SettingsCard title={t('Bridge storage and transfer status')}>
      <p className='text-muted-foreground mb-4 text-sm'>
        {t(
          'Videos stream without a complete disk copy. Verified result images expire four hours after completion; downloads do not extend this lifetime. Domestic input materials keep their separate retention rules.'
        )}
      </p>
      {!runtime?.available || !runtime.status ? (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Bridge runtime status is unavailable; cache and transfer counters are unknown'
          )}
        </p>
      ) : (
        <RuntimeDetails
          key={runtime.status.applied_revision}
          status={runtime.status}
          checkedAt={runtime.checked_at}
          canEdit={props.canEdit}
        />
      )}
    </SettingsCard>
  )
}

function RuntimeDetails(props: {
  status: BridgeRuntimeStatus
  checkedAt: number
  canEdit: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [config, setConfig] = useState(props.status.config)
  const [reason, setReason] = useState('')
  const update = useMutation({
    mutationFn: applyBridgeRuntime,
    retry: false,
    onSuccess: (data) => {
      client.setQueryData(bridgeManagementQueryKey, data)
      setReason('')
    },
    onError: (error) => handleServerError(error),
  })
  const status = props.status
  const metrics = status.metrics
  const rows = [
    [
      t('Bridge directly fetching upstream'),
      metrics.upstream_requests,
      metrics.upstream_bytes,
    ],
    [
      t('Bridge fetching through relay'),
      metrics.relay_requests,
      metrics.relay_bytes,
    ],
    [t('Bridge image cache hits'), metrics.cache_hits, metrics.cache_bytes],
    [
      t('Unclassified bridge traffic'),
      metrics.unknown_route_requests,
      metrics.unknown_bytes ?? 0,
    ],
  ] as const
  const disabled = !props.canEdit || update.isPending
  const valid =
    (config.capacity === undefined ||
      capacitySchema.safeParse(config.capacity).success) &&
    Number.isInteger(config.image_cache_max_bytes) &&
    config.image_cache_max_bytes >= 64 * 1048576 &&
    config.image_cache_max_bytes <= 4096 * 1048576 &&
    Number.isInteger(config.image_cache_max_file_bytes) &&
    config.image_cache_max_file_bytes >= 1048576 &&
    config.image_cache_max_file_bytes <= 64 * 1048576 &&
    config.image_cache_max_file_bytes <= config.image_cache_max_bytes &&
    Number.isInteger(config.max_resume_attempts) &&
    config.max_resume_attempts >= 0 &&
    config.max_resume_attempts <= 3

  return (
    <div className='space-y-5 text-sm'>
      <p>
        {t('Applied bridge configuration: v{{version}}', {
          version: status.applied_revision,
        })}
      </p>
      <p className='text-muted-foreground'>
        {t('Runtime checked at {{time}}', {
          time: formatTimestamp(props.checkedAt),
        })}
      </p>
      <p>
        {t(
          'Cached images: {{count}}; used {{used}} MiB; reserved {{reserved}} MiB',
          {
            count: formatNumber(status.cache.entries, locale),
            used: formatNumber(
              (status.cache.bytes + (status.cache.orphan_bytes ?? 0)) / 1048576,
              locale
            ),
            reserved: formatNumber(
              status.cache.reserved_bytes / 1048576,
              locale
            ),
          }
        )}
      </p>
      <p>
        {t('Active downloads: {{downloads}}; cache readers: {{readers}}', {
          downloads: formatNumber(status.active_downloads, locale),
          readers: formatNumber(status.cache.active_readers, locale),
        })}
      </p>
      <div className='overflow-x-auto'>
        <table className='w-full text-left'>
          <thead>
            <tr>
              <th className='pb-2'>{t('Actual bridge transfer path')}</th>
              <th className='pb-2'>{t('Requests')}</th>
              <th className='pb-2'>{t('Transferred MiB')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(([label, count, bytes]) => (
              <tr key={label}>
                <td className='py-1'>{label}</td>
                <td>{formatNumber(count, locale)}</td>
                <td>{formatNumber(bytes / 1048576, locale)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p>
        {t(
          'Resume attempts: {{attempts}}; successful: {{success}}; rejected: {{rejected}}',
          {
            attempts: formatNumber(metrics.resume_attempts, locale),
            success: formatNumber(metrics.resume_successes, locale),
            rejected: formatNumber(metrics.resume_rejected, locale),
          }
        )}
      </p>
      <p>
        {t(
          'Stream failures: {{failures}}; cancellations: {{cancellations}}; cache errors: {{errors}}',
          {
            failures: formatNumber(metrics.stream_failures, locale),
            cancellations: formatNumber(metrics.cancellations, locale),
            errors: formatNumber(metrics.cache_errors, locale),
          }
        )}
      </p>
      <p className='text-muted-foreground'>
        {t(
          'These aggregate counters start at gateway process startup. Original-route downloads and client fallback reasons are not observed here. A zero value is not a measurement of those paths.'
        )}
      </p>
      <div className='grid gap-4 md:grid-cols-2'>
        <label className='flex items-center gap-3'>
          <Switch
            aria-label={t('Enable result image cache')}
            checked={config.image_cache_enabled}
            disabled={disabled}
            onCheckedChange={(value) =>
              setConfig({ ...config, image_cache_enabled: value })
            }
          />
          {t('Enable result image cache')}
        </label>
        <label className='space-y-2'>
          <span>{t('Image cache capacity (MiB)')}</span>
          <Input
            type='number'
            min={64}
            max={4096}
            disabled={disabled}
            value={config.image_cache_max_bytes / 1048576}
            onChange={(event) =>
              setConfig({
                ...config,
                image_cache_max_bytes: event.target.valueAsNumber * 1048576,
              })
            }
          />
        </label>
        <label className='space-y-2'>
          <span>{t('Maximum cached image size (MiB)')}</span>
          <Input
            type='number'
            min={1}
            max={64}
            disabled={disabled}
            value={config.image_cache_max_file_bytes / 1048576}
            onChange={(event) =>
              setConfig({
                ...config,
                image_cache_max_file_bytes:
                  event.target.valueAsNumber * 1048576,
              })
            }
          />
        </label>
        <label className='space-y-2'>
          <span>{t('Gateway resume attempts')}</span>
          <Input
            type='number'
            min={0}
            max={3}
            disabled={disabled}
            value={config.max_resume_attempts}
            onChange={(event) =>
              setConfig({
                ...config,
                max_resume_attempts: event.target.valueAsNumber,
              })
            }
          />
        </label>
      </div>
      {status.version >= 3 && config.capacity && status.capacity ? (
        <BridgeCapacityFields
          value={config.capacity}
          status={status.capacity}
          disabled={disabled}
          onChange={(capacity) => setConfig({ ...config, capacity })}
        />
      ) : (
        <p className='text-muted-foreground'>
          {t(
            'This bridge version does not report configurable capacity or queues. Upgrade the gateway before editing these limits.'
          )}
        </p>
      )}
      {props.canEdit && (
        <div className='space-y-3'>
          <label className='block space-y-2'>
            <span>{t('Runtime change reason')}</span>
            <Input
              value={reason}
              maxLength={500}
              disabled={disabled}
              onChange={(event) => setReason(event.target.value)}
            />
          </label>
          <Button
            disabled={disabled || !valid || !reason.trim()}
            onClick={() =>
              update.mutate({
                config: { ...config, revision: status.applied_revision + 1 },
                reason: reason.trim(),
              })
            }
          >
            {t('Apply bridge runtime settings')}
          </Button>
          <p className='text-muted-foreground'>
            {t(
              'Applies to the bridge immediately after its durable configuration acknowledgement. Client route policy is saved separately above. Conflicting revisions must be reloaded.'
            )}
          </p>
        </div>
      )}
    </div>
  )
}
