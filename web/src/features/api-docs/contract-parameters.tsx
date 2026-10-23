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

import { StaticDataTable } from '@/components/data-table'

export type ContractParameter = {
  name: string
  type: string
  required: boolean
  description: string
  values?: string
}

export function ContractParameters({
  fields,
}: {
  fields: ContractParameter[]
}) {
  const { t } = useTranslation()
  return (
    <StaticDataTable
      className='rounded-xl border'
      tableClassName='min-w-[640px]'
      data={fields}
      getRowKey={(field) => field.name}
      columns={[
        {
          id: 'name',
          header: t('apiDocs.video.field'),
          cell: (field) => <code>{field.name}</code>,
        },
        {
          id: 'type',
          header: t('apiDocs.video.type'),
          cell: (field) => <code>{field.type}</code>,
        },
        {
          id: 'required',
          header: t('apiDocs.video.requirement'),
          cell: (field) =>
            t(
              field.required
                ? 'apiDocs.video.required'
                : 'apiDocs.video.optional'
            ),
        },
        {
          id: 'description',
          header: t('apiDocs.video.description'),
          cell: (field) => (
            <div className='min-w-56 space-y-2 leading-6 whitespace-normal'>
              <span>{t(field.description)}</span>
              {field.values && (
                <code className='text-muted-foreground block text-xs break-words'>
                  {field.values}
                </code>
              )}
            </div>
          ),
        },
      ]}
    />
  )
}
