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
import { useTranslation } from 'react-i18next'

import { formatTokens } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { TaskUsageDetails } from '../types'

type TokenUsageLike = TaskUsageDetails & {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

function numberValue(value: unknown): number {
  const n = Number(value || 0)
  return Number.isFinite(n) && n > 0 ? n : 0
}

export function hasTokenUsageDetails(
  usage: TokenUsageLike | null | undefined
): boolean {
  if (!usage) return false
  const inputDetails = usage.prompt_tokens_details || {}
  const outputDetails = usage.completion_tokens_details || {}
  return [
    usage.prompt_tokens,
    usage.completion_tokens,
    usage.total_tokens,
    inputDetails.text_tokens,
    inputDetails.cached_tokens,
    inputDetails.cached_creation_tokens,
    inputDetails.image_tokens,
    inputDetails.audio_tokens,
    outputDetails.text_tokens,
    outputDetails.image_tokens,
    outputDetails.audio_tokens,
    outputDetails.reasoning_tokens,
  ].some((v) => numberValue(v) > 0)
}

export function TokenUsageDetails(props: {
  usage?: TokenUsageLike | null
  compact?: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const usage = props.usage

  if (!hasTokenUsageDetails(usage)) return null

  const inputDetails = usage?.prompt_tokens_details || {}
  const cacheReadTokens = numberValue(inputDetails.cached_tokens)
  const cacheWriteTokens = numberValue(inputDetails.cached_creation_tokens)

  if (cacheReadTokens === 0 && cacheWriteTokens === 0) return null

  return (
    <div className={cn('flex items-center gap-1 text-[11px]', props.className)}>
      {cacheReadTokens > 0 && (
        <span className='text-muted-foreground/60'>
          {t('Cache')}↓ {formatTokens(cacheReadTokens)}
        </span>
      )}
      {cacheWriteTokens > 0 && (
        <span className='text-muted-foreground/60'>
          ↑ {formatTokens(cacheWriteTokens)}
        </span>
      )}
    </div>
  )
}
