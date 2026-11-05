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
import { useInfiniteQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getRechargeInvoiceHistory } from '../api'
import { INVOICE_ACTION_LABEL_KEYS, getInvoiceStatusConfig } from '../constants'
import type { InvoiceHistoryItem, RechargeRecord } from '../types'

interface InvoiceHistoryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: RechargeRecord | null
}

export function InvoiceHistoryDialog(props: InvoiceHistoryDialogProps) {
  const { t } = useTranslation()
  const query = useInfiniteQuery({
    queryKey: [
      'recharge-record-history',
      props.record?.source,
      props.record?.source_id,
    ],
    enabled: props.open && props.record != null,
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last: InvoiceHistoryItem[]) =>
      last.length === 100 ? last.at(-1)?.id : undefined,
    queryFn: async ({ pageParam }) => {
      if (!props.record) return []
      const response = await getRechargeInvoiceHistory(
        props.record.source,
        props.record.source_id,
        pageParam
      )
      const result = requireServerSuccess(response)
      return result.data ?? []
    },
  })
  const data = query.data?.pages.flat() ?? []

  let content: ReactNode
  if (query.isError) {
    content = <ErrorState onRetry={() => void query.refetch()} />
  } else if (query.isLoading) {
    content = <LoadingState />
  } else if (data && data.length > 0) {
    content = (
      <div className='space-y-3'>
        {data.map((item) => (
          <HistoryEntry key={item.id} item={item} />
        ))}
        {query.hasNextPage && (
          <Button
            variant='outline'
            disabled={query.isFetchingNextPage}
            onClick={() => void query.fetchNextPage()}
          >
            {t('Load more')}
          </Button>
        )}
      </div>
    )
  } else {
    content = (
      <p className='text-muted-foreground py-8 text-center text-sm'>
        {t('No invoice history found')}
      </p>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Invoice history')}
      description={
        props.record
          ? `${props.record.record_no} · ${t('User')} ${props.record.user_id}`
          : undefined
      }
      contentClassName='max-w-3xl'
    >
      {content}
    </Dialog>
  )
}

function HistoryEntry(props: { item: InvoiceHistoryItem }) {
  const { t } = useTranslation()
  const before = getInvoiceStatusConfig(props.item.before.status)
  const after = getInvoiceStatusConfig(props.item.after.status)
  return (
    <div className='space-y-2 rounded-lg border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <StatusBadge
            label={t(INVOICE_ACTION_LABEL_KEYS[props.item.action])}
            variant='info'
            copyable={false}
          />
          <span className='text-muted-foreground text-xs'>
            {formatTimestampToDate(props.item.created_at)}
          </span>
        </div>
        <span className='text-muted-foreground text-xs'>
          {t('Operator')}: {props.item.operator_name} (#{props.item.operator_id}
          )
        </span>
      </div>
      {props.item.reason && (
        <p className='text-sm'>
          <span className='text-muted-foreground'>{t('Reason')}: </span>
          {props.item.reason}
        </p>
      )}
      <div className='flex flex-wrap items-center gap-2 text-xs'>
        <StatusBadge
          label={t(before.labelKey)}
          variant={before.variant}
          copyable={false}
        />
        <span aria-hidden='true'>→</span>
        <StatusBadge
          label={t(after.labelKey)}
          variant={after.variant}
          copyable={false}
        />
        <span className='text-muted-foreground'>
          {t('Version')} {props.item.before.version} →{' '}
          {props.item.after.version}
        </span>
      </div>
      {(props.item.before.invoice_no ||
        props.item.after.invoice_no ||
        props.item.before.invoice_date ||
        props.item.after.invoice_date) && (
        <div className='text-muted-foreground grid gap-1 text-xs sm:grid-cols-2'>
          <span>
            {t('Invoice number')}:{' '}
            {props.item.before.invoice_no || t('Not recorded')} →{' '}
            {props.item.after.invoice_no || t('Not recorded')}
          </span>
          <span>
            {t('Actual invoice date')}:{' '}
            {props.item.before.invoice_date || t('Not recorded')} →{' '}
            {props.item.after.invoice_date || t('Not recorded')}
          </span>
        </div>
      )}
      {(props.item.after.internal_note ||
        props.item.after.verification_note) && (
        <div className='text-muted-foreground space-y-1 text-xs'>
          {props.item.after.internal_note && (
            <p>
              {t('Internal note')}: {props.item.after.internal_note}
            </p>
          )}
          {props.item.after.verification_note && (
            <p>
              {t('Verification note')}: {props.item.after.verification_note}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
