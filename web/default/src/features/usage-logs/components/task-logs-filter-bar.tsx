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
import { useState, useEffect, useCallback } from 'react'
import { useQueryClient, useIsFetching } from '@tanstack/react-query'
import { useNavigate, getRouteApi } from '@tanstack/react-router'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { useIsAdmin } from '@/hooks/use-admin'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { TASK_STATUS_ALL_VALUE, TASK_STATUS_FILTERS } from '../constants'
import { buildSearchParams } from '../lib/filter'
import { getDefaultTimeRange } from '../lib/utils'
import type { DrawingLogFilters, LogCategory, TaskLogFilters } from '../types'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from './logs-filter-toolbar'

const route = getRouteApi('/_authenticated/usage-logs/$section')

type TaskLikeLogCategory = Extract<LogCategory, 'drawing' | 'task'>
type TaskLogsFilters = DrawingLogFilters | TaskLogFilters
type TaskLogsFilterField = keyof DrawingLogFilters | keyof TaskLogFilters
type TaskStatusFilterValue = (typeof TASK_STATUS_FILTERS)[number]['value']

interface TaskLogsFilterBarProps<TData> {
  table: Table<TData>
  logCategory: TaskLikeLogCategory
}

function getFilterValue(
  filters: TaskLogsFilters,
  logCategory: TaskLikeLogCategory
): string {
  if (logCategory === 'drawing') {
    return (filters as DrawingLogFilters).mjId || ''
  }
  return (filters as TaskLogFilters).taskId || ''
}

function setFilterValue(
  filters: TaskLogsFilters,
  logCategory: TaskLikeLogCategory,
  value: string
): TaskLogsFilters {
  if (logCategory === 'drawing') {
    return { ...filters, mjId: value }
  }
  return { ...filters, taskId: value }
}

function isTaskStatusFilterValue(
  value: string
): value is TaskStatusFilterValue {
  return TASK_STATUS_FILTERS.some((status) => status.value === value)
}

export function TaskLogsFilterBar<TData>(props: TaskLogsFilterBarProps<TData>) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const searchParams = route.useSearch()
  const isAdmin = useIsAdmin()
  const fetchingLogs = useIsFetching({ queryKey: ['logs'] })

  const [filters, setFilters] = useState<TaskLogsFilters>(() => {
    const { start, end } = getDefaultTimeRange()
    return { startTime: start, endTime: end }
  })

  useEffect(() => {
    const { start, end } = getDefaultTimeRange()
    const baseFilters = {
      startTime: searchParams.startTime
        ? new Date(searchParams.startTime)
        : start,
      endTime: searchParams.endTime ? new Date(searchParams.endTime) : end,
      ...(searchParams.channel
        ? { channel: String(searchParams.channel) }
        : {}),
    }
    const next: TaskLogsFilters =
      props.logCategory === 'drawing'
        ? {
            ...baseFilters,
            ...(searchParams.filter ? { mjId: searchParams.filter } : {}),
          }
        : {
            ...baseFilters,
            ...(searchParams.filter ? { taskId: searchParams.filter } : {}),
            ...(searchParams.model ? { model: searchParams.model } : {}),
            ...(searchParams.status ? { status: searchParams.status } : {}),
            ...(isAdmin && searchParams.username
              ? { username: searchParams.username }
              : {}),
          }

    setFilters(next)
  }, [
    isAdmin,
    props.logCategory,
    searchParams.startTime,
    searchParams.endTime,
    searchParams.channel,
    searchParams.filter,
    searchParams.model,
    searchParams.status,
    searchParams.username,
  ])

  const handleChange = useCallback(
    (field: TaskLogsFilterField, value: Date | string | undefined) => {
      setFilters((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  const handleApply = useCallback(() => {
    const filterParams = buildSearchParams(filters, props.logCategory)
    navigate({
      to: '/usage-logs/$section',
      params: { section: props.logCategory },
      search: {
        ...filterParams,
        page: 1,
      },
    })
    queryClient.invalidateQueries({ queryKey: ['logs'] })
  }, [filters, navigate, props.logCategory, queryClient])

  const handleReset = useCallback(() => {
    const { start, end } = getDefaultTimeRange()
    const resetFilters: TaskLogsFilters = { startTime: start, endTime: end }
    setFilters(resetFilters)

    navigate({
      to: '/usage-logs/$section',
      params: { section: props.logCategory },
      search: {
        page: 1,
        startTime: start.getTime(),
        endTime: end.getTime(),
      },
    })
    queryClient.invalidateQueries({ queryKey: ['logs'] })
  }, [navigate, props.logCategory, queryClient])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') handleApply()
    },
    [handleApply]
  )

  const handleFilterChange = useCallback(
    (value: string) => {
      setFilters((prev) => setFilterValue(prev, props.logCategory, value))
    },
    [props.logCategory]
  )

  const filterValue = getFilterValue(filters, props.logCategory)
  const taskFilters = filters as TaskLogFilters
  const modelValue = props.logCategory === 'task' ? taskFilters.model || '' : ''
  const usernameValue =
    props.logCategory === 'task' && isAdmin ? taskFilters.username || '' : ''
  const statusValue =
    props.logCategory === 'task' &&
    taskFilters.status &&
    isTaskStatusFilterValue(taskFilters.status)
      ? taskFilters.status
      : TASK_STATUS_ALL_VALUE
  const statusLabel =
    TASK_STATUS_FILTERS.find((status) => status.value === statusValue)?.label ||
    'All Status'
  const placeholder =
    props.logCategory === 'drawing'
      ? t('Filter by Midjourney task ID')
      : t('Filter by task ID')
  const taskSearchValues =
    props.logCategory === 'task'
      ? [
          modelValue,
          usernameValue,
          statusValue === TASK_STATUS_ALL_VALUE ? '' : statusValue,
        ]
      : []
  const activeFilterValues = [filterValue, filters.channel, ...taskSearchValues]
  const hasAdditionalFilters = activeFilterValues.some(Boolean)
  const dateRangeFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={filters.startTime}
        end={filters.endTime}
        onChange={({ start, end }) => {
          handleChange('startTime', start)
          handleChange('endTime', end)
        }}
      />
    </LogsFilterField>
  )
  const taskIdFilter = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Task ID')}
        placeholder={placeholder}
        value={filterValue}
        onChange={(e) => handleFilterChange(e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const modelFilter =
    props.logCategory === 'task' ? (
      <LogsFilterField>
        <LogsFilterInput
          placeholder={t('Model Name')}
          value={modelValue}
          onChange={(e) => handleChange('model', e.target.value)}
          onKeyDown={handleKeyDown}
        />
      </LogsFilterField>
    ) : null
  const usernameFilter =
    props.logCategory === 'task' && isAdmin ? (
      <LogsFilterField>
        <LogsFilterInput
          placeholder={t('Username')}
          value={usernameValue}
          onChange={(e) => handleChange('username', e.target.value)}
          onKeyDown={handleKeyDown}
        />
      </LogsFilterField>
    ) : null
  const statusFilter =
    props.logCategory === 'task' ? (
      <LogsFilterField>
        <Select
          items={TASK_STATUS_FILTERS}
          value={statusValue}
          onValueChange={(value) => {
            const nextValue =
              value !== null && isTaskStatusFilterValue(value)
                ? value
                : TASK_STATUS_ALL_VALUE
            handleChange(
              'status',
              nextValue === TASK_STATUS_ALL_VALUE ? undefined : nextValue
            )
          }}
        >
          <SelectTrigger>
            <SelectValue>{t(statusLabel)}</SelectValue>
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {TASK_STATUS_FILTERS.map((status) => (
                <SelectItem key={status.value} value={status.value}>
                  {t(status.label)}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </LogsFilterField>
    ) : null
  const channelFilter = isAdmin ? (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Channel ID')}
        value={filters.channel || ''}
        onChange={(e) => handleChange('channel', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  ) : null

  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateRangeFilter}
          {taskIdFilter}
          {modelFilter}
          {statusFilter}
          {usernameFilter}
          {channelFilter}
        </>
      }
      mobilePinnedFilters={dateRangeFilter}
      mobileFilters={
        <>
          {taskIdFilter}
          {modelFilter}
          {statusFilter}
          {usernameFilter}
          {channelFilter}
        </>
      }
      mobileFilterCount={activeFilterValues.filter(Boolean).length}
      hasActiveFilters={hasAdditionalFilters}
      onSearch={handleApply}
      searchLoading={fetchingLogs > 0}
      onReset={handleReset}
    />
  )
}
