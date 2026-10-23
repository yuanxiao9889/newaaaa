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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SettingsCard } from '@/features/system-settings/components/settings-card'

import { usePolicyEditor } from '../hooks/use-policy-editor'
import type { BridgeManagementData } from '../types'
import { PolicyHistory } from './history'
import { PolicyFields } from './policy-fields'

export function PolicyEditor(props: {
  data: BridgeManagementData
  canEdit: boolean
}) {
  const { t } = useTranslation()
  const {
    form,
    baseVersion,
    save,
    reload,
    busy,
    outdated,
    replacement,
    setReplacement,
    replaceDraft,
  } = usePolicyEditor(props.data)
  const disabled = !props.canEdit || busy

  return (
    <>
      <Form {...form}>
        <form
          className='space-y-4'
          noValidate
          onSubmit={form.handleSubmit((values) => {
            if (!props.canEdit || busy || outdated) return
            save.mutate({ base_version: baseVersion, ...values })
          })}
        >
          {!props.canEdit && (
            <Alert>
              <AlertDescription>
                {t(
                  'Administrators can view this page. Only the super administrator can save bridge policies.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {outdated && (
            <Alert variant='destructive'>
              <AlertTitle>{t('The saved policy has changed')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Your draft is preserved. Reload the latest saved policy before editing and saving again; this page will not overwrite another administrator’s revision.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <SettingsCard title={t('Bridge admission and fixed routes')}>
            <div className='space-y-5'>
              <FormField
                control={form.control}
                name='policy.enabled'
                render={({ field }) => (
                  <FormItem className='flex items-center justify-between gap-4'>
                    <div>
                      <FormLabel>
                        {t('Allow bridge for new client operations')}
                      </FormLabel>
                      <FormDescription>
                        {t(
                          'Disabling admission affects new operations after clients fetch the policy. It does not cancel or drain running tasks.'
                        )}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={disabled}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className='grid gap-4 md:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='policy.bridge_base_url'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Bridge API URL')}</FormLabel>
                      <FormControl>
                        <Input {...field} readOnly />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='policy.origin_base_url'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Original API URL')}</FormLabel>
                      <FormControl>
                        <Input {...field} readOnly />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </div>
          </SettingsCard>
          <PolicyFields fields={props.data.fields} disabled={disabled} />
          <SettingsCard title={t('Save a policy revision')}>
            <div className='space-y-4'>
              <p className='text-muted-foreground text-sm'>
                {t('Editing base version: v{{version}}', {
                  version: baseVersion,
                })}
              </p>
              {props.canEdit && (
                <FormField
                  control={form.control}
                  name='reason'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Change reason')}</FormLabel>
                      <FormControl>
                        <Textarea {...field} disabled={busy} />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Explain the operational reason, up to 500 characters. Do not include credentials.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
              <div className='flex flex-wrap gap-2'>
                {props.canEdit && (
                  <Button
                    type='submit'
                    disabled={busy || outdated || !form.formState.isDirty}
                  >
                    {save.isPending ? t('Saving...') : t('Save bridge policy')}
                  </Button>
                )}
                <Button
                  type='button'
                  variant='outline'
                  disabled={busy}
                  onClick={() => {
                    if (form.formState.isDirty) setReplacement('reload')
                    else replaceDraft('reload')
                  }}
                >
                  {reload.isPending
                    ? t('Loading...')
                    : t('Reload saved policy')}
                </Button>
              </div>
            </div>
          </SettingsCard>
        </form>
      </Form>
      <PolicyHistory
        history={props.data.history}
        canEdit={props.canEdit}
        disabled={busy || outdated}
        onRestoreDraft={(revision) => {
          if (form.formState.isDirty) setReplacement(revision)
          else replaceDraft(revision)
        }}
      />
      <ConfirmDialog
        open={replacement !== null}
        onOpenChange={(open) => {
          if (!open) setReplacement(null)
        }}
        title={t('Replace the current draft?')}
        desc={t(
          'Unsaved edits will be replaced. No policy is saved by this action.'
        )}
        confirmText={t('Replace draft')}
        handleConfirm={() => {
          if (replacement) replaceDraft(replacement)
        }}
      />
    </>
  )
}
