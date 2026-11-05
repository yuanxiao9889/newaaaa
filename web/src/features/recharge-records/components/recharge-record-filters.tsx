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
import { Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DatePicker } from '@/components/date-picker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { INVOICE_STATUS_OPTIONS, RECHARGE_SOURCE_OPTIONS } from '../constants'
import type { RechargeRecordFilters } from '../types'

interface RechargeRecordFilterBarProps {
  filters: RechargeRecordFilters
  onChange: (patch: Partial<RechargeRecordFilters>) => void
  onApply: () => void
  onReset: () => void
  loading?: boolean
  showUserFilter?: boolean
}

const selectTriggerClassName = 'h-9 w-full sm:w-[150px]'

export function RechargeRecordFilterBar(props: RechargeRecordFilterBarProps) {
  const { t } = useTranslation()
  const invalidRange =
    props.filters.startDate != null &&
    props.filters.endDate != null &&
    props.filters.startDate.getTime() > props.filters.endDate.getTime()

  return (
    <div className='flex w-full flex-col gap-2'>
      <p className='text-muted-foreground text-xs'>
        {t('Only successful recharges are shown.')}
      </p>
      <div className='flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center'>
        <Input
          value={props.filters.keyword}
          onChange={(event) => props.onChange({ keyword: event.target.value })}
          placeholder={t('Search by user, order number, or record number')}
          aria-label={t('Search by user, order number, or record number')}
          className='h-9 w-full sm:w-[280px]'
        />
        {props.showUserFilter && (
          <Input
            type='number'
            min={1}
            value={props.filters.userId}
            onChange={(event) => props.onChange({ userId: event.target.value })}
            placeholder={t('User ID')}
            aria-label={t('User ID')}
            className='h-9 w-full sm:w-[120px]'
          />
        )}
        <Select
          items={[
            { value: 'all', label: t('All sources') },
            ...RECHARGE_SOURCE_OPTIONS.map((option) => ({
              value: option.value,
              label: t(option.labelKey),
            })),
          ]}
          value={props.filters.source || 'all'}
          onValueChange={(value) =>
            props.onChange({ source: value === 'all' ? '' : (value ?? '') })
          }
        >
          <SelectTrigger
            className={selectTriggerClassName}
            aria-label={t('Source')}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              <SelectItem value='all'>{t('All sources')}</SelectItem>
              {RECHARGE_SOURCE_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Select
          items={[
            { value: 'all', label: t('All invoice statuses') },
            ...INVOICE_STATUS_OPTIONS.map((option) => ({
              value: option.value,
              label: t(option.labelKey),
            })),
          ]}
          value={props.filters.invoiceStatus || 'all'}
          onValueChange={(value) =>
            props.onChange({
              invoiceStatus: value === 'all' ? '' : (value ?? ''),
            })
          }
        >
          <SelectTrigger
            className={selectTriggerClassName}
            aria-label={t('Invoice status')}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              <SelectItem value='all'>{t('All invoice statuses')}</SelectItem>
              {INVOICE_STATUS_OPTIONS.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <div className='flex items-center gap-1.5'>
          <DatePicker
            selected={props.filters.startDate}
            onSelect={(date) => props.onChange({ startDate: date })}
            placeholder={t('Start date')}
            aria-label={t('Start date')}
            className='h-9 w-full sm:w-[150px]'
          />
          <span className='text-muted-foreground text-xs'>-</span>
          <DatePicker
            selected={props.filters.endDate}
            onSelect={(date) => props.onChange({ endDate: date })}
            placeholder={t('End date')}
            aria-label={t('End date')}
            className='h-9 w-full sm:w-[150px]'
          />
        </div>
        <div className='flex items-center gap-2 sm:ms-auto'>
          <Button variant='outline' className='h-9' onClick={props.onReset}>
            {t('Reset')}
          </Button>
          <Button
            className='h-9 gap-1.5'
            onClick={props.onApply}
            disabled={props.loading || invalidRange}
          >
            <Search className='size-4' />
            {t('Search')}
          </Button>
        </div>
      </div>
      {invalidRange && (
        <p className='text-destructive text-xs' role='alert'>
          {t('Start date must not be later than end date')}
        </p>
      )}
    </div>
  )
}
