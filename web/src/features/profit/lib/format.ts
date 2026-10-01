import { formatBillingCurrencyFromUSD } from '@/lib/currency'

export function formatMoney(value: number) {
  return formatBillingCurrencyFromUSD(value || 0, {
    digitsLarge: 2,
    digitsSmall: 6,
    minimumNonZero: 0.000001,
  })
}

export function formatPercent(value: number) {
  return `${((value || 0) * 100).toFixed(2)}%`
}

export function toDateInput(timestamp?: number) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}
