import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Calculator,
  Plus,
  RefreshCw,
  Save,
  ShieldCheck,
  SlidersHorizontal,
} from 'lucide-react'
import { useCallback, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DatePicker } from '@/components/date-picker'
import { Dialog as FormDialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { useSystemConfig } from '@/hooks/use-system-config'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import type { CurrencyConfig } from '@/stores/system-config-store'

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
import {
  CostPriceFields,
  type CostPriceForm,
} from './components/cost-price-fields'
import { CostPriceTable } from './components/cost-price-table'
import { ProfitOverview } from './components/profit-overview'
import { formatMoney, toDateInput } from './lib/format'
import type {
  ProfitCostPrice,
  ProfitCostPricePrefill,
  ProfitQueryParams,
  ProfitRange,
  SaveProfitCostPricePayload,
} from './types'

const nowSeconds = () => Math.floor(Date.now() / 1000)

function defaultStart(days = 30) {
  const date = new Date()
  date.setDate(date.getDate() - days + 1)
  date.setHours(0, 0, 0, 0)
  return Math.floor(date.getTime() / 1000)
}

function fromDateInput(value: string, end = false) {
  if (!value) return undefined
  const [year, month, day] = value.split('-').map(Number)
  if (!year || !month || !day) return undefined
  const date = new Date(
    year,
    month - 1,
    day,
    end ? 23 : 0,
    end ? 59 : 0,
    end ? 59 : 0
  )
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
      rate:
        currency.usdExchangeRate && currency.usdExchangeRate > 0
          ? currency.usdExchangeRate
          : 1,
    }
  }
  if (currency?.quotaDisplayType === 'CUSTOM') {
    return {
      label: currency.customCurrencySymbol?.trim() || 'Custom',
      rate:
        currency.customCurrencyExchangeRate &&
        currency.customCurrencyExchangeRate > 0
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
  if (value === undefined || value === 0) return ''
  // Keep small prices while trimming binary noise from currency conversion.
  return Number(value.toPrecision(15)).toString()
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

type FormState = CostPriceForm

const emptyForm = (): FormState => ({
  channel_id: '',
  model_name: '',
  cost_mode: 'token',
  input_price: '',
  cache_read_price: '',
  cache_creation_price: '',
  cache_creation_1h_price: '',
  image_input_price: '',
  image_output_price: '',
  audio_input_price: '',
  audio_output_price: '',
  output_price: '',
  request_price: '',
  second_price: '',
  effective_date: toDateInput(nowSeconds()),
})

type ProfitPasswordMode = 'verify' | 'setup'

type CostPriceSheetMode = 'create' | 'edit'

export function Profit() {
  const { t } = useTranslation()
  const contentRef = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  const { currency } = useSystemConfig()
  const profitMoneyUnit = useMemo(
    () => getProfitMoneyUnit(currency),
    [currency]
  )
  const [activeTab, setActiveTab] = useState('overview')
  const [range, setRange] = useState<ProfitRange>('day')
  const [start, setStart] = useState(defaultStart())
  const [end, setEnd] = useState(nowSeconds())
  const [draftStart, setDraftStart] = useState<Date | undefined>(() =>
    timestampToDate(defaultStart())
  )
  const [draftEnd, setDraftEnd] = useState<Date | undefined>(() =>
    timestampToDate(nowSeconds())
  )
  const [draftRange, setDraftRange] = useState<ProfitRange>('day')
  const [draftChannelId, setDraftChannelId] = useState('')
  const [draftModelName, setDraftModelName] = useState('')
  const [priceSearch, setPriceSearch] = useState('')
  const [priceStatus, setPriceStatus] = useState('active')
  const [filtersOpen, setFiltersOpen] = useState(false)
  const [quickRange, setQuickRange] = useState('30')
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ProfitCostPrice | null>(null)
  const [channelId, setChannelId] = useState('')
  const [modelName, setModelName] = useState('')
  const [sheetOpen, setSheetOpen] = useState(false)
  const [sheetMode, setSheetMode] = useState<CostPriceSheetMode>('create')
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
    setPasswordMode(
      code === 'PROFIT_PASSWORD_NOT_CONFIGURED' ? 'setup' : 'verify'
    )
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
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const res = await profitQuery(() => getProfitSummary(params, signal))
      return res ? requireServerSuccess(res).data : null
    },
  })

  const pricesQuery = useQuery({
    queryKey: ['profit-cost-prices'],
    enabled: activeTab === 'costs',
    queryFn: async ({ signal }) => {
      const res = await profitQuery(() => getProfitCostPrices(false, signal))
      return res ? requireServerSuccess(res).data : []
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
    enabled: filtersOpen || sheetOpen,
    staleTime: 60_000,
    queryFn: async () => {
      const first = requireServerSuccess(
        await getChannels({ p: 1, page_size: 100, id_sort: true })
      )
      const channels = [...(first.data?.items ?? [])]
      const pageCount = Math.ceil(
        (first.data?.total ?? 0) / (first.data?.page_size || 100)
      )
      // Respect the server's 100-row cap and limit concurrent page requests.
      for (let page = 2; page <= pageCount; page += 5) {
        const pages = await Promise.all(
          Array.from(
            { length: Math.min(5, pageCount - page + 1) },
            (_, index) =>
              getChannels({ p: page + index, page_size: 100, id_sort: true })
          )
        )
        for (const result of pages) {
          channels.push(...(requireServerSuccess(result).data?.items ?? []))
        }
      }
      return [
        ...new Map(channels.map((channel) => [channel.id, channel])).values(),
      ]
    },
  })

  const channelOptions = useMemo(
    () =>
      (channelsQuery.data ?? []).map((channel) => ({
        value: String(channel.id),
        label: `#${channel.id} ${channel.name || ''}`.trim(),
      })),
    [channelsQuery.data]
  )
  const selectedFormChannel = useMemo(
    () =>
      (channelsQuery.data ?? []).find(
        (channel) => String(channel.id) === form.channel_id
      ),
    [channelsQuery.data, form.channel_id]
  )

  const formModels = useMemo(
    () => parseChannelModels(selectedFormChannel),
    [selectedFormChannel]
  )

  const prefillQuery = useQuery({
    queryKey: ['profit-cost-price-prefill', form.channel_id, form.model_name],
    enabled: sheetOpen && !!form.channel_id && !!form.model_name,
    queryFn: async ({ signal }) => {
      const res = await profitQuery(() =>
        getProfitCostPricePrefill(
          Number(form.channel_id),
          form.model_name,
          signal
        )
      )
      return res ? requireServerSuccess(res).data : null
    },
  })

  const report = reportQuery.data

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
    setRange(draftRange)
    setChannelId(draftChannelId)
    setModelName(draftModelName.trim())
    setQuickRange('')
    setFiltersOpen(false)
  }

  const openCreateSheet = useCallback((channel = '', model = '') => {
    setSheetMode('create')
    setForm({ ...emptyForm(), channel_id: channel, model_name: model })
    setSheetOpen(true)
  }, [])

  const openEditSheet = (item: ProfitCostPrice) => {
    if (item.price_type === 'tiered_expr') {
      toast.error(t('Advanced expression cost prices cannot be edited here'))
      return
    }
    let costMode: FormState['cost_mode'] = 'token'
    if (item.price_type === 'fixed_price') {
      costMode = item.fixed_unit === 'second' ? 'second' : 'request'
    }
    setSheetMode('edit')
    setForm({
      channel_id: String(item.channel_id),
      model_name: item.model_name,
      cost_mode: costMode,
      input_price: formatPriceInput(
        usdToDisplayMoney(item.input_price ?? 0, profitMoneyUnit.rate)
      ),
      cache_read_price: formatPriceInput(
        usdToDisplayMoney(item.cache_read_price ?? 0, profitMoneyUnit.rate)
      ),
      cache_creation_price: formatPriceInput(
        usdToDisplayMoney(item.cache_creation_price ?? 0, profitMoneyUnit.rate)
      ),
      cache_creation_1h_price: formatPriceInput(
        usdToDisplayMoney(
          item.cache_creation_1h_price ?? 0,
          profitMoneyUnit.rate
        )
      ),
      image_input_price: formatPriceInput(
        usdToDisplayMoney(item.image_input_price ?? 0, profitMoneyUnit.rate)
      ),
      image_output_price: formatPriceInput(
        usdToDisplayMoney(item.image_output_price ?? 0, profitMoneyUnit.rate)
      ),
      audio_input_price: formatPriceInput(
        usdToDisplayMoney(item.audio_input_price ?? 0, profitMoneyUnit.rate)
      ),
      audio_output_price: formatPriceInput(
        usdToDisplayMoney(item.audio_output_price ?? 0, profitMoneyUnit.rate)
      ),
      output_price: formatPriceInput(
        usdToDisplayMoney(item.output_price ?? 0, profitMoneyUnit.rate)
      ),
      request_price: formatPriceInput(
        usdToDisplayMoney(item.request_price ?? 0, profitMoneyUnit.rate)
      ),
      second_price: formatPriceInput(
        usdToDisplayMoney(item.second_price ?? 0, profitMoneyUnit.rate)
      ),
      effective_date:
        toDateInput(item.effective_from) || toDateInput(nowSeconds()),
    })
    setSheetOpen(true)
  }

  const updateCostPriceChannel = (channelIdValue: string) => {
    const channel = (channelsQuery.data ?? []).find(
      (item) => String(item.id) === channelIdValue
    )
    const models = parseChannelModels(channel)
    setForm((prev) => ({
      ...prev,
      channel_id: channelIdValue,
      model_name: models.length === 1 ? models[0] : '',
      cost_mode: 'token',
      input_price: '',
      cache_read_price: '',
      cache_creation_price: '',
      cache_creation_1h_price: '',
      image_input_price: '',
      image_output_price: '',
      audio_input_price: '',
      audio_output_price: '',
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
      cache_creation_price: '',
      cache_creation_1h_price: '',
      image_input_price: '',
      image_output_price: '',
      audio_input_price: '',
      audio_output_price: '',
      output_price: '',
      request_price: '',
      second_price: '',
    }))
  }

  const submitCostPrice = async () => {
    if (savingRef.current) return
    const inputPrice = toOptionalNumber(form.input_price)
    const cacheReadPrice = toOptionalNumber(form.cache_read_price)
    const cacheCreationPrice = toOptionalNumber(form.cache_creation_price)
    const cacheCreation1hPrice = toOptionalNumber(form.cache_creation_1h_price)
    const imageInputPrice = toOptionalNumber(form.image_input_price)
    const imageOutputPrice = toOptionalNumber(form.image_output_price)
    const audioInputPrice = toOptionalNumber(form.audio_input_price)
    const audioOutputPrice = toOptionalNumber(form.audio_output_price)
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
      payload.cache_read_price = displayMoneyToUSD(
        cacheReadPrice,
        profitMoneyUnit.rate
      )
      payload.cache_creation_price = displayMoneyToUSD(
        cacheCreationPrice,
        profitMoneyUnit.rate
      )
      payload.cache_creation_1h_price = displayMoneyToUSD(
        cacheCreation1hPrice,
        profitMoneyUnit.rate
      )
      payload.image_input_price = displayMoneyToUSD(
        imageInputPrice,
        profitMoneyUnit.rate
      )
      payload.image_output_price = displayMoneyToUSD(
        imageOutputPrice,
        profitMoneyUnit.rate
      )
      payload.audio_input_price = displayMoneyToUSD(
        audioInputPrice,
        profitMoneyUnit.rate
      )
      payload.audio_output_price = displayMoneyToUSD(
        audioOutputPrice,
        profitMoneyUnit.rate
      )
      payload.output_price = displayMoneyToUSD(
        outputPrice,
        profitMoneyUnit.rate
      )
    } else if (form.cost_mode === 'request') {
      payload.fixed_unit = 'request'
      payload.fixed_amount = displayMoneyToUSD(
        requestPrice,
        profitMoneyUnit.rate
      )
    } else {
      payload.fixed_unit = 'second'
      payload.fixed_amount = displayMoneyToUSD(
        secondPrice,
        profitMoneyUnit.rate
      )
    }
    if (!payload.channel_id || !payload.model_name) {
      toast.error(t('Channel and model are required'))
      return
    }
    let activePrices = [secondPrice]
    if (form.cost_mode === 'token') {
      activePrices = [
        inputPrice,
        cacheReadPrice,
        cacheCreationPrice,
        cacheCreation1hPrice,
        imageInputPrice,
        imageOutputPrice,
        audioInputPrice,
        audioOutputPrice,
        outputPrice,
      ]
    } else if (form.cost_mode === 'request') {
      activePrices = [requestPrice]
    }
    if (activePrices.some((value) => !Number.isFinite(value))) {
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
    if (!payload.effective_from) {
      toast.error(t('Please select an effective date'))
      return
    }
    savingRef.current = true
    setSaving(true)
    try {
      const res = await profitQuery(() => saveProfitCostPrice(payload))
      if (!res) return
      requireServerSuccess(res)
      toast.success(t('Cost price saved'))
      setSheetOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
      void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
    } catch (error) {
      handleServerError(error)
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }

  const removeCostPrice = async (item: ProfitCostPrice) => {
    if (deleting) return
    setDeleting(true)
    try {
      const res = await profitQuery(() => deleteProfitCostPrice(item.id))
      if (!res) return
      requireServerSuccess(res)
      toast.success(t('Cost price disabled'))
      setDeleteTarget(null)
      void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
      void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
    } catch (error) {
      handleServerError(error)
    } finally {
      setDeleting(false)
    }
  }

  const handlePasswordSuccess = () => {
    setPasswordOpen(false)
    void queryClient.invalidateQueries({ queryKey: ['profit-password-status'] })
    void queryClient.invalidateQueries({ queryKey: ['profit-summary'] })
    void queryClient.invalidateQueries({ queryKey: ['profit-cost-prices'] })
  }

  return (
    <>
      <SectionPageLayout contentRef={contentRef}>
        <SectionPageLayout.Title>{t('Profit')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button size='sm' onClick={() => openCreateSheet()}>
            <Plus className='size-4' />
            {t('Cost price')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-3'>
            {passwordStatusQuery.data === false && (
              <div className='flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200'>
                <span>{t('Profit password is not configured')}</span>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() =>
                    showPasswordDialog('PROFIT_PASSWORD_NOT_CONFIGURED')
                  }
                >
                  <ShieldCheck className='size-4' />
                  {t('Set profit access password')}
                </Button>
              </div>
            )}

            <Tabs
              value={activeTab}
              onValueChange={setActiveTab}
              className='min-w-0 gap-5'
            >
              <TabsList variant='line' aria-label={t('Profit')}>
                <TabsTrigger value='overview'>
                  {t('Profit overview')}
                </TabsTrigger>
                <TabsTrigger value='costs'>{t('Cost management')}</TabsTrigger>
              </TabsList>
              <TabsContent value='overview' className='min-w-0 space-y-5'>
                <div className='space-y-3'>
                  <div className='flex flex-wrap items-center justify-between gap-3'>
                    <ToggleGroup
                      aria-label={t('Date range')}
                      variant='outline'
                      value={quickRange ? [quickRange] : []}
                      onValueChange={(values) => {
                        if (!values[0]) return
                        setQuickRange(values[0])
                        setStart(defaultStart(Number(values[0])))
                        setEnd(nowSeconds())
                      }}
                    >
                      {[7, 30, 90].map((days) => (
                        <ToggleGroupItem key={days} value={String(days)}>
                          {t('Last {{days}} days', { days })}
                        </ToggleGroupItem>
                      ))}
                    </ToggleGroup>
                    <div className='flex items-center gap-2'>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          setDraftStart(timestampToDate(start))
                          setDraftEnd(timestampToDate(end))
                          setDraftRange(range)
                          setDraftChannelId(channelId)
                          setDraftModelName(modelName)
                          setFiltersOpen(true)
                        }}
                      >
                        <SlidersHorizontal className='size-4' />
                        {t('Filters')}
                        {(channelId || modelName) && (
                          <span className='bg-primary text-primary-foreground rounded px-1.5 text-xs'>
                            {Number(!!channelId) + Number(!!modelName)}
                          </span>
                        )}
                      </Button>
                      <Button
                        variant='ghost'
                        size='icon-sm'
                        aria-label={t('Refresh')}
                        title={t('Refresh')}
                        disabled={reportQuery.isFetching}
                        onClick={() => {
                          const nextEnd = nowSeconds()
                          if (quickRange && nextEnd !== end) {
                            setStart(defaultStart(Number(quickRange)))
                            setEnd(nextEnd)
                            return
                          }
                          void reportQuery.refetch()
                        }}
                      >
                        <RefreshCw
                          className={
                            reportQuery.isFetching
                              ? 'size-4 animate-spin'
                              : 'size-4'
                          }
                        />
                      </Button>
                    </div>
                  </div>
                  <div className='text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-2 text-xs'>
                    <span>
                      {t('Applied range')}: {toDateInput(start)} –{' '}
                      {toDateInput(end)}
                    </span>
                    <span>
                      {
                        {
                          day: t('Daily'),
                          week: t('Weekly'),
                          month: t('Monthly'),
                        }[range]
                      }
                    </span>
                    {channelId && (
                      <span className='bg-muted max-w-full truncate rounded px-2 py-1'>
                        {channelOptions.find(
                          (option) => option.value === channelId
                        )?.label ?? `#${channelId}`}
                      </span>
                    )}
                    {modelName && (
                      <span
                        className='bg-muted max-w-full truncate rounded px-2 py-1'
                        title={modelName}
                      >
                        {modelName}
                      </span>
                    )}
                    {(channelId ||
                      modelName ||
                      quickRange !== '30' ||
                      range !== 'day') && (
                      <Button
                        variant='link'
                        size='sm'
                        className='h-auto p-0 text-xs'
                        onClick={() => {
                          setStart(defaultStart())
                          setEnd(nowSeconds())
                          setRange('day')
                          setChannelId('')
                          setModelName('')
                          setQuickRange('30')
                        }}
                      >
                        {t('Reset')}
                      </Button>
                    )}
                  </div>
                </div>
                {reportQuery.isError && (
                  <ErrorState
                    className='min-h-32'
                    onRetry={() => void reportQuery.refetch()}
                  />
                )}
                {reportQuery.isPending && <LoadingState />}
                {!reportQuery.isPending && !reportQuery.isError && report && (
                  <ProfitOverview
                    report={report}
                    scrollContainerRef={contentRef}
                    onConfigure={openCreateSheet}
                  />
                )}
              </TabsContent>
              <TabsContent value='costs' className='min-w-0'>
                <CostPriceTable
                  prices={pricesQuery.data ?? []}
                  loading={pricesQuery.isPending}
                  error={pricesQuery.isError}
                  refreshing={pricesQuery.isFetching}
                  onRefresh={() => void pricesQuery.refetch()}
                  search={priceSearch}
                  onSearchChange={setPriceSearch}
                  status={priceStatus}
                  onStatusChange={setPriceStatus}
                  onEdit={openEditSheet}
                  onDisable={setDeleteTarget}
                />
              </TabsContent>
            </Tabs>
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <FormDialog
        open={filtersOpen}
        onOpenChange={setFiltersOpen}
        title={t('Filters')}
        description={t(
          'Choose the period, channel and model for your profit report.'
        )}
        footer={
          <>
            <Button variant='outline' onClick={() => setFiltersOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button type='submit' form='profit-filter-form'>
              {t('Apply filters')}
            </Button>
          </>
        }
      >
        <form
          id='profit-filter-form'
          className='grid gap-4 sm:grid-cols-2'
          onSubmit={(event) => {
            event.preventDefault()
            applyFilters()
          }}
        >
          <div className='space-y-1.5'>
            <Label htmlFor='profit-start-date'>{t('Start date')}</Label>
            <DatePicker
              id='profit-start-date'
              aria-label={t('Start date')}
              className='w-full'
              selected={draftStart}
              onSelect={setDraftStart}
              placeholder={t('Start date')}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='profit-end-date'>{t('End date')}</Label>
            <DatePicker
              id='profit-end-date'
              aria-label={t('End date')}
              className='w-full'
              selected={draftEnd}
              onSelect={setDraftEnd}
              placeholder={t('End date')}
            />
          </div>
          <div className='space-y-1.5 sm:col-span-2'>
            <Label htmlFor='profit-range'>{t('Time granularity')}</Label>
            <Select
              items={[
                { value: 'day', label: t('Daily') },
                { value: 'week', label: t('Weekly') },
                { value: 'month', label: t('Monthly') },
              ]}
              value={draftRange}
              onValueChange={(value) => setDraftRange(value as ProfitRange)}
            >
              <SelectTrigger id='profit-range' className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='day'>{t('Daily')}</SelectItem>
                <SelectItem value='week'>{t('Weekly')}</SelectItem>
                <SelectItem value='month'>{t('Monthly')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='profit-channel-filter'>{t('Channel')}</Label>
            <Combobox
              id='profit-channel-filter'
              aria-label={t('Channel')}
              className='w-full'
              options={[
                { value: '', label: t('All channels') },
                ...channelOptions,
              ]}
              placeholder={t('Search channel name or ID')}
              value={draftChannelId}
              onValueChange={(value) => setDraftChannelId(value ?? '')}
            />
          </div>
          <div className='space-y-1.5'>
            <Label htmlFor='profit-model-filter'>{t('Model')}</Label>
            <Input
              id='profit-model-filter'
              autoComplete='off'
              placeholder={t('Model name')}
              value={draftModelName}
              onChange={(event) => setDraftModelName(event.target.value)}
            />
          </div>
          {channelsQuery.isError && (
            <ErrorState
              className='min-h-24 sm:col-span-2'
              onRetry={() => void channelsQuery.refetch()}
            />
          )}
        </form>
      </FormDialog>

      <FormDialog
        open={sheetOpen}
        onOpenChange={(open) => {
          if (!saving) setSheetOpen(open)
        }}
        title={sheetMode === 'edit' ? t('Edit cost price') : t('Cost price')}
        description={t(
          'Configure simple encrypted upstream cost pricing by channel and model.'
        )}
        showCloseButton={!saving}
        contentClassName='sm:max-w-2xl'
        footer={
          <>
            <Button
              variant='outline'
              disabled={saving}
              onClick={() => setSheetOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              form='profit-cost-form'
              disabled={saving || !form.channel_id || !form.model_name}
            >
              <Save className='size-4' />
              {saving ? t('Saving...') : t('Save')}
            </Button>
          </>
        }
      >
        <form
          id='profit-cost-form'
          onSubmit={(event) => {
            event.preventDefault()
            void submitCostPrice()
          }}
        >
          <fieldset disabled={saving} className='min-w-0 space-y-4'>
            <div className='grid gap-4 sm:grid-cols-2'>
              <div className='space-y-1.5'>
                <Label htmlFor='profit-cost-channel'>{t('Channel')}</Label>
                <Combobox
                  id='profit-cost-channel'
                  aria-label={t('Channel')}
                  options={channelOptions}
                  value={form.channel_id}
                  onValueChange={(value) => updateCostPriceChannel(value ?? '')}
                  disabled={
                    saving ||
                    sheetMode === 'edit' ||
                    channelsQuery.isPending ||
                    channelsQuery.isError
                  }
                  placeholder={t('Search channel name or ID')}
                  className='w-full'
                />
              </div>
              <div className='space-y-1.5'>
                <Label htmlFor='profit-cost-model'>{t('Model')}</Label>
                <Combobox
                  id='profit-cost-model'
                  aria-label={t('Model')}
                  options={formModels.map((model) => ({
                    value: model,
                    label: model,
                  }))}
                  value={form.model_name}
                  onValueChange={(value) => updateCostPriceModel(value ?? '')}
                  disabled={
                    saving ||
                    sheetMode === 'edit' ||
                    !form.channel_id ||
                    formModels.length === 0
                  }
                  placeholder={
                    form.channel_id
                      ? t('Select model')
                      : t('Select channel first')
                  }
                  className='w-full'
                />
              </div>
            </div>
            {channelsQuery.isPending && (
              <LoadingState inline message={t('Loading...')} />
            )}
            {channelsQuery.isError && (
              <ErrorState
                className='min-h-24'
                onRetry={() => void channelsQuery.refetch()}
              />
            )}
            {form.channel_id &&
              !channelsQuery.isPending &&
              !channelsQuery.isError &&
              formModels.length === 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'This channel has no configured models. Please update the channel model list first.'
                  )}
                </p>
              )}
            <CostPriceFields
              form={form}
              currency={profitMoneyUnit.label}
              onChange={(patch) =>
                setForm((previous) => ({ ...previous, ...patch }))
              }
            />
            {form.model_name && prefillQuery.isPending && (
              <LoadingState inline message={t('Loading...')} />
            )}
            {form.model_name && prefillQuery.isError && (
              <ErrorState
                className='min-h-24'
                onRetry={() => void prefillQuery.refetch()}
              />
            )}
            {form.model_name && prefillQuery.data?.has_pricing && (
              <div className='bg-muted/30 rounded-lg border p-3 text-xs'>
                <div className='flex items-center gap-1.5 font-medium'>
                  <Calculator className='size-4' />
                  {t('Selling price reference')}
                </div>
                <p className='text-muted-foreground mt-2 break-words'>
                  {sellingPriceReference(prefillQuery.data, t)}
                </p>
                <p className='text-muted-foreground mt-2'>
                  {t(
                    'Reference from legacy pricing settings only; tiered and dynamic prices may differ. Enter your actual upstream cost.'
                  )}
                </p>
              </div>
            )}
            {form.model_name &&
              prefillQuery.data &&
              !prefillQuery.data.has_pricing && (
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'No selling price found for this model. Please enter cost price manually.'
                  )}
                </p>
              )}
          </fieldset>
        </form>
      </FormDialog>
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open && !deleting) setDeleteTarget(null)
        }}
        title={t('Disable cost price?')}
        desc={t(
          'Disable the cost price for {{model}} on channel #{{id}}? Historical usage before disabling keeps its cost price.',
          { model: deleteTarget?.model_name, id: deleteTarget?.channel_id }
        )}
        confirmText={t('Disable')}
        destructive
        isLoading={deleting}
        handleConfirm={() => {
          if (deleteTarget) void removeCostPrice(deleteTarget)
        }}
      />

      <ProfitPasswordDialog
        open={passwordOpen}
        mode={passwordMode}
        onOpenChange={setPasswordOpen}
        onVerificationRequired={showPasswordDialog}
        onSuccess={handlePasswordSuccess}
      />
    </>
  )
}

function sellingPriceReference(
  prefill: ProfitCostPricePrefill,
  t: (key: string) => string
) {
  const parts: string[] = []
  if (prefill.pricing_mode === 'simple_token') {
    if (prefill.input_price > 0) {
      parts.push(
        `${t('Input')} ${formatMoney(prefill.input_price)} / 1M tokens`
      )
    }
    if (prefill.cache_read_price > 0) {
      parts.push(
        `${t('Cache Read')} ${formatMoney(prefill.cache_read_price)} / 1M tokens`
      )
    }
    if (prefill.cache_creation_price > 0) {
      parts.push(
        `${t('Cache Creation')} ${formatMoney(prefill.cache_creation_price)} / 1M tokens`
      )
    }
    if (prefill.cache_creation_1h_price > 0) {
      parts.push(
        `${t('Cache Creation (1h)')} ${formatMoney(prefill.cache_creation_1h_price)} / 1M tokens`
      )
    }
    if (prefill.image_input_price > 0) {
      parts.push(
        `${t('Image input')} ${formatMoney(prefill.image_input_price)} / 1M tokens`
      )
    }
    if (prefill.image_output_price > 0) {
      parts.push(
        `${t('Image Output')} ${formatMoney(prefill.image_output_price)} / 1M tokens`
      )
    }
    if (prefill.audio_input_price > 0) {
      parts.push(
        `${t('Audio Input')} ${formatMoney(prefill.audio_input_price)} / 1M tokens`
      )
    }
    if (prefill.audio_output_price > 0) {
      parts.push(
        `${t('Audio Output')} ${formatMoney(prefill.audio_output_price)} / 1M tokens`
      )
    }
    if (prefill.output_price > 0) {
      parts.push(
        `${t('Output')} ${formatMoney(prefill.output_price)} / 1M tokens`
      )
    }
  }
  if (prefill.request_price > 0) {
    parts.push(`${t('Per request')} ${formatMoney(prefill.request_price)}`)
  }
  if (prefill.second_price > 0) {
    parts.push(`${t('Per second')} ${formatMoney(prefill.second_price)}`)
  }
  return parts.join(' / ') || '-'
}

function ProfitPasswordDialog(props: {
  open: boolean
  mode: ProfitPasswordMode
  onOpenChange: (open: boolean) => void
  onVerificationRequired: (code: string) => void
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
    } catch (error) {
      const code = (error as { response?: { data?: { code?: string } } })
        ?.response?.data?.code
      if (
        code === 'PROFIT_PASSWORD_NOT_CONFIGURED' ||
        code === 'PROFIT_VERIFICATION_REQUIRED' ||
        code === 'PROFIT_VERIFICATION_EXPIRED'
      ) {
        setPassword('')
        setConfirmPassword('')
        props.onVerificationRequired(code)
      }
      handleServerError(error, t('Request failed'))
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
            {isSetup
              ? t('Set profit access password')
              : t('Enter profit access password')}
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
              aria-label={t('Profit access password')}
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
                aria-label={t('Confirm profit access password')}
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

function Field(props: { label: string; children: ReactNode }) {
  return (
    <div className='flex flex-col gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
    </div>
  )
}
