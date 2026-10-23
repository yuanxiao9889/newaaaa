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
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { SettingsCard } from '../components/settings-card'
import { SettingsSwitchField } from '../components/settings-form-layout'
import { safeNumberFieldProps } from '../utils/numeric-field'
import type { RoutingPolicyFormValues } from './routing-form'

export function RetrySection() {
  const { t } = useTranslation()
  const form = useFormContext<RoutingPolicyFormValues>()
  const timed = useWatch({
    control: form.control,
    name: 'RetryTimeBudgetEnabled',
  })
  return (
    <SettingsCard title={t('Retry budget')} className='shadow-none'>
      <div className='space-y-4'>
        <FormField
          control={form.control}
          name='ImageInputRetentionMinutes'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Image input retention')}</FormLabel>
              <FormControl>
                <NativeSelect {...field} onChange={event => field.onChange(Number(event.target.value))}>
                  {[10, 20, 30].map(minutes => (
                    <NativeSelectOption key={minutes} value={minutes}>
                      {t('{{minutes}} minutes', { minutes })}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </FormControl>
              <FormDescription>
                {t('Keep uploaded image references and original inputs of successful or failed tasks for this duration. New preparation or accepted resubmission renews retention; status checks do not. The selected duration applies on the next preparation, use or completion.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='RetryTimes'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Maximum retries')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={0}
                  max={99}
                  step={1}
                  {...safeNumberFieldProps(field)}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Excludes the first attempt. With channel cycling, retries are counted across all groups; otherwise they are counted per group.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='RetryCycleEnabled'
          render={({ field }) => (
            <SettingsSwitchField
              controlId='retry-cycle'
              checked={field.value}
              onCheckedChange={field.onChange}
              label={t('Cycle through channels after exhaustion')}
              description={t(
                'Try each eligible channel before starting another round. Wait one second between rounds; disabled channels and request restrictions still apply.'
              )}
            />
          )}
        />
        <FormField
          control={form.control}
          name='RetryTimeBudgetEnabled'
          render={({ field }) => (
            <SettingsSwitchField
              controlId='retry-time-budget'
              checked={field.value}
              onCheckedChange={field.onChange}
              label={t('Use a time budget instead of the retry count')}
              description={t(
                'Retryable failures may exceed the count limit within this budget, even when maximum retries is zero. Enable channel cycling to reuse exhausted channels.'
              )}
            />
          )}
        />
        <FormField
          control={form.control}
          name='RetryTimeBudgetSeconds'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Total retry time budget (seconds)')}</FormLabel>
              <FormControl>
                <Input
                  type='number'
                  min={1}
                  max={3600}
                  step={1}
                  disabled={!timed}
                  {...safeNumberFieldProps(field)}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Counted from the first upstream attempt, including request and wait time. At expiry, no new attempt starts; an in-flight request may finish later. Accepted video jobs keep their existing polling policy.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='AutomaticRetryStatusCodes'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Auto-retry status codes')}</FormLabel>
              <FormControl>
                <Input
                  {...field}
                  placeholder={t('e.g. 401, 403, 429, 500-599')}
                />
              </FormControl>
              <FormDescription>
                {t('2xx, 504 and 524 are always excluded.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
    </SettingsCard>
  )
}
