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
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import { FieldGroup } from '@/components/ui/field'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

import type { ChannelFormValues } from '../lib/channel-form'
import { GPT_IMAGE_4K_FILTER_PRESET } from '../lib/image-request-filter'

export function ImageRequestFilterSettings(props: {
  form: UseFormReturn<ChannelFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation()
  const enabled = useWatch({
    control: props.form.control,
    name: 'image_request_filter_enabled',
  })
  const hasDraft = useWatch({
    control: props.form.control,
    name: 'image_request_filter',
    compute: (value) => Boolean(value?.trim()),
  })

  const loadPreset = () => {
    props.form.setValue(
      'image_request_filter',
      JSON.stringify(GPT_IMAGE_4K_FILTER_PRESET, null, 2),
      { shouldDirty: true, shouldValidate: true }
    )
    props.form.setValue('image_request_filter_enabled', true, {
      shouldDirty: true,
      shouldValidate: true,
    })
  }

  return (
    <FieldGroup className='gap-4'>
      <FormField
        control={props.form.control}
        name='image_request_filter_enabled'
        render={({ field }) => (
          <FormItem className='flex items-center justify-between gap-3'>
            <div className='space-y-0.5'>
              <FormLabel>{t('Image request filtering')}</FormLabel>
              <FormDescription>
                {t(
                  'Skip this channel when a listed model does not match both quality and size. Other models are unaffected. Disabling keeps the rules'
                )}
              </FormDescription>
            </div>
            <FormControl>
              <Switch
                checked={field.value === true}
                onCheckedChange={field.onChange}
                disabled={props.disabled}
              />
            </FormControl>
          </FormItem>
        )}
      />
      <Button
        type='button'
        variant='outline'
        size='sm'
        className='self-start text-left whitespace-normal'
        disabled={props.disabled}
        onClick={loadPreset}
      >
        {t('Load GPT Image 4K preset')}
      </Button>
      <p className='text-muted-foreground text-sm'>
        {t(
          'The preset accepts 15 built-in 4K sizes: gpt-image-2 at medium/high, and gpt-image-2.5-sunburst/flare at high/xhigh/max. Custom sizes and missing or auto values are excluded'
        )}
      </p>
      {(enabled || hasDraft) && (
        <FormField
          control={props.form.control}
          name='image_request_filter'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Image request filter rules (JSON)')}</FormLabel>
              <FormControl>
                <JsonCodeEditor
                  value={field.value || ''}
                  onChange={field.onChange}
                  name={field.name}
                  onBlur={field.onBlur}
                  textareaRef={field.ref}
                  disabled={props.disabled}
                  ariaLabel={t('Image request filter rules (JSON)')}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Use client model names before model mapping. Each model can appear in only one rule. Filtering preserves the original request parameters'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      {!enabled && !hasDraft && (
        <FormField
          control={props.form.control}
          name='image_request_filter'
          render={() => (
            <FormItem>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
    </FieldGroup>
  )
}
