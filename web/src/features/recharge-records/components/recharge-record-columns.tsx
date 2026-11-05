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
import type { ColumnDef } from '@tanstack/react-table'
import { History, PencilLine, ReceiptText, Undo2 } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTableRowActionMenu } from '@/components/data-table'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuota, formatTimestampToDate } from '@/lib/format'

import {
  INVOICE_ACTION_LABEL_KEYS,
  getInvoiceStatusConfig,
  getSourceConfig,
  getStatusConfig,
} from '../constants'
import { getRecordDisplayName, getRecordPaidAmount } from '../lib/format'
import type { InvoiceAction, RechargeRecord } from '../types'

interface UseRechargeRecordColumnsOptions {
  mode: 'admin' | 'self'
  canWrite: boolean
  onAction: (record: RechargeRecord, action: InvoiceAction | 'history') => void
}

export function useRechargeRecordColumns(
  props: UseRechargeRecordColumnsOptions
): ColumnDef<RechargeRecord>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  return useMemo<ColumnDef<RechargeRecord>[]>(() => {
    const columns: ColumnDef<RechargeRecord>[] = []

    if (props.mode === 'admin' && props.canWrite) {
      columns.push({
        id: 'select',
        header: ({ table }) => (
          <Checkbox
            checked={table.getIsAllPageRowsSelected()}
            indeterminate={table.getIsSomePageRowsSelected()}
            onCheckedChange={(value) =>
              table.toggleAllPageRowsSelected(!!value)
            }
            aria-label={t('Select all')}
            className='translate-y-[2px]'
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={t('Select row')}
            className='translate-y-[2px]'
          />
        ),
        enableSorting: false,
        enableHiding: false,
        size: 40,
      })
    }

    columns.push(
      {
        accessorKey: 'recharge_time',
        header: t('Recharge time'),
        cell: ({ row }) => (
          <span className='font-mono text-sm whitespace-nowrap'>
            {formatTimestampToDate(row.original.recharge_time)}
          </span>
        ),
        size: 180,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'record_no',
        header: t('Order / record number'),
        cell: ({ row }) => (
          <div className='flex min-w-[150px] flex-col gap-1'>
            <StatusBadge
              label={row.original.record_no}
              variant='neutral'
              type='text'
              copyText={row.original.record_no}
              className='font-mono text-xs'
            />
            {row.original.source === 'redemption' && (
              <span className='text-muted-foreground text-xs'>
                {t('Redemption record')}
              </span>
            )}
          </div>
        ),
        size: 230,
      }
    )

    if (props.mode === 'admin') {
      columns.push({
        accessorKey: 'username',
        header: t('User'),
        cell: ({ row }) => (
          <div className='flex min-w-[130px] flex-col gap-0.5'>
            <LongText className='max-w-[180px] text-sm font-normal'>
              {getRecordDisplayName(row.original)}
            </LongText>
            <span className='text-muted-foreground text-xs'>
              ID: {row.original.user_id}
            </span>
          </div>
        ),
        size: 180,
      })
    }

    columns.push(
      {
        accessorKey: 'source_kind',
        header: t('Source'),
        cell: ({ row }) => {
          const config = getSourceConfig(row.original.source_kind)
          return (
            <StatusBadge
              label={t(config.labelKey)}
              variant={config.variant}
              copyable={false}
            />
          )
        },
        size: 140,
        meta: { mobileBadge: true },
      },
      {
        accessorKey: 'credited_quota',
        header: t('Credited quota'),
        cell: ({ row }) => (
          <span className='font-mono text-sm whitespace-nowrap'>
            {formatQuota(row.original.credited_quota)}
          </span>
        ),
        size: 130,
      },
      {
        accessorKey: 'paid_amount',
        header: t('Actual paid amount'),
        cell: ({ row }) => {
          const paid = getRecordPaidAmount(row.original, locale)
          if (paid) {
            return (
              <span className='font-mono text-sm whitespace-nowrap'>
                {paid}
              </span>
            )
          }
          return (
            <Tooltip>
              <TooltipTrigger
                render={
                  <StatusBadge
                    label={t('Actual payment not recorded')}
                    variant='warning'
                    copyable={false}
                  />
                }
              />
              <TooltipContent className='max-w-[280px]'>
                {t(
                  'The actual paid amount cannot be confirmed for this record. Its credited quota is not used as a substitute.'
                )}
              </TooltipContent>
            </Tooltip>
          )
        },
        size: 150,
      },
      {
        accessorKey: 'status',
        header: t('Recharge status'),
        cell: ({ row }) => {
          const config = getStatusConfig(row.original.status)
          return (
            <StatusBadge
              label={t(config.labelKey)}
              variant={config.variant}
              copyable={false}
            />
          )
        },
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        accessorKey: 'invoice_status',
        header: t('Invoice status'),
        cell: ({ row }) => {
          const record = row.original
          const config = getInvoiceStatusConfig(record.invoice_status)
          return (
            <div className='flex min-w-[130px] flex-col gap-1'>
              {record.invoice_status === 'unmarked' ? (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <StatusBadge
                        label={t(config.labelKey)}
                        variant={config.variant}
                        copyable={false}
                      />
                    }
                  />
                  <TooltipContent className='max-w-[280px]'>
                    {t(
                      'Unmarked is the default system state and does not mean the payment was not invoiced.'
                    )}
                  </TooltipContent>
                </Tooltip>
              ) : (
                <StatusBadge
                  label={t(config.labelKey)}
                  variant={config.variant}
                  copyable={false}
                />
              )}
              {record.invoice_status === 'marked' && (
                <div className='text-muted-foreground space-y-0.5 text-xs'>
                  <div>
                    {t('Invoice number')}:{' '}
                    {record.invoice_no || t('Not recorded')}
                  </div>
                  <div>
                    {t('Actual invoice date')}:{' '}
                    {record.invoice_date || t('Not recorded')}
                  </div>
                </div>
              )}
            </div>
          )
        },
        size: 160,
      }
    )

    if (props.mode === 'admin') {
      columns.push({
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => {
          const record = row.original
          const canMark =
            props.canWrite &&
            record.can_mark &&
            record.invoice_status !== 'marked'
          const canCorrect =
            props.canWrite && record.invoice_status === 'marked'
          const canRevoke = props.canWrite && record.invoice_status === 'marked'
          return (
            <DataTableRowActionMenu ariaLabel={t('Open menu')} modal={false}>
              {props.canWrite && (
                <>
                  <DropdownMenuItem
                    onSelect={(event) => {
                      event.preventDefault()
                      props.onAction(record, 'mark')
                    }}
                    disabled={!canMark}
                  >
                    {t(INVOICE_ACTION_LABEL_KEYS.mark)}
                    <DropdownMenuShortcut>
                      <ReceiptText size={16} />
                    </DropdownMenuShortcut>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={(event) => {
                      event.preventDefault()
                      props.onAction(record, 'correct')
                    }}
                    disabled={!canCorrect}
                  >
                    {t(INVOICE_ACTION_LABEL_KEYS.correct)}
                    <DropdownMenuShortcut>
                      <PencilLine size={16} />
                    </DropdownMenuShortcut>
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onSelect={(event) => {
                      event.preventDefault()
                      props.onAction(record, 'revoke')
                    }}
                    disabled={!canRevoke}
                  >
                    {t(INVOICE_ACTION_LABEL_KEYS.revoke)}
                    <DropdownMenuShortcut>
                      <Undo2 size={16} />
                    </DropdownMenuShortcut>
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                </>
              )}
              <DropdownMenuItem
                onSelect={(event) => {
                  event.preventDefault()
                  props.onAction(record, 'history')
                }}
              >
                {t('Invoice history')}
                <DropdownMenuShortcut>
                  <History size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            </DataTableRowActionMenu>
          )
        },
        meta: { pinned: 'right' as const },
      })
    }

    return columns
  }, [locale, props, t])
}
