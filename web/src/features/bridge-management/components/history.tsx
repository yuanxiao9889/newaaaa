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

import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import dayjs from '@/lib/dayjs'
import { formatTimestamp } from '@/lib/format'

import type { PolicyRevision } from '../types'

export function PolicyHistory(props: {
  history: PolicyRevision[]
  canEdit: boolean
  disabled: boolean
  onRestoreDraft: (revision: PolicyRevision) => void
}) {
  const { t } = useTranslation()
  return (
    <SettingsCard
      title={t('Policy version history')}
      description={t(
        'Loading a historical policy only changes the draft. Review it and save with the current base version to create a new revision.'
      )}
    >
      {props.history.length === 0 && (
        <EmptyState title={t('No policy revisions yet')} className='min-h-24' />
      )}
      <ol className='space-y-4'>
        {props.history.map((revision) => (
          <li
            key={revision.version}
            className='flex flex-wrap items-start justify-between gap-3'
          >
            <div className='min-w-0 space-y-1 text-sm'>
              <p className='font-medium'>
                {t('Version {{version}}', { version: revision.version })}
              </p>
              <p className='break-words'>{revision.reason}</p>
              <p className='text-muted-foreground'>
                {typeof revision.changed_at === 'number'
                  ? formatTimestamp(revision.changed_at)
                  : dayjs(revision.changed_at).format('YYYY-MM-DD HH:mm:ss')}
                {' · '}
                {t('Changed by {{user}}', { user: revision.changed_by })}
              </p>
            </div>
            {props.canEdit && (
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={props.disabled}
                onClick={() => props.onRestoreDraft(revision)}
              >
                {t('Load v{{version}} as draft', { version: revision.version })}
              </Button>
            )}
          </li>
        ))}
      </ol>
    </SettingsCard>
  )
}
