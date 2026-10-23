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
import type { BridgeCapacityConfig } from './lib/capacity-form'

export type TransferPolicy = {
  enabled: boolean
  origin_base_url: string
  bridge_base_url: string
  [key: string]: number | string | boolean
}

export type FieldSpec = {
  key: string
  group: 'routing' | 'slow' | 'probe' | 'budget'
  unit: 'seconds' | 'bytes' | 'bytes_per_second' | 'count' | 'ratio'
  min: number
  max: number
  default_value: number
}

export type BridgeCheck = {
  checked_at: number | string
  ok: boolean
  latency_ms: number
  error?: string
}

export type PolicyRevision = {
  version: number
  changed_at: number | string
  changed_by: number | string
  reason: string
  policy: TransferPolicy
}

export type BridgeManagementData = {
  version: number
  policy: TransferPolicy
  fields: FieldSpec[]
  supported: {
    request_recovery: boolean
    download_routing: boolean
    bridge_storage: boolean
    domestic_asset_retention: boolean
  }
  last_check: BridgeCheck | null
  history: PolicyRevision[]
  reference_storage?: {
    enabled: boolean
    available: boolean
    measured: boolean
    stored_bytes: number
    reserved_bytes: number
    ready_assets: number
    active_uploads: number
    max_cache_bytes: number
    max_user_cache_bytes: number
    free_bytes: number
    required_free_bytes: number
    retention_seconds: number
  }
  runtime?: {
    checked_at: number
    available: boolean
    error?: string
    status?: BridgeRuntimeStatus
  }
  client_observations?: {
    source: 'client_reported'
    started_at: number
    routes: Record<
      string,
      {
        reports: number
        bytes: number
        resumes: number
        completed: number
        failed: number
        cancelled: number
        reasons: Record<string, number>
      }
    >
  }
}

export type BridgeRuntimeConfig = {
  capacity?: BridgeCapacityConfig
  revision: number
  image_cache_enabled: boolean
  image_cache_max_bytes: number
  image_cache_max_file_bytes: number
  max_resume_attempts: number
}

export type BridgePoolStatus = {
  active: number
  queued: number
  rejected: number
  timed_out: number
  cancelled: number
  draining: boolean
}
export type BridgeCapacityStatus = {
  upload: BridgePoolStatus
  download: BridgePoolStatus
}

export type BridgeRuntimeStatus = {
  capacity?: BridgeCapacityStatus
  version: number
  applied_revision: number
  config: BridgeRuntimeConfig
  image_cache_ttl_seconds: number
  video_disk_cache: boolean
  cache: {
    entries: number
    bytes: number
    reserved_bytes: number
    active_readers: number
    orphan_bytes?: number
  }
  active_downloads: number
  metrics: {
    requests: number
    upstream_requests: number
    relay_requests: number
    unknown_route_requests: number
    unknown_bytes?: number
    bytes_sent: number
    upstream_bytes: number
    relay_bytes: number
    cache_bytes: number
    cache_hits: number
    cache_misses: number
    cache_writes: number
    cache_errors: number
    resume_attempts: number
    resume_successes: number
    resume_rejected: number
    stream_failures: number
    cancellations: number
  }
}

export type SaveBridgePolicy = {
  base_version: number
  policy: TransferPolicy
  reason: string
}
