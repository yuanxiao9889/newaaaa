import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  BarChart3,
  Calculator,
  DollarSign,
  Percent,
  Plus,
  Save,
  ShieldCheck,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { PasswordInput } from '@/components/password-input'
import { SectionPageLayout } from '@/components/layout'
import { DatePicker } from '@/components/date-picker'
import { getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { useSystemConfig } from '@/hooks/use-system-config'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import type { CurrencyConfig } from '@/stores/system-config-store'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  deleteProfitCostPrice,
  getProfitCostPrices,
  getProfitCostPricePrefill,
  getProfitPasswordStatus,
  getProfitSummary,
  saveProfitCostPrice,
  setProfitPassword,
  verifyProfitPassword,
} from './api'
import type {
  ProfitCostPrice,
  ProfitCostPricePrefill,
  ProfitQueryParams,
  ProfitRange,
  SaveProfitCostPricePayload,
} from './types'

const nowSeconds = () => Math.floor(Date.now() / 1000)

function defaultStart(range: ProfitRange) {
  const date = new Date()
  if (range === 'month') date.setMonth(date.getMonth() - 6)
  else if (range === 'week') date.setDate(date.getDate() - 84)
  else date.setDate(date.getDate() - 30)
  date.setHours(0, 0, 0, 0)
  return Math.floor(date.getTime() / 1000)
}

function formatMoney(value: number) {
  return formatBillingCurrencyFromUSD(value || 0, {
    digitsLarge: 2,
    digitsSmall: 6,
    minimumNonZero: 0.000001,
  })
}

function formatPercent(value: number) {
  return `${((value || 0) * 100).toFixed(2)}%`
}

function toDateInput(timestamp?: number) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function fromDateInput(value: string, end = false) {
  if (!value) return undefined
  const [year, month, day] = value.split('-').map(Number)
  if (!year || !month || !day) return undefined
  const date = new Date(year, month - 1, day, end ? 23 : 0, end ? 59 : 0, end ? 59 : 0)
  return Math.floor(date.getTime() / 1000)
}

function timestampToDate(timestamp?: number) {
  if (!timestamp) return undefined
  return new Date(timestamp * 1000)
}

function dateToTimestamp(date?: Date, end = false) {
  if (!date) return undefined
  const normalized = new Date(date)
  normalized.setHours(end ? 23 : 0, end ? 59 : 0, end ? 59 : 0, end ? 999 : 0)
  return Math.floor(normalized.getTime() / 1000)
}

function toOptionalNumber(value: string) {
  const trimmed = value.trim()
  if (trimmed === '') return 0
  return Number(trimmed)
}

function getProfitMoneyUnit(currency?: CurrencyConfig) {
  if (currency?.quotaDisplayType === 'CNY') {
    return {
      label: 'CNY',
      rate: currency.usdExchangeRate && currency.usdExchangeRate > 0 ? currency.usdExchangeRate : 1,
    }
  }
  if (currency?.quotaDisplayType === 'CUSTOM') {
    return {
      label: currency.customCurrencySymbol?.trim() || 'Custom',
      rate:
        currency.customCurrencyExchangeRate && currency.customCurrencyExchangeRate > 0
          ? currency.customCurrencyExchangeRate
          : 1,
    }
  }
  return { label: 'USD', rate: 1 }
}

function displayMoneyToUSD(value: number, rate: number) {
  return rate > 0 ? value / rate : value
}

function usdToDisplayMoney(value: number, rate: number) {
  return value * (rate > 0 ? rate : 1)
}

function formatPriceInput(value?: number) {
  if (!value) return ''
  return Number(value.toFixed(6)).toString()
}

function parseChannelModels(channel?: Pick<Channel, 'models'>) {
  if (!channel?.models) return []
  return channel.models
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
    .filter((item, index, list) => list.indexOf(item) === index)
    .sort((a, b) => a.localeCompare(b))
}

type FormState = {
  channel_id: string
  model_name: string
  cost_mode: 'token' | 'request' | 'second'
  input_price: string
  cache_read_price: string
  output_price: string
  request_price: string
  second_price: string
  effective_date: string
}

const emptyForm = (): FormState => ({
  channel_id: '',
  model_name: '',
  cost_mode: 'token',
  input_price: '',
  cache_read_price: '',
  output_price: '',
  request_price: '',
  second_price: '',
  effective_date: toDateInput(nowSeconds()),
})

type ProfitPasswordMode = 'verify' | 'setup'

export function Profit() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { currency } = useSystemConfig()
  const profitMoneyUnit = useMemo(
    () => getProfitMoneyUnit(currency),
    [currency]
  )
  const [range, setRange] = useState<ProfitRange>('day')
  const [start, setStart] = useState(defaultStart('day'))
  const [end, setEnd] = useState(nowSeconds())
  const [draftStart, setDraftStart] = useState<Date | undefined>(() =>
    timestampToDate(defaultStart('day'))
  )
  const [draftEnd, setDraftEnd] = useState<Date | undefined>(() =>
    timestampToDate(nowSeconds())
  )
  const [channelId, setChannelId] = useState('')
  const [modelName, setModelName] = useState('')
  const [sheetOpen, setSheetOpen] = useState(false)
  const [form, setForm] = useState<FormState>(emptyForm)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [passwordMode, setPasswordMode] = useState<ProfitPasswordMode>('verify')

  const params = useMemo<ProfitQueryParams>(
    () => ({
      range,
      start_timestamp: start,
      end_timestamp: end,
      channel_id: channelId ? Number(channelId) : undefined,
      model_name: modelName || undefined,
    }),
    [range, start, end, channelId, modelName]
  )

  const showPasswordDialog = useCallback((code?: string) => {
    setPasswordMode(code === 'PROFIT_PASSWORD_NOT_CONFIGURED' ? 'setup' : 'verify')
    setPasswordOpen(true)
  }, [])

  const profitQuery = useCallback(
    async <T,>(fn: () => Promise<T>) => {
      try {
        return await fn()
      } catch (error: unknown) {
        const response = (error as { response?: { data?: { code?: string } } })
          ?.response
        const code = response?.data?.code
        if (
          code === 'PROFIT_VERIFICATION_REQUIRED' ||
          code === 'PROFIT_VERIFICATION_EXPIRED' ||
          code === 'PROFIT_PASSWORD_NOT_CONFIGURED'
        ) {
          showPasswordDialog(code)
          return null
        }
        throw error
      }
    },
    [showPasswordDialog]
  )

  const reportQuery = useQuery({
    queryKey: ['profit-summary', params],
    queryFn: async () => {
      const res = await profitQuery(() => getProfitSummary(params))
      return res?.data ?? null
    },
  })

  const pricesQuery = useQuery({
    queryKey: ['profit-cost-prices'],
    queryFn: async () => {
      const res = await profitQuery(() => getProfitCostPrices(false))
      return res?.data ?? []
    },
  })

  const passwordStatusQuery = useQuery({
    queryKey: ['profit-password-status'],
    queryFn: async () => {
      const res = await getProfitPasswordStatus()
      return res.data?.configured ?? false
    },
  })

  const channelsQuery = useQuery({
    queryKey: ['profit-channels'],
    queryFn: async () => {
      const res = await getChannels({ page_size: 1000 })
      return res.data?.items ?? []
    },
  })

  const selectedFormChannel = useMemo(
    () => (channelsQuery.data ?? []).find((channel) => String(channel.id) === form.channel_id),
    [channelsQuery.data, form.channel_id]
  )

  const formModels = useMemo(
    () => parseChannelModels(selectedFormChannel),
    [selectedFormChannel]
  )

  const prefillQuery = useQuery({
    queryKey: ['profit-cost-price-prefill', form.channel_id, form.model_name],
    enabled: sheetOpen && !!form.channel_id && !!form.model_name,
    queryFn: async () => {
      const res = await profitQuery(() =>
        getProfitCostPricePrefill(Number(form.channel_id), form.model_name)
      )
      return res?.data ?? null
    },
  })

  const report = reportQuery.data
  const summary = report?.summary

  const updateRange = (value: ProfitRange) => {
    const nextStart = defaultStart(value)
    const nextEnd = nowSeconds()
    setRange(value)
    setStart(nextStart)
    setEnd(nextEnd)
    setDraftStart(timestampToDate(nextStart))
    setDraftEnd(timestampToDate(nextEnd))
  }

  const applyFilters = () => {
    const nextStart = dateToTimestamp(draftStart)
    const nextEnd = dateToTimestamp(draftEnd, true)
    if (!nextStart || !nextEnd) {
      toast.error(t('Please select a start and end date'))
      return
    }
    if (nextStart > nextEnd) {
      toast.error(t('Start date cannot be later than end date'))
      return
    }
    setStart(nextStart)
    setEnd(nextEnd)
  }

  const openCreateSheet = () => {
    setForm(emptyForm())
    setSheetOpen(true)
  }

  const updateCostPriceChannel = (channelIdValue: string) => {
    const channel = (channelsQuery.data ?? []).find((item) => String(item.id) === channelIdValue)
    const models = parseChannelModels(channel)
    setForm((prev) => ({
      ...prev,
      channel_id: channelIdValue,
      model_name: models.length === 1 ? models[0] : '',
      cost_mode: 'token',
      input_price: '',
      cache_read_price: '',
      output_price: '',
      request_price: '',
      second_price: '',
    }))
  }

  const updateCostPriceModel = (nextModel: string) => {
    setForm((prev) => ({
      ...prev,
      model_name: nextModel,
      cost_mode: 'token',
      input_price: '',
      cache_read_price: '',
      output_price: '',
      request_price: '',
      second_price: '',
    }))
  }

  const submitCostPrice = async () => {
    const inputPrice = toOptionalNumber(form.input_price)
    const cacheReadPrice = toOptionalNumber(form.cache_read_price)
    const outputPrice = toOptionalNumber(form.output_price)
    const requestPrice = toOptionalNumber(form.request_price)
    const secondPrice = toOptionalNumber(form.second_price)
    const payload: SaveProfitCostPricePayload = {
      channel_id: Number(form.channel_id),
      model_name: form.model_name.trim(),
      price_type: form.cost_mode === 'token' ? 'simple_token' : 'fixed_price',
      effective_from: fromDateInput(form.effective_date),
    }
    if (form.cost_mode === 'token') {
      payload.input_price = displayMoneyToUSD(inputPrice, profitMoneyUnit.rate)
      payload.cache_read_price = displayMoneyToUSD(cacheReadPrice, profitMoneyUnit.rate)
      payload.output_price = displayMoneyToUSD(outputPrice, profitMoneyUnit.rate)
    } else if (form.cost_mode === 'request') {
      payload.fixed_unit = 'request'
      payload.fixed_amount = displayMoneyToUSD(requestPrice, profitMoneyUnit.rate)
    } else {
      payload.fixed_unit = 'second'
      payload.fixed_amount = displayMoneyToUSD(secondPrice, profitMoneyUnit.rate)
    }
    if (!payload.channel_id || !payload.model_name) {
      toast.error(t('Channel and model are required'))
      return
    }
    const activePrices =
      form.cost_mode === 'token'
        ? [inputPrice, cacheReadPrice, outputPrice]
        : form.cost_mode === 'request'
          ? [requestPrice]
          : [secondPrice]
    if (activePrices.some(Number.isNaN)) {
      toast.error(t('Please enter valid numbers'))
      return
    }
    if (activePrices.some((v) => v < 0)) {
      toast.error(t('Cost price cannot be negative'))
      return
    }
    if (activePrices.every((v) => v === 0)) {
      toast.error(t('At least one cost price is required'))
      return
    }
    const res = await profitQuery(() => saveProfitCostPrice(payload))
    if (res?.success) {
      toast.success(t('Cost price saved'))
      setSheetOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
      void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
    } else if (res?.message) {
      toast.error(res.message)
    }
  }

  const removeCostPrice = async (item: ProfitCostPrice) => {
    const res = await profitQuery(() => deleteProfitCostPrice(item.id))
    if (res?.success) {
      toast.success(t('Cost price disabled'))
      void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
      void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
    }
  }

  const handlePasswordSuccess = () => {
    setPasswordOpen(false)
    void queryClient.invalidateQueries({ queryKey: ['profit-password-status'] })
    void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
    void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
  }

  useEffect(() => {
    if (!prefillQuery.data) return
    const prefill = prefillQuery.data
    const matchesCurrentForm =
      String(prefill.channel_id) === form.channel_id &&
      prefill.model_name === form.model_name
    const formHasNoPrices =
      form.input_price === '' &&
      form.cache_read_price === '' &&
      form.output_price === '' &&
      form.request_price === '' &&
      form.second_price === ''
    if (matchesCurrentForm && formHasNoPrices && prefill.has_pricing) {
      const nextInputPrice = formatPriceInput(usdToDisplayMoney(prefill.input_price, profitMoneyUnit.rate))
      const nextCacheReadPrice = formatPriceInput(usdToDisplayMoney(prefill.cache_read_price, profitMoneyUnit.rate))
      const nextOutputPrice = formatPriceInput(usdToDisplayMoney(prefill.output_price, profitMoneyUnit.rate))
      const nextRequestPrice = formatPriceInput(usdToDisplayMoney(prefill.request_price, profitMoneyUnit.rate))
      const nextSecondPrice = formatPriceInput(usdToDisplayMoney(prefill.second_price, profitMoneyUnit.rate))
      if (!nextInputPrice && !nextCacheReadPrice && !nextOutputPrice && !nextRequestPrice && !nextSecondPrice) {
        return
      }
      setForm((prev) => ({
        ...prev,
        cost_mode:
          prefill.pricing_mode === 'simple_token'
            ? 'token'
            : prefill.pricing_mode === 'fixed_price' && nextRequestPrice
              ? 'request'
              : prefill.pricing_mode === 'fixed_price' && nextSecondPrice
                ? 'second'
              : prev.cost_mode,
        input_price: nextInputPrice,
        cache_read_price: nextCacheReadPrice,
        output_price: nextOutputPrice,
        request_price: nextRequestPrice,
        second_price: nextSecondPrice,
      }))
    }
  }, [
    form.channel_id,
    form.cache_read_price,
    form.input_price,
    form.model_name,
    form.output_price,
    form.request_price,
    form.second_price,
    prefillQuery.data,
    profitMoneyUnit.rate,
  ])

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Profit')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button size='sm' onClick={openCreateSheet}>
            <Plus className='size-4' />
            {t('Cost price')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-3'>
            {passwordStatusQuery.data === false && (
              <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200'>
                <span>{t('Profit password is not configured')}</span>
                <Button size='sm' variant='outline' onClick={() => showPasswordDialog('PROFIT_PASSWORD_NOT_CONFIGURED')}>
                  <ShieldCheck className='size-4' />
                  {t('Set profit access password')}
                </Button>
              </div>
            )}

            <div className='flex flex-wrap items-end gap-2 border-b pb-3'>
              <div className='flex flex-col gap-1'>
                <Label>{t('Range')}</Label>
                <Select value={range} onValueChange={(v) => updateRange(v as ProfitRange)}>
                  <SelectTrigger className='w-32'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='day'>{t('Daily')}</SelectItem>
                    <SelectItem value='week'>{t('Weekly')}</SelectItem>
                    <SelectItem value='month'>{t('Monthly')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className='flex flex-col gap-1'>
                <Label>{t('Start date')}</Label>
                <DatePicker
                  selected={draftStart}
                  onSelect={setDraftStart}
                  placeholder={t('Start date')}
                />
              </div>
              <div className='flex flex-col gap-1'>
                <Label>{t('End date')}</Label>
                <DatePicker
                  selected={draftEnd}
                  onSelect={setDraftEnd}
                  placeholder={t('End date')}
                />
              </div>
              <div className='flex flex-col gap-1'>
                <Label>{t('Channel')}</Label>
                <Input
                  className='h-8 w-32'
                  placeholder={t('Channel ID')}
                  value={channelId}
                  onChange={(e) => setChannelId(e.target.value)}
                />
              </div>
              <div className='flex flex-col gap-1'>
                <Label>{t('Model')}</Label>
                <Input
                  className='h-8 w-56'
                  placeholder={t('Model name')}
                  value={modelName}
                  onChange={(e) => setModelName(e.target.value)}
                />
              </div>
              <Button
                variant='outline'
                size='sm'
                onClick={applyFilters}
              >
                <BarChart3 className='size-4' />
                {t('Apply filters')}
              </Button>
            </div>

            <div className='text-muted-foreground text-sm'>
              {t('Applied range')}: {toDateInput(start)} - {toDateInput(end)}
            </div>

            <div className='grid gap-2 md:grid-cols-5'>
              <MetricCell
                icon={<DollarSign className='size-4' />}
                label={t('Usage revenue')}
                value={formatMoney(summary?.revenue ?? 0)}
                description={t('Calculated from usage logs, not top-up payments')}
              />
              <MetricCell icon={<DollarSign className='size-4' />} label={t('Cost')} value={formatMoney(summary?.cost ?? 0)} />
              <MetricCell icon={<BarChart3 className='size-4' />} label={t('Profit')} value={formatMoney(summary?.profit ?? 0)} />
              <MetricCell icon={<Percent className='size-4' />} label={t('Margin')} value={formatPercent(summary?.profit_margin ?? 0)} />
              <MetricCell icon={<ShieldCheck className='size-4' />} label={t('Coverage')} value={formatPercent(summary?.coverage_rate ?? 0)} />
            </div>

            {(summary?.unpriced_count ?? 0) > 0 && (
              <div className='flex items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200'>
                <AlertTriangle className='size-4' />
                {t('Some usage has no cost price configured. Profit only includes priced usage.')}
              </div>
            )}

            <div className='grid gap-3 xl:grid-cols-[1fr_1.1fr]'>
              <DataPanel title={t('Trend')}>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Period')}</TableHead>
                      <TableHead>{t('Usage revenue')}</TableHead>
                      <TableHead>{t('Cost')}</TableHead>
                      <TableHead>{t('Profit')}</TableHead>
                      <TableHead>{t('Coverage')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(report?.series ?? []).map((item) => (
                      <TableRow key={item.period_start}>
                        <TableCell>{item.period_label}</TableCell>
                        <TableCell>{formatMoney(item.revenue)}</TableCell>
                        <TableCell>{formatMoney(item.cost)}</TableCell>
                        <TableCell>{formatMoney(item.profit)}</TableCell>
                        <TableCell>{formatPercent(item.coverage_rate)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </DataPanel>

              <DataPanel title={t('Channel model breakdown')}>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Channel')}</TableHead>
                      <TableHead>{t('Model')}</TableHead>
                      <TableHead>{t('Requests')}</TableHead>
                      <TableHead>{t('Usage revenue')}</TableHead>
                      <TableHead>{t('Profit')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(report?.breakdown ?? []).map((item) => (
                      <TableRow key={`${item.channel_id}-${item.model_name}`}>
                        <TableCell>{item.channel_name || item.channel_id}</TableCell>
                        <TableCell className='max-w-60 truncate'>{item.model_name}</TableCell>
                        <TableCell>{item.request_count}</TableCell>
                        <TableCell>{formatMoney(item.revenue)}</TableCell>
                        <TableCell>{formatMoney(item.profit)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </DataPanel>
            </div>

            <DataPanel title={t('Cost prices')}>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Channel')}</TableHead>
                    <TableHead>{t('Model')}</TableHead>
                    <TableHead>{t('Type')}</TableHead>
                    <TableHead>{t('Price summary')}</TableHead>
                    <TableHead>{t('Effective from')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead className='w-20'></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(pricesQuery.data ?? []).map((item) => (
                    <TableRow key={item.id}>
                      <TableCell>{item.channel_name || item.channel_id}</TableCell>
                      <TableCell>{item.model_name}</TableCell>
                      <TableCell>{priceTypeLabel(item.price_type, t)}</TableCell>
                      <TableCell>{priceSummary(item, t)}</TableCell>
                      <TableCell>{toDateInput(item.effective_from)}</TableCell>
                      <TableCell>{item.disabled ? t('Disabled') : t('Active')}</TableCell>
                      <TableCell>
                        <Button variant='ghost' size='icon-sm' onClick={() => void removeCostPrice(item)}>
                          <Trash2 className='size-4' />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </DataPanel>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
        <SheetContent className='sm:max-w-xl'>
          <SheetHeader>
            <SheetTitle>{t('Cost price')}</SheetTitle>
            <SheetDescription>
              {t('Configure simple encrypted upstream cost pricing by channel and model.')}
            </SheetDescription>
          </SheetHeader>
          <div className='flex flex-1 flex-col gap-3 overflow-auto px-4'>
            <div className='grid gap-3 sm:grid-cols-2'>
              <Field label={t('Channel')}>
                <Select
                  value={form.channel_id}
                  onValueChange={(v) => updateCostPriceChannel(v ?? '')}
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue placeholder={t('Select channel')} />
                  </SelectTrigger>
                  <SelectContent>
                    {(channelsQuery.data ?? []).map((channel: Channel) => (
                      <SelectItem key={channel.id} value={String(channel.id)}>
                        {channel.name || `#${channel.id}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label={t('Model')}>
                <Select
                  value={form.model_name}
                  onValueChange={(v) => updateCostPriceModel(v ?? '')}
                  disabled={!form.channel_id || formModels.length === 0}
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue
                      placeholder={
                        form.channel_id
                          ? t('Select model')
                          : t('Select channel first')
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {formModels.map((model) => (
                      <SelectItem key={model} value={model}>
                        {model}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
            {form.channel_id && formModels.length === 0 && (
              <div className='text-muted-foreground text-sm'>
                {t('This channel has no configured models. Please update the channel model list first.')}
              </div>
            )}
            {form.model_name && prefillQuery.data?.has_pricing && (
              <div className='rounded-md border bg-muted/30 px-3 py-2 text-sm'>
                <div className='flex items-center gap-1.5 font-medium'>
                  <Calculator className='size-4' />
                  {t('Selling price reference')}
                </div>
                <div className='text-muted-foreground mt-1'>
                  {sellingPriceReference(prefillQuery.data, t)}
                </div>
                <div className='text-muted-foreground mt-1'>
                  {t('Selling price and upstream cost can use different billing units.')}
                </div>
              </div>
            )}
            {form.model_name && prefillQuery.data && !prefillQuery.data.has_pricing && (
              <div className='text-muted-foreground text-sm'>
                {t('No selling price found for this model. Please enter cost price manually.')}
              </div>
            )}
            <div className='grid gap-3 sm:grid-cols-2'>
              <Field label={t('Cost billing mode')}>
                <Select
                  value={form.cost_mode}
                  onValueChange={(v) =>
                    setForm((prev) => ({
                      ...prev,
                      cost_mode: (v as FormState['cost_mode']) ?? 'token',
                    }))
                  }
                >
                  <SelectTrigger className='w-full'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='token'>{t('Token unit price')}</SelectItem>
                    <SelectItem value='request'>{t('Fixed per request')}</SelectItem>
                    <SelectItem value='second'>{t('Fixed per second')}</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              {form.cost_mode === 'token' && (
                <>
                  <Field label={t('Input price ({{currency}} / 1M tokens)', { currency: profitMoneyUnit.label })}>
                    <Input
                      type='number'
                      step='0.000001'
                      min='0'
                      value={form.input_price}
                      onChange={(e) => setForm((prev) => ({ ...prev, input_price: e.target.value }))}
                    />
                  </Field>
                  <Field label={t('Cache read price ({{currency}} / 1M tokens)', { currency: profitMoneyUnit.label })}>
                    <Input
                      type='number'
                      step='0.000001'
                      min='0'
                      value={form.cache_read_price}
                      onChange={(e) => setForm((prev) => ({ ...prev, cache_read_price: e.target.value }))}
                    />
                  </Field>
                  <Field label={t('Output price ({{currency}} / 1M tokens)', { currency: profitMoneyUnit.label })}>
                    <Input
                      type='number'
                      step='0.000001'
                      min='0'
                      value={form.output_price}
                      onChange={(e) => setForm((prev) => ({ ...prev, output_price: e.target.value }))}
                    />
                  </Field>
                </>
              )}
              {form.cost_mode === 'request' && (
                <Field label={t('Fixed per request ({{currency}})', { currency: profitMoneyUnit.label })}>
                  <Input
                    type='number'
                    step='0.000001'
                    min='0'
                    value={form.request_price}
                    onChange={(e) => setForm((prev) => ({ ...prev, request_price: e.target.value }))}
                  />
                </Field>
              )}
              {form.cost_mode === 'second' && (
                <>
                  <Field label={t('Fixed per second ({{currency}})', { currency: profitMoneyUnit.label })}>
                    <Input
                      type='number'
                      step='0.000001'
                      min='0'
                      value={form.second_price}
                      onChange={(e) => setForm((prev) => ({ ...prev, second_price: e.target.value }))}
                    />
                  </Field>
                  <div className='text-muted-foreground self-end text-sm'>
                    {t('For video tasks, per-second cost uses the task duration from logs when available.')}
                  </div>
                </>
              )}
              <Field label={t('Effective from')}>
                <Input
                  type='date'
                  value={form.effective_date}
                  onChange={(e) => setForm((prev) => ({ ...prev, effective_date: e.target.value }))}
                />
              </Field>
            </div>
          </div>
          <SheetFooter>
            <Button onClick={() => void submitCostPrice()}>
              <Save className='size-4' />
              {t('Save')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <ProfitPasswordDialog
        open={passwordOpen}
        mode={passwordMode}
        onOpenChange={setPasswordOpen}
        onSuccess={handlePasswordSuccess}
      />
    </>
  )
}

function priceTypeLabel(type: ProfitCostPrice['price_type'], t: (key: string) => string) {
  if (type === 'simple_token') return t('Unit pricing')
  if (type === 'tiered_expr') return t('Advanced expression')
  return t('Fixed cost')
}

function priceSummary(item: ProfitCostPrice, t: (key: string) => string) {
  if (item.price_type !== 'simple_token') return item.price_summary || '-'
  const parts: string[] = []
  if ((item.input_price ?? 0) > 0) parts.push(`${t('Input')} ${formatMoney(item.input_price ?? 0)}`)
  if ((item.cache_read_price ?? 0) > 0) parts.push(`${t('Cache Read')} ${formatMoney(item.cache_read_price ?? 0)}`)
  if ((item.output_price ?? 0) > 0) parts.push(`${t('Output')} ${formatMoney(item.output_price ?? 0)}`)
  if ((item.request_price ?? 0) > 0) parts.push(`${t('Per request')} ${formatMoney(item.request_price ?? 0)}`)
  if ((item.second_price ?? 0) > 0) parts.push(`${t('Per second')} ${formatMoney(item.second_price ?? 0)}`)
  return parts.join(' / ') || '-'
}

function sellingPriceReference(
  prefill: ProfitCostPricePrefill,
  t: (key: string) => string
) {
  const parts: string[] = []
  if (prefill.pricing_mode === 'simple_token') {
    if (prefill.input_price > 0) parts.push(`${t('Input')} ${formatMoney(prefill.input_price)} / 1M tokens`)
    if (prefill.cache_read_price > 0) parts.push(`${t('Cache Read')} ${formatMoney(prefill.cache_read_price)} / 1M tokens`)
    if (prefill.output_price > 0) parts.push(`${t('Output')} ${formatMoney(prefill.output_price)} / 1M tokens`)
  }
  if (prefill.request_price > 0) parts.push(`${t('Per request')} ${formatMoney(prefill.request_price)}`)
  if (prefill.second_price > 0) parts.push(`${t('Per second')} ${formatMoney(prefill.second_price)}`)
  return parts.join(' / ') || '-'
}

function ProfitPasswordDialog(props: {
  open: boolean
  mode: ProfitPasswordMode
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const isSetup = props.mode === 'setup'

  const submit = async () => {
    if (!password) {
      toast.error(t('Password is required'))
      return
    }
    if (isSetup && password !== confirmPassword) {
      toast.error(t('Passwords do not match'))
      return
    }
    setLoading(true)
    try {
      const res = isSetup
        ? await setProfitPassword(password)
        : await verifyProfitPassword(password)
      if (res.success) {
        toast.success(isSetup ? t('Password saved') : t('Password verified'))
        setPassword('')
        setConfirmPassword('')
        props.onSuccess()
      } else {
        toast.error(res.message || t('Request failed'))
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        props.onOpenChange(open)
        if (!open) {
          setPassword('')
          setConfirmPassword('')
        }
      }}
    >
      <DialogContent showCloseButton={!loading}>
        <DialogHeader>
          <DialogTitle>
            {isSetup ? t('Set profit access password') : t('Enter profit access password')}
          </DialogTitle>
          <DialogDescription>
            {isSetup
              ? t('Please set a profit access password first.')
              : t('Profit and cost prices are sensitive financial data.')}
          </DialogDescription>
        </DialogHeader>
        <div className='grid gap-3'>
          <Field label={t('Profit access password')}>
            <PasswordInput
              value={password}
              disabled={loading}
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void submit()
              }}
            />
          </Field>
          {isSetup && (
            <Field label={t('Confirm profit access password')}>
              <PasswordInput
                value={confirmPassword}
                disabled={loading}
                onChange={(e) => setConfirmPassword(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void submit()
                }}
              />
            </Field>
          )}
        </div>
        <DialogFooter>
          <Button onClick={() => void submit()} disabled={loading}>
            <ShieldCheck className='size-4' />
            {isSetup ? t('Save') : t('Verify')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function MetricCell(props: { icon: ReactNode; label: string; value: string; description?: string }) {
  return (
    <div className='rounded-md border px-3 py-2'>
      <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        {props.icon}
        {props.label}
      </div>
      <div className='mt-1 truncate text-lg font-semibold'>{props.value}</div>
      {props.description && (
        <div className='text-muted-foreground mt-1 truncate text-xs'>{props.description}</div>
      )}
    </div>
  )
}

function DataPanel(props: { title: string; children: ReactNode }) {
  return (
    <section className='min-h-0 rounded-md border'>
      <div className='border-b px-3 py-2 text-sm font-medium'>{props.title}</div>
      <div className='max-h-[420px] overflow-auto'>{props.children}</div>
    </section>
  )
}

function Field(props: { label: string; children: ReactNode }) {
  return (
    <div className='flex flex-col gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
    </div>
  )
}
