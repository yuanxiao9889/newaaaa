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
import type {
  TaskLog,
  TaskPluginInfo,
  TaskPluginRuntimeInfo,
  TaskUsageDetails,
} from '../types'

export interface TaskDetailAccess {
  plugin?: TaskPluginInfo
  runtime?: TaskPluginRuntimeInfo
  upstreamTaskId?: string
  nodeName?: string
}

export function resolveTaskDetailAccess(
  log: TaskLog,
  isAdmin: boolean,
  isRoot: boolean
): TaskDetailAccess {
  if (!isAdmin) return {}

  const access: TaskDetailAccess = {
    plugin: log.admin_info?.task_plugin,
  }
  if (!isRoot) return access

  access.runtime = log.root_info?.task_plugin
  access.upstreamTaskId = log.root_info?.upstream_task_id
  access.nodeName = log.root_info?.node_name
  return access
}

export type TokenUsageLike = TaskUsageDetails & {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

export function tokenUsageNumber(value: unknown): number {
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
  ].some((v) => tokenUsageNumber(v) > 0)
}
