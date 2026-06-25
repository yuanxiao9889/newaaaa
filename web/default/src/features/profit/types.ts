export type ProfitRange = 'day' | 'week' | 'month'

export type ProfitMetric = {
  revenue: number
  priced_revenue: number
  unpriced_revenue: number
  cost: number
  profit: number
  profit_margin: number
  coverage_rate: number
  request_count: number
  priced_count: number
  unpriced_count: number
  error_count: number
  prompt_tokens: number
  completion_tokens: number
}

export type ProfitSeriesItem = ProfitMetric & {
  period_start: number
  period_label: string
}

export type ProfitBreakdownItem = ProfitMetric & {
  channel_id: number
  channel_name: string
  model_name: string
}

export type ProfitReport = {
  range: ProfitRange
  start_timestamp: number
  end_timestamp: number
  summary: ProfitMetric
  series: ProfitSeriesItem[]
  breakdown: ProfitBreakdownItem[]
  generated_at: number
  quota_per_unit: number
  currency: string
  has_filters: boolean
}

export type ProfitCostPrice = {
  id: number
  channel_id: number
  channel_name?: string
  model_name: string
  current_version_id: number
  price_type: 'simple_token' | 'tiered_expr' | 'fixed_price'
  fixed_unit?: 'request' | 'second' | ''
  effective_from: number
  created_at: number
  updated_at: number
  disabled: boolean
  disabled_at: number
  price_configured: boolean
  price_value?: string
  price_summary?: string
  input_price?: number
  cache_read_price?: number
  output_price?: number
  request_price?: number
  second_price?: number
}

export type ProfitCostPricePrefill = {
  channel_id: number
  model_name: string
  models: string[]
  pricing_mode: 'simple_token' | 'tiered_expr' | 'fixed_price' | ''
  input_price: number
  cache_read_price: number
  output_price: number
  request_price: number
  second_price: number
  model_ratio?: number
  completion_ratio?: number
  model_price?: number
  has_pricing: boolean
  note?: string
}

export type ProfitQueryParams = {
  range?: ProfitRange
  start_timestamp?: number
  end_timestamp?: number
  channel_id?: number
  model_name?: string
}

export type SaveProfitCostPricePayload = {
  channel_id: number
  model_name: string
  price_type: 'simple_token' | 'tiered_expr' | 'fixed_price'
  price_value?: string
  fixed_unit?: 'request' | 'second'
  fixed_amount?: number
  input_price?: number
  cache_read_price?: number
  output_price?: number
  request_price?: number
  second_price?: number
  effective_from?: number
}
