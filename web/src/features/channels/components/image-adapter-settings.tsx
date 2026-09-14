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
import { useState } from 'react'
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { mergeImageAdapterParameterOverride } from '../lib/image-adapter'
import { NATIVE_ASYNC_IMAGE_ADAPTER_PRESET } from '../lib/image-adapter-preset'

export function ImageAdapterSettings(props: {
  form: UseFormReturn<ChannelFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation()
  const enabled = useWatch({
    control: props.form.control,
    name: 'image_adapter_enabled',
  })
  const draft = useWatch({ control: props.form.control, name: 'image_adapter' })
  const [templateNotice, setTemplateNotice] = useState('')

  const loadTemplate = () => {
    try {
      const merged = mergeImageAdapterParameterOverride(
        props.form.getValues('param_override')
      )
      props.form.setValue('param_override', merged.value, {
        shouldDirty: true,
        shouldValidate: true,
      })
      props.form.setValue(
        'image_adapter',
        JSON.stringify(NATIVE_ASYNC_IMAGE_ADAPTER_PRESET, null, 2),
        { shouldDirty: true, shouldValidate: true }
      )
      props.form.setValue('image_adapter_enabled', true, {
        shouldDirty: true,
        shouldValidate: true,
      })
      const notice =
        merged.preservedFields.length > 0
          ? t(
              'Image template loaded. Existing rules for {{fields}} were preserved; review async=true, n=1 and stream=false in Parameter Override',
              { fields: merged.preservedFields.join(', ') }
            )
          : t(
              'Image template loaded. Image-only async=true, n=1 and stream=false rules were added to Parameter Override'
            )
      setTemplateNotice(notice)
    } catch {
      toast.error(
        t('Fix Parameter Override JSON before loading the image template')
      )
    }
  }

  return (
    <FieldGroup className='gap-4'>
      <FormField
        control={props.form.control}
        name='image_adapter_enabled'
        render={({ field }) => (
          <FormItem className='flex items-center justify-between gap-3'>
            <div className='space-y-0.5'>
              <FormLabel>{t('Image API conversion')}</FormLabel>
              <FormDescription>
                {t(
                  'Convert image requests and async tasks for this upstream. Disabling keeps the saved mapping'
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
        onClick={loadTemplate}
      >
        {t('Load native async public-reference template')}
      </Button>
      {templateNotice && (
        <Alert>
          <AlertDescription>{templateNotice}</AlertDescription>
        </Alert>
      )}
      {(enabled || Boolean(draft?.trim())) && (
        <FormField
          control={props.form.control}
          name='image_adapter'
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {t('Image conversion configuration (JSON)')}
              </FormLabel>
              <FormControl>
                <JsonCodeEditor
                  value={field.value || ''}
                  onChange={field.onChange}
                  name={field.name}
                  onBlur={field.onBlur}
                  textareaRef={field.ref}
                  disabled={props.disabled}
                  ariaLabel={t('Image conversion configuration (JSON)')}
                />
              </FormControl>
              <FormDescription className='space-y-2'>
                <span className='block'>
                  {t(
                    'The switch controls enabled. Empty models applies to all image models in this channel; otherwise list upstream model IDs after model mapping. Request mappings use top-level field names; task paths use dots, such as data.0.url'
                  )}
                </span>
                <span className='block'>
                  {t(
                    'Paths are relative to the channel Base URL. Fixed values belong in Parameter Override. The template maps 1K to the 1080P compatibility tier and supports only its listed sizes'
                  )}
                </span>
                <span className='block'>
                  {t(
                    'The template generates one image per request and uploads local references to public URLs (up to 16 images); configure image hosting first. Transparent backgrounds and masks are unsupported'
                  )}
                </span>
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      {!enabled && !draft?.trim() && (
        <FormField
          control={props.form.control}
          name='image_adapter'
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
