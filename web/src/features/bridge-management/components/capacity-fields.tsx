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

import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { capacitySchema, type BridgeCapacityConfig } from '../lib/capacity-form'
import type { BridgeCapacityStatus } from '../types'

export function BridgeCapacityFields(props: {
  value: BridgeCapacityConfig
  status: BridgeCapacityStatus
  disabled: boolean
  onChange: (value: BridgeCapacityConfig) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const valid = capacitySchema.safeParse(props.value).success
  const definitions = [
    {
      key: 'global',
      label: t('Global active transfers'),
      max: 32,
      scale: 1,
      min: 1,
    },
    {
      key: 'per_account',
      label: t('Active transfers per account'),
      max: 32,
      scale: 1,
      min: 1,
    },
    {
      key: 'queue',
      label: t('Maximum queued transfers'),
      max: 64,
      scale: 1,
      min: 0,
    },
    {
      key: 'per_account_queue',
      label: t('Queued transfers per account'),
      max: 64,
      scale: 1,
      min: 0,
    },
    {
      key: 'wait_ms',
      label: t('Maximum queue wait (seconds)'),
      max: 3,
      scale: 1000,
      min: 0,
    },
  ] as const
  return (
    <section className='space-y-4' aria-label={t('Bridge capacity and queues')}>
      <h3 className='font-medium'>{t('Bridge capacity and queues')}</h3>
      <p className='text-muted-foreground'>
        {t(
          'Uploads and downloads have separate bounded queues. Queue expiry or a full queue allows automatic clients to recover through the original route. Active transfers drain when limits decrease.'
        )}
      </p>
      {(['upload', 'download'] as const).map((kind) => {
        const label = kind === 'upload' ? t('Uploads') : t('Downloads')
        const measured = props.status[kind]
        return (
          <FieldSet key={kind}>
            <FieldLegend>{label}</FieldLegend>
            <p>
              {t(
                'Active: {{active}}; queued: {{queued}}; rejected: {{rejected}}; timed out: {{timeout}}; cancelled: {{cancelled}}',
                {
                  active: formatNumber(measured.active, locale),
                  queued: formatNumber(measured.queued, locale),
                  rejected: formatNumber(measured.rejected, locale),
                  timeout: formatNumber(measured.timed_out, locale),
                  cancelled: formatNumber(measured.cancelled, locale),
                }
              )}
            </p>
            {measured.draining && (
              <p role='status'>
                {t('Existing transfers are draining to the new limits')}
              </p>
            )}
            <FieldGroup className='grid gap-4 md:grid-cols-2'>
              {definitions.map((field) => (
                <Field key={field.key}>
                  <FieldLabel htmlFor={`capacity-${kind}-${field.key}`}>
                    {field.label}
                  </FieldLabel>
                  <Input
                    id={`capacity-${kind}-${field.key}`}
                    type='number'
                    aria-label={`${label}: ${field.label}`}
                    disabled={props.disabled}
                    min={field.min}
                    max={field.max}
                    step={field.scale === 1000 ? 0.1 : 1}
                    value={props.value[kind][field.key] / field.scale}
                    aria-invalid={!valid}
                    onChange={(event) =>
                      props.onChange({
                        ...props.value,
                        [kind]: {
                          ...props.value[kind],
                          [field.key]: event.target.valueAsNumber * field.scale,
                        },
                      })
                    }
                  />
                </Field>
              ))}
            </FieldGroup>
          </FieldSet>
        )
      })}
      <FieldGroup className='grid gap-4 md:grid-cols-2'>
        {(
          [
            [
              'bandwidth_bytes_per_second',
              t('Configured bridge bandwidth (MiB/s)'),
            ],
            [
              'api_reserved_bytes_per_second',
              t('Bandwidth reserved for task requests (MiB/s)'),
            ],
          ] as const
        ).map(([key, label]) => (
          <Field key={key}>
            <FieldLabel htmlFor={`capacity-${key}`}>{label}</FieldLabel>
            <Input
              id={`capacity-${key}`}
              type='number'
              min={key === 'bandwidth_bytes_per_second' ? 0.125 : 0.0625}
              max={1024}
              step={0.0625}
              disabled={props.disabled}
              aria-invalid={!valid}
              value={props.value[key] / 1048576}
              onChange={(event) =>
                props.onChange({
                  ...props.value,
                  [key]: event.target.valueAsNumber * 1048576,
                })
              }
            />
          </Field>
        ))}
      </FieldGroup>
      <p className='text-muted-foreground'>
        {t(
          'File uploads and downloads share the configured bandwidth minus the task reserve, including cached and legacy file routes. Set bandwidth from measurements; protocol overhead and unrelated server traffic are outside this payload limit.'
        )}
      </p>
      {!valid && (
        <p role='alert'>
          {t(
            'Invalid capacity settings: account limits must fit global limits; combined active limits cannot exceed 48. Disable queues with all three queue values set to zero. Keep at least 64 KiB/s each for task reserve and files.'
          )}
        </p>
      )}
    </section>
  )
}
