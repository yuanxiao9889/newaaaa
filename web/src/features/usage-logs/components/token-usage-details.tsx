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

import {
  hasTokenUsageDetails,
  tokenUsageNumber,
  type TokenUsageLike,
} from '../lib/task-details'

export function TokenUsageDetails(props: {
  usage?: TokenUsageLike | null
  compact?: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const usage = props.usage

  if (!hasTokenUsageDetails(usage)) return null

  const inputDetails = usage?.prompt_tokens_details || {}
  const cacheReadTokens = tokenUsageNumber(inputDetails.cached_tokens)
  const cacheWriteTokens = tokenUsageNumber(inputDetails.cached_creation_tokens)

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
