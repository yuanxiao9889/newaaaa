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
import { useQuery } from '@tanstack/react-query'
import type { Table } from '@tanstack/react-table'
import { ReceiptText } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTableBulkActions,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { useMediaQuery } from '@/hooks'
import { hasPermission } from '@/lib/admin-permissions'
import dayjs from '@/lib/dayjs'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getRechargeRecordConfig,
  getRechargeRecords,
  getSelfRechargeRecords,
} from '../api'
import type {
  InvoiceAction,
  RechargeRecord,
  RechargeRecordFilters,
} from '../types'
import { InvoiceHistoryDialog } from './invoice-history-dialog'
import { InvoiceMarkingDialog } from './invoice-marking-dialog'
import { useRechargeRecordColumns } from './recharge-record-columns'
import { RechargeRecordFilterBar } from './recharge-record-filters'

interface RechargeRecordsTableProps {
  mode: 'admin' | 'self'
  fixedUserId?: number
  embedded?: boolean
  onRecordsChanged?: () => void
}

interface ActionState {
  action: InvoiceAction
  records: RechargeRecord[]
}

const EMPTY_FILTERS: RechargeRecordFilters = {
  keyword: '',
  userId: '',
  source: '',
  status: '',
  invoiceStatus: '',
}

export function RechargeRecordsTable(props: RechargeRecordsTableProps) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [draftFilters, setDraftFilters] =
    useState<RechargeRecordFilters>(EMPTY_FILTERS)
  const [filters, setFilters] = useState<RechargeRecordFilters>(EMPTY_FILTERS)
  const [pageIndex, setPageIndex] = useState(0)
  const [pageSize, setPageSize] = useState(isMobile ? 10 : 20)
  const [actionState, setActionState] = useState<ActionState | null>(null)
  const [historyRecord, setHistoryRecord] = useState<RechargeRecord | null>(
    null
  )

  const configQuery = useQuery({
    queryKey: ['recharge-record-config'],
    queryFn: async () => {
      const response = await getRechargeRecordConfig()
      return requireServerSuccess(response).data
    },
    staleTime: 60_000,
  })

  const query = useQuery({
    queryKey: [
      'recharge-records',
      props.mode,
      user?.id,
      props.fixedUserId,
      filters,
      pageIndex,
      pageSize,
    ],
    enabled: configQuery.data?.feature_enabled === true,
    queryFn: async () => {
      const startTime = filters.startDate
        ? dayjs(filters.startDate).startOf('day').unix()
        : undefined
      const endTime = filters.endDate
        ? dayjs(filters.endDate).endOf('day').unix()
        : undefined
      const userId = props.fixedUserId ?? Number(filters.userId)
      const params = {
        page: pageIndex + 1,
        pageSize,
        keyword: filters.keyword,
        userId: Number.isFinite(userId) && userId > 0 ? userId : undefined,
        source: filters.source || undefined,
        status: filters.status || undefined,
        invoiceStatus: filters.invoiceStatus || undefined,
        startTime,
        endTime,
      }
      const response =
        props.mode === 'admin'
          ? await getRechargeRecords(params)
          : await getSelfRechargeRecords(params)
      const result = requireServerSuccess(response)
      return {
        items: result.data?.items ?? [],
        total: result.data?.total ?? 0,
      }
    },
  })

  const records = useMemo(() => query.data?.items ?? [], [query.data?.items])
  const canWrite =
    props.mode === 'admin' &&
    configQuery.data?.writes_enabled &&
    hasPermission(user, 'recharge_record', 'write') &&
    !query.isFetching &&
    !query.isError

  const handleAction = useCallback(
    (record: RechargeRecord, action: InvoiceAction | 'history') => {
      if (action === 'history') {
        setHistoryRecord(record)
        return
      }
      setActionState({ action, records: [record] })
    },
    []
  )

  const columns = useRechargeRecordColumns({
    mode: props.mode,
    canWrite: canWrite === true,
    onAction: handleAction,
  })

  const { table } = useDataTable({
    data: records,
    columns,
    enableRowSelection: canWrite === true,
    getRowId: (row) => `${row.source}:${row.source_id}`,
    pagination: { pageIndex, pageSize },
    onPaginationChange: (updater) => {
      const next =
        typeof updater === 'function'
          ? updater({ pageIndex, pageSize })
          : updater
      setPageIndex(next.pageIndex)
      setPageSize(next.pageSize)
    },
    manualPagination: true,
    totalCount: query.data?.total ?? 0,
  })

  useEffect(() => {
    table.resetRowSelection()
  }, [filters, pageIndex, pageSize, props.fixedUserId, table])

  useEffect(() => {
    if (!query.data) return
    const total = query.data.total
    const lastPage = Math.max(0, Math.ceil(total / pageSize) - 1)
    if (pageIndex > lastPage) setPageIndex(lastPage)
  }, [pageIndex, pageSize, query.data])

  const handleApply = () => {
    setPageIndex(0)
    setFilters({ ...draftFilters })
  }

  const handleReset = () => {
    setDraftFilters(EMPTY_FILTERS)
    setFilters(EMPTY_FILTERS)
    setPageIndex(0)
  }

  const handleChanged = () => {
    table.resetRowSelection()
    props.onRecordsChanged?.()
  }

  if (configQuery.isLoading) return <LoadingState />
  if (configQuery.isError) {
    return (
      <EmptyState
        title={t('Failed to load recharge records')}
        description={t('Please refresh the page and try again.')}
      />
    )
  }
  if (!configQuery.data?.feature_enabled) {
    return (
      <EmptyState
        title={t('Recharge records are not enabled')}
        description={t(
          'The administrator has disabled this feature. Existing invoice markings are retained.'
        )}
      />
    )
  }

  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load recharge records')}
        onRetry={() => void query.refetch()}
      />
    )
  }

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={query.isLoading}
        isFetching={query.isFetching}
        emptyTitle={t('No recharge records found')}
        emptyDescription={t(
          'Try changing the user, date range, source, or status filters.'
        )}
        skeletonKeyPrefix='recharge-records-skeleton'
        applyHeaderSize
        fixedHeight={!props.embedded}
        paginationInFooter={!props.embedded}
        showMobileBulkActions={props.mode === 'admin'}
        toolbar={
          <RechargeRecordFilterBar
            filters={draftFilters}
            onChange={(patch) =>
              setDraftFilters((current) => ({ ...current, ...patch }))
            }
            onApply={handleApply}
            onReset={handleReset}
            loading={query.isFetching}
            showUserFilter={props.mode === 'admin' && !props.fixedUserId}
          />
        }
        bulkActions={
          canWrite ? (
            <RechargeRecordBulkActions
              table={table}
              canWrite={canWrite === true}
              onSelect={(selected) =>
                setActionState({ action: 'mark', records: selected })
              }
            />
          ) : undefined
        }
      />

      {actionState && (
        <InvoiceMarkingDialog
          open={actionState != null}
          onOpenChange={(open) => {
            if (!open) setActionState(null)
          }}
          action={actionState.action}
          records={actionState.records}
          onCompleted={handleChanged}
        />
      )}

      <InvoiceHistoryDialog
        open={historyRecord != null}
        onOpenChange={(open) => {
          if (!open) setHistoryRecord(null)
        }}
        record={historyRecord}
      />
    </>
  )
}

interface RechargeRecordBulkActionsProps {
  table: Table<RechargeRecord>
  canWrite: boolean
  onSelect: (records: RechargeRecord[]) => void
}

function RechargeRecordBulkActions(props: RechargeRecordBulkActionsProps) {
  const { t } = useTranslation()
  const selected = props.table
    .getFilteredSelectedRowModel()
    .rows.map((row) => row.original)
  const sameUser = new Set(selected.map((record) => record.user_id)).size <= 1
  const allMarkable =
    selected.length > 0 &&
    selected.every(
      (record) => record.can_mark && record.invoice_status !== 'marked'
    )
  return (
    <DataTableBulkActions table={props.table} entityName={t('recharge record')}>
      <Button
        size='sm'
        className='gap-1.5'
        disabled={!props.canWrite || !sameUser || !allMarkable}
        onClick={() => props.onSelect(selected)}
      >
        <ReceiptText className='size-4' />
        {t('Mark selected as invoiced')}
      </Button>
    </DataTableBulkActions>
  )
}
