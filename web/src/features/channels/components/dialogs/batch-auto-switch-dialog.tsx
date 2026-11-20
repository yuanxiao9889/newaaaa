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
import { useId, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { handleServerError } from '@/lib/handle-server-error'

import {
  batchSetChannelAutoSwitch,
  type BatchAutoSwitchSettings,
} from '../../api'
import { channelsQueryKeys } from '../../lib/channel-actions'
import {
  channelFormSchema,
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import type { Channel } from '../../types'
import { MediaRetryFields } from '../channel-quick-options'

export function BatchAutoSwitchDialog(props: {
  channels: Channel[]
  onSaved: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const id = useId()
  const [defaults] = useState(() => {
    const values = props.channels.map(transformChannelToFormDefaults)
    const first = values[0]
    const sameLimits = values.every(
      (value) =>
        value.auto_switch_retry_window_minutes ===
          first.auto_switch_retry_window_minutes &&
        value.auto_switch_max_attempts === first.auto_switch_max_attempts
    )
    return {
      auto_switch_enabled: values.some(
        (value) => value.auto_switch_enabled === true
      ),
      auto_switch_retry_window_minutes: sameLimits
        ? first.auto_switch_retry_window_minutes
        : 10,
      auto_switch_max_attempts: sameLimits ? first.auto_switch_max_attempts : 3,
    }
  })
  const form = useForm<ChannelFormValues>({ defaultValues: defaults })
  const enabled = useWatch({
    control: form.control,
    name: 'auto_switch_enabled',
  })
  const save = useMutation({
    mutationFn: (settings: BatchAutoSwitchSettings) =>
      batchSetChannelAutoSwitch(
        props.channels.map((ch) => ch.id),
        settings
      ),
    onError: (error) => handleServerError(error),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      toast.success(t('Updated successfully'))
      props.onSaved()
      props.onClose()
    },
  })
  const submit = form.handleSubmit((values) => {
    if (!values.auto_switch_enabled) {
      save.mutate({ auto_switch_enabled: false })
      return
    }
    const minutes = channelFormSchema.shape.auto_switch_retry_window_minutes
      .unwrap()
      .safeParse(values.auto_switch_retry_window_minutes)
    const attempts = channelFormSchema.shape.auto_switch_max_attempts
      .unwrap()
      .safeParse(values.auto_switch_max_attempts)
    if (!minutes.success) {
      form.setError('auto_switch_retry_window_minutes', {
        message: minutes.error.issues[0].message,
      })
    }
    if (!attempts.success) {
      form.setError('auto_switch_max_attempts', {
        message: attempts.error.issues[0].message,
      })
    }
    if (minutes.success && attempts.success) {
      save.mutate({
        auto_switch_enabled: true,
        auto_switch_retry_window_minutes: minutes.data,
        auto_switch_max_attempts: attempts.data,
      })
    }
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.onClose()
      }}
      title={t('Set automatic switching for selected channels')}
      description={
        <span className='block max-h-20 overflow-auto break-words'>
          {props.channels.map((ch) => `#${ch.id}`).join(', ')}
        </span>
      }
      footer={
        <>
          <Button
            variant='outline'
            disabled={save.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={id} disabled={save.isPending}>
            {t('Apply to selected channels ({{count}})', {
              count: props.channels.length,
            })}
          </Button>
        </>
      }
    >
      <form id={id} noValidate onSubmit={submit} className='space-y-3'>
        <SettingsSwitchField
          controlId={`${id}-enabled`}
          checked={!!enabled}
          onCheckedChange={(value) =>
            form.setValue('auto_switch_enabled', value)
          }
          disabled={save.isPending}
          label={t('Automatic switching')}
        />
        {enabled && <MediaRetryFields form={form} disabled={save.isPending} />}
      </form>
    </Dialog>
  )
}
