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
import { useEffect } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'

const asyncImageTaskSchema = z.object({
  AsyncImageInternalTaskEnabled: z.boolean(),
  AsyncImageRetentionHours: z.enum(['2', '6', '12', '18', '24']),
  AsyncImageWorkerConcurrency: z.number().int().min(1).max(256),
  AsyncImageMaxUnfinishedTasks: z.number().int().min(1).max(100000),
})

type AsyncImageTaskFormValues = z.infer<typeof asyncImageTaskSchema>

type AsyncImageTaskSettingsSectionProps = {
  defaultValues: AsyncImageTaskFormValues
}

export function AsyncImageTaskSettingsSection({
  defaultValues,
}: AsyncImageTaskSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const form = useForm<AsyncImageTaskFormValues>({
    resolver: zodResolver(asyncImageTaskSchema),
    defaultValues,
  })

  useEffect(() => {
    form.reset(defaultValues)
  }, [defaultValues, form])

  const onSubmit = async (values: AsyncImageTaskFormValues) => {
    const updates = Object.entries(values).filter(
      ([key, value]) =>
        value !== defaultValues[key as keyof AsyncImageTaskFormValues]
    )

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({ key, value })
    }
  }

  return (
    <SettingsSection title={t('Async Image Tasks')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save async image task settings'
          />
          <div className='space-y-4'>
            <FormField
              control={form.control}
              name='AsyncImageInternalTaskEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Enable async image tasks')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Accepts ?async=true on image generation and edit requests, then stores the result for later download.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </SettingsSwitchItem>
              )}
            />

            <FormField
              control={form.control}
              name='AsyncImageRetentionHours'
              render={({ field }) => (
                <div className='flex min-w-0 flex-col gap-2 border-b py-2.5 last:border-b-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4'>
                  <div className='min-w-0 space-y-0.5'>
                    <FormLabel>{t('Async image retention')}</FormLabel>
                    <FormDescription>
                      {t(
                        'How long downloaded async image files remain available.'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <NativeSelect
                      className='w-full sm:w-[160px]'
                      value={field.value}
                      onChange={field.onChange}
                    >
                      {['2', '6', '12', '18', '24'].map((hours) => (
                        <NativeSelectOption key={hours} value={hours}>
                          {t('{{count}} hours', { count: Number(hours) })}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </div>
              )}
            />

            <FormField
              control={form.control}
              name='AsyncImageWorkerConcurrency'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Worker concurrency')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='1'
                      max='256'
                      step='1'
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Maximum number of async image tasks processed at the same time.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AsyncImageMaxUnfinishedTasks'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Queue limit')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min='1'
                      max='100000'
                      step='1'
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Maximum number of submitted, queued, and processing async image tasks.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
