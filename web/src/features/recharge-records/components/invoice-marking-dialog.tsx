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
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DatePicker } from '@/components/date-picker'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getInvoiceMarkingConflicts,
  markRechargeInvoices,
  type InvoiceMarkingConflict,
} from '../api'
import { INVOICE_ACTION_LABEL_KEYS, getSourceConfig } from '../constants'
import {
  buildIdempotencyKey,
  getRecordDisplayName,
  getRecordPaidAmount,
} from '../lib/format'
import type { InvoiceAction, RechargeRecord } from '../types'

const CONFLICT_REASON_KEYS: Record<string, string> = {
  not_markable: 'Record is not in a markable state',
  version_conflict: 'Invoice version conflict. Refresh and try again.',
  not_marked: 'Invoice is not marked',
  already_marked: 'Invoice is already marked',
}

interface InvoiceMarkingDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  action: InvoiceAction
  records: RechargeRecord[]
  onCompleted?: () => void
}

export function InvoiceMarkingDialog(props: InvoiceMarkingDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [invoiceNo, setInvoiceNo] = useState('')
  const [invoiceDate, setInvoiceDate] = useState<Date | undefined>()
  const [internalNote, setInternalNote] = useState('')
  const [verificationNote, setVerificationNote] = useState('')
  const [receiptConfirmed, setReceiptConfirmed] = useState(false)
  const [reason, setReason] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [conflicts, setConflicts] = useState<InvoiceMarkingConflict[]>([])
  const [idempotencyKey, setIdempotencyKey] = useState('')

  const requiresVerification = props.records.some(
    (record) => record.requires_receipt_verification
  )
  const requiresReason = props.action === 'correct' || props.action === 'revoke'

  useEffect(() => {
    if (!props.open) return
    const first = props.records[0]
    setInvoiceNo(props.action === 'mark' ? '' : (first?.invoice_no ?? ''))
    setInvoiceDate(
      first?.invoice_date
        ? dayjs(first.invoice_date, 'YYYY-MM-DD').toDate()
        : undefined
    )
    setInternalNote(props.action === 'mark' ? '' : (first?.internal_note ?? ''))
    setVerificationNote(
      props.action === 'mark' ? '' : (first?.verification_note ?? '')
    )
    setReceiptConfirmed(false)
    setReason('')
    setConflicts([])
    setConfirmOpen(false)
    setIdempotencyKey(buildIdempotencyKey())
  }, [props.action, props.open, props.records])

  const formInvalid =
    props.records.length === 0 ||
    (requiresReason && reason.trim() === '') ||
    (requiresVerification &&
      props.action !== 'revoke' &&
      (!receiptConfirmed || verificationNote.trim() === ''))

  const handleSubmit = async () => {
    if (formInvalid || submitting) return
    setSubmitting(true)
    setConflicts([])
    try {
      const response = await markRechargeInvoices(
        {
          action: props.action,
          records: props.records.map((record) => ({
            source: record.source,
            id: record.source_id,
            expected_version: record.invoice_version,
          })),
          invoice_no: invoiceNo.trim(),
          invoice_date: invoiceDate
            ? dayjs(invoiceDate).format('YYYY-MM-DD')
            : '',
          internal_note: internalNote.trim(),
          verification_note: verificationNote.trim(),
          reason: reason.trim(),
          receipt_confirmed: receiptConfirmed,
        },
        idempotencyKey
      )
      requireServerSuccess(response)
      await queryClient.invalidateQueries({ queryKey: ['recharge-records'] })
      await queryClient.invalidateQueries({
        queryKey: ['recharge-record-history'],
      })
      toast.success(
        t(
          props.action === 'revoke'
            ? 'Invoice marking revoked'
            : 'Invoice status updated'
        )
      )
      setConfirmOpen(false)
      props.onCompleted?.()
      props.onOpenChange(false)
    } catch (error) {
      const items = getInvoiceMarkingConflicts(error)
      setConflicts(items)
      handleServerError(error, t('Failed to update invoice status'))
    } finally {
      setSubmitting(false)
    }
  }

  const actionLabel = t(INVOICE_ACTION_LABEL_KEYS[props.action])

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={props.onOpenChange}
        title={actionLabel}
        description={t(
          'This changes invoice status only. Balances, recharge amounts, and order status are not modified.'
        )}
        contentClassName='max-w-3xl'
        footer={
          <div className='flex w-full flex-col gap-2 sm:flex-row sm:justify-end'>
            <Button
              variant='outline'
              onClick={() => props.onOpenChange(false)}
              disabled={submitting}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => setConfirmOpen(true)}
              disabled={formInvalid || submitting}
            >
              {t('Review and submit')}
            </Button>
          </div>
        }
      >
        <div className='space-y-5'>
          <div className='space-y-2'>
            <div className='flex items-center justify-between gap-2'>
              <Label>{t('Records to update')}</Label>
              <StatusBadge
                label={t('{{count}} records', {
                  count: props.records.length,
                })}
                variant='neutral'
                copyable={false}
              />
            </div>
            <div className='max-h-48 space-y-2 overflow-y-auto rounded-md border p-2'>
              {props.records.map((record) => (
                <RecordSummary
                  key={`${record.source}:${record.source_id}`}
                  record={record}
                />
              ))}
            </div>
          </div>

          {conflicts.length > 0 && (
            <Alert variant='destructive'>
              <AlertTriangle />
              <AlertDescription>
                <p className='font-medium'>{t('No records were changed')}</p>
                <ul className='mt-1 list-disc space-y-1 ps-4 text-xs'>
                  {conflicts.map((conflict) => (
                    <li key={`${conflict.source}:${conflict.id}`}>
                      {t('Recharge record')} #{conflict.id}:{' '}
                      {t(
                        CONFLICT_REASON_KEYS[conflict.reason] ?? conflict.reason
                      )}
                    </li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}

          {props.action !== 'revoke' && (
            <div className='grid gap-4 sm:grid-cols-2'>
              <div className='space-y-2'>
                <Label htmlFor='recharge-invoice-no'>
                  {t('Invoice number (optional)')}
                </Label>
                <Input
                  id='recharge-invoice-no'
                  value={invoiceNo}
                  maxLength={128}
                  onChange={(event) => setInvoiceNo(event.target.value)}
                  placeholder={t('Not recorded')}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Actual invoice date (optional)')}</Label>
                <div className='flex items-center gap-1.5'>
                  <DatePicker
                    selected={invoiceDate}
                    onSelect={setInvoiceDate}
                    placeholder={t('Not recorded')}
                    aria-label={t('Actual invoice date (optional)')}
                    className='w-full flex-1'
                  />
                  {invoiceDate && (
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon-sm'
                      onClick={() => setInvoiceDate(undefined)}
                      aria-label={t('Clear')}
                      title={t('Clear')}
                    >
                      <X />
                    </Button>
                  )}
                </div>
              </div>
            </div>
          )}

          <div className='space-y-2'>
            <Label htmlFor='recharge-invoice-note'>{t('Internal note')}</Label>
            <Textarea
              id='recharge-invoice-note'
              value={internalNote}
              maxLength={2000}
              onChange={(event) => setInternalNote(event.target.value)}
              placeholder={t('Only visible to administrators')}
              rows={2}
            />
          </div>

          {requiresVerification && props.action !== 'revoke' && (
            <div className='space-y-3 rounded-md border border-amber-500/40 bg-amber-500/5 p-3'>
              <div className='flex items-start gap-2'>
                <Checkbox
                  id='recharge-receipt-confirmed'
                  checked={receiptConfirmed}
                  onCheckedChange={(value) => setReceiptConfirmed(!!value)}
                />
                <Label
                  htmlFor='recharge-receipt-confirmed'
                  className='leading-relaxed font-normal'
                >
                  {t(
                    'I verified the corresponding payment receipt and the invoice facts.'
                  )}
                </Label>
              </div>
              <div className='space-y-2'>
                <Label htmlFor='recharge-verification-note'>
                  {t('Verification note')}
                </Label>
                <Textarea
                  id='recharge-verification-note'
                  value={verificationNote}
                  maxLength={2000}
                  onChange={(event) => setVerificationNote(event.target.value)}
                  placeholder={t(
                    'Describe the evidence used to verify this marking'
                  )}
                  rows={2}
                />
              </div>
            </div>
          )}

          {props.action === 'revoke' && (
            <p className='text-muted-foreground text-sm'>
              {t('Revoking this marking does not void the actual invoice.')}
            </p>
          )}

          {requiresReason && (
            <div className='space-y-2'>
              <Label htmlFor='recharge-invoice-reason'>{t('Reason')}</Label>
              <Textarea
                id='recharge-invoice-reason'
                value={reason}
                maxLength={1000}
                onChange={(event) => setReason(event.target.value)}
                placeholder={t('Required for correction or revocation')}
                rows={2}
              />
            </div>
          )}
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={actionLabel}
        desc={t(
          '{{action}} for {{count}} record(s)? This updates invoice status only and is recorded in the invoice history.',
          { action: actionLabel, count: props.records.length }
        )}
        confirmText={actionLabel}
        isLoading={submitting}
        handleConfirm={handleSubmit}
        destructive={props.action === 'revoke'}
      >
        <div className='max-h-40 space-y-1 overflow-y-auto rounded-md border p-2 text-xs'>
          {props.records.map((record) => (
            <div
              key={`${record.source}:${record.source_id}`}
              className='flex items-center justify-between gap-2'
            >
              <span className='font-mono'>{record.record_no}</span>
              <span className='text-muted-foreground'>
                {getRecordDisplayName(record)}
              </span>
            </div>
          ))}
        </div>
      </ConfirmDialog>
    </>
  )
}

function RecordSummary(props: { record: RechargeRecord }) {
  const { t, i18n } = useTranslation()
  const source = getSourceConfig(props.record.source_kind)
  const paid = getRecordPaidAmount(
    props.record,
    toIntlLocale(i18n.resolvedLanguage || i18n.language)
  )
  return (
    <div className='bg-muted/30 flex flex-wrap items-center justify-between gap-2 rounded border px-2 py-1.5 text-xs'>
      <div className='flex min-w-0 items-center gap-2'>
        <StatusBadge
          label={t(source.labelKey)}
          variant={source.variant}
          copyable={false}
        />
        <span className='truncate font-mono'>{props.record.record_no}</span>
        <span className='text-muted-foreground truncate'>
          {getRecordDisplayName(props.record)}
        </span>
      </div>
      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground'>
          {paid ?? t('Actual payment not recorded')}
        </span>
        <span className='font-mono'>v{props.record.invoice_version}</span>
      </div>
    </div>
  )
}
