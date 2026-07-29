import { api } from '@/lib/api'
import type {
  ProfitCostPrice,
  ProfitCostPricePrefill,
  ProfitQueryParams,
  ProfitReport,
  SaveProfitCostPricePayload,
} from './types'

const secureConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
  disableDuplicate: true,
}

export async function getProfitSummary(params: ProfitQueryParams = {}) {
  const res = await api.get('/api/profit/summary', {
    params,
    ...secureConfig,
  })
  return res.data as { success: boolean; data: ProfitReport; message?: string }
}

export async function getProfitCostPrices(reveal = false) {
  const res = await api.get('/api/profit/cost-prices', {
    params: reveal ? { reveal: true } : undefined,
    ...secureConfig,
  })
  return res.data as {
    success: boolean
    data: ProfitCostPrice[]
    message?: string
  }
}

export async function getProfitCostPricePrefill(
  channelId: number,
  modelName?: string
) {
  const res = await api.get('/api/profit/cost-price-prefill', {
    params: { channel_id: channelId, model_name: modelName || undefined },
    ...secureConfig,
  })
  return res.data as {
    success: boolean
    data: ProfitCostPricePrefill
    message?: string
  }
}

export async function saveProfitCostPrice(payload: SaveProfitCostPricePayload) {
  const res = await api.post('/api/profit/cost-prices', payload, secureConfig)
  return res.data as { success: boolean; message?: string }
}

export async function deleteProfitCostPrice(id: number) {
  const res = await api.delete(`/api/profit/cost-prices/${id}`, secureConfig)
  return res.data as { success: boolean; message?: string }
}

export async function getProfitPasswordStatus() {
  const res = await api.get('/api/profit/password/status', secureConfig)
  return res.data as {
    success: boolean
    data?: { configured: boolean }
    message?: string
  }
}

export async function verifyProfitPassword(password: string) {
  const res = await api.post('/api/profit/verify', { password }, secureConfig)
  return res.data as { success: boolean; message?: string; code?: string }
}

export async function setProfitPassword(password: string) {
  const res = await api.post('/api/profit/password', { password }, secureConfig)
  return res.data as { success: boolean; message?: string; code?: string }
}
