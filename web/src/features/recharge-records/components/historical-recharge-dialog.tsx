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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { DatePicker } from '@/components/date-picker'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import dayjs from '@/lib/dayjs'
import { formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  advanceRechargeImportJob,
  createRechargeImportJob,
  getRechargeImportReview,
  getRechargeRecordConfig,
  reviewHistoricalRecharge,
  type HistoricalRecharge,
} from '../api'

export function HistoricalRechargeDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [start, setStart] = useState<Date | undefined>(() =>
    dayjs().subtract(30, 'day').toDate()
  )
  const [end, setEnd] = useState<Date | undefined>(() => new Date())
  const [before, setBefore] = useState<number>()
  const [selected, setSelected] = useState<HistoricalRecharge | null>(null)
  const [userId, setUserId] = useState('')
  const [quota, setQuota] = useState('')
  const [note, setNote] = useState('')
  const [decision, setDecision] = useState<'confirm' | 'reject' | null>(null)
  const config = useQuery({
    queryKey: ['recharge-record-config'],
    enabled: props.open,
    queryFn: async () =>
      requireServerSuccess(await getRechargeRecordConfig()).data,
  })
  const query = useQuery({
    queryKey: ['recharge-import-review', before],
    enabled: props.open,
    queryFn: () => getRechargeImportReview(before),
  })
  const mutation = useMutation({
    mutationFn: async (
      action:
        | { type: 'start' }
        | { type: 'advance'; id: number }
        | { type: 'review'; reject: boolean }
    ) => {
      if (action.type === 'start') {
        if (!start || !end) return
        await createRechargeImportJob(
          dayjs(start).startOf('day').unix(),
          dayjs(end).endOf('day').unix()
        )
      } else if (action.type === 'advance') {
        await advanceRechargeImportJob(action.id)
      } else if (selected) {
        await reviewHistoricalRecharge(selected.id, {
          user_id: Number(userId),
          quota: Number(quota),
          note,
          reject: action.reject,
          confirmed: true,
        })
      }
    },
    onSuccess: async () => {
      setDecision(null)
      setSelected(null)
      await client.invalidateQueries({ queryKey: ['recharge-import-review'] })
      await client.invalidateQueries({ queryKey: ['recharge-records'] })
    },
  })
  const disabled = mutation.isPending || config.data?.imports_enabled !== true
  const columns: StaticDataTableColumn<HistoricalRecharge>[] = [
    {
      id: 'source',
      header: t('Order / record number'),
      cell: (row) => (
        <span className='font-mono text-xs'>{row.source_key}</span>
      ),
    },
    {
      id: 'date',
      header: t('Time'),
      cell: (row) => formatTimestampToDate(row.occurred_at),
    },
    {
      id: 'user',
      header: t('User ID'),
      cell: (row) => row.user_id || t('Unconfirmed'),
    },
    {
      id: 'review',
      header: t('Actions'),
      cell: (row) => (
        <Button
          variant='outline'
          size='sm'
          disabled={disabled}
          onClick={() => {
            setSelected(row)
            setUserId(row.user_id ? String(row.user_id) : '')
            setQuota(row.quota ? String(row.quota) : '')
            setNote('')
          }}
        >
          {t('Review')}
        </Button>
      ),
    },
  ]
  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={props.onOpenChange}
        title={t('Review historical recharges')}
        contentClassName='sm:max-w-4xl'
        description={t(
          'Scan a batch, verify ownership and quota, then confirm. Confirmation adds a historical record without changing the balance.'
        )}
      >
        {config.data?.imports_enabled === false && (
          <p className='text-muted-foreground'>
            {t('Historical recharge import is disabled')}
          </p>
        )}
        <div className='flex flex-wrap gap-2'>
          <DatePicker
            selected={start}
            onSelect={setStart}
            aria-label={t('Start date')}
          />
          <DatePicker
            selected={end}
            onSelect={setEnd}
            aria-label={t('End date')}
          />
          <Button
            disabled={disabled || !start || !end || start > end}
            onClick={() => mutation.mutate({ type: 'start' })}
          >
            {t('Create review task')}
          </Button>
        </div>
        {query.isLoading && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.data && (
          <div className='space-y-4'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Each scan processes up to 100 logs. Close this dialog to pause; resume the task later.'
              )}
            </p>
            <div className='max-h-40 space-y-2 overflow-auto'>
              {query.data.jobs.map((job) => (
                <div
                  key={job.id}
                  className='flex flex-wrap items-center justify-between gap-2 text-sm'
                >
                  <span>
                    #{job.id} · {formatTimestampToDate(job.start_time)} –{' '}
                    {formatTimestampToDate(job.end_time)}
                  </span>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={disabled || job.done}
                    onClick={() =>
                      mutation.mutate({ type: 'advance', id: job.id })
                    }
                  >
                    {job.done ? t('Completed') : t('Scan next batch')}
                  </Button>
                </div>
              ))}
            </div>
            <StaticDataTable
              columns={columns}
              data={query.data.items}
              getRowKey={(row) => row.id}
              emptyContent={t('No records awaiting review')}
            />
            <div className='flex gap-2'>
              {before && (
                <Button variant='outline' onClick={() => setBefore(undefined)}>
                  {t('Back')}
                </Button>
              )}
              {query.data.next_before_id > 0 && (
                <Button
                  variant='outline'
                  onClick={() => setBefore(query.data.next_before_id)}
                >
                  {t('Next page')}
                </Button>
              )}
            </div>
          </div>
        )}
      </Dialog>
      <Dialog
        open={selected != null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setSelected(null)
        }}
        title={t('Review historical recharge')}
        description={selected?.source_key}
        footer={
          <div className='flex gap-2'>
            <Button
              variant='outline'
              disabled={disabled || !note.trim()}
              onClick={() => setDecision('reject')}
            >
              {t('Reject')}
            </Button>
            <Button
              disabled={
                disabled ||
                !note.trim() ||
                !Number.isSafeInteger(Number(userId)) ||
                Number(userId) <= 0 ||
                !Number.isSafeInteger(Number(quota)) ||
                Number(quota) <= 0
              }
              onClick={() => setDecision('confirm')}
            >
              {t('Confirm')}
            </Button>
          </div>
        }
      >
        <p className='text-sm break-words whitespace-pre-wrap'>
          {selected?.evidence}
        </p>
        <div className='space-y-2'>
          <Label htmlFor='historical-user'>{t('Verified user ID')}</Label>
          <Input
            id='historical-user'
            type='number'
            value={userId}
            onChange={(event) => setUserId(event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='historical-quota'>{t('Quota units')}</Label>
          <Input
            id='historical-quota'
            type='number'
            value={quota}
            onChange={(event) => setQuota(event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='historical-note'>{t('Verification note')}</Label>
          <Textarea
            id='historical-note'
            value={note}
            maxLength={2000}
            onChange={(event) => setNote(event.target.value)}
          />
        </div>
      </Dialog>
      <ConfirmDialog
        open={decision != null}
        onOpenChange={(open) => {
          if (!open) setDecision(null)
        }}
        title={
          decision === 'reject' ? t('Reject') : t('Confirm historical recharge')
        }
        desc={t(
          'Verify this source record, user and quota before continuing. This does not credit the wallet or mark an invoice.'
        )}
        isLoading={mutation.isPending}
        handleConfirm={() =>
          mutation.mutate({ type: 'review', reject: decision === 'reject' })
        }
      >
        <p className='text-sm break-words'>
          {selected?.source_key} · {t('User ID')}: {userId} · {t('Quota units')}
          : {quota}
        </p>
      </ConfirmDialog>
    </>
  )
}
