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
import type { TFunction } from 'i18next'

export function bridgePolicyLabels(t: TFunction): Record<string, string> {
  return {
    cooldown_failure_count: t('Failures before route cooldown'),
    cooldown_seconds: t('Route cooldown duration'),
    download_header_timeout_seconds: t('Download response header timeout'),
    slow_observation_seconds: t('Initial download observation period'),
    slow_window_seconds: t('Download speed window'),
    slow_window_count: t('Consecutive slow windows'),
    slow_speed_bytes_per_second: t('Slow download speed threshold'),
    slow_remaining_seconds: t('Minimum remaining time for slow detection'),
    near_complete_seconds: t('Near-completion protection'),
    stall_prepare_seconds: t('No-progress time before probing'),
    stall_fail_seconds: t('No-progress failure timeout'),
    unknown_size_probe_after_seconds: t(
      'Probe delay when download size is unknown'
    ),
    probe_max_bytes: t('Maximum bytes per route probe'),
    probe_max_seconds: t('Maximum duration per route probe'),
    probe_min_bytes: t('Minimum sample bytes for speed comparison'),
    probe_limit: t('Maximum probes per download'),
    process_probe_limit: t('Concurrent probes per client process'),
    min_speed_ratio: t('Minimum alternative route speed multiplier'),
    min_savings_seconds: t('Minimum estimated time saving'),
    min_savings_ratio: t('Minimum estimated saving ratio'),
    max_restart_bytes: t('Maximum discarded bytes when restarting'),
    max_restart_ratio: t('Maximum discarded download ratio'),
    max_speed_switches: t('Maximum speed switches per download'),
    max_failure_recoveries: t('Maximum failure recoveries per download'),
    max_primary_requests: t('Maximum main requests per download'),
  }
}

export function bridgePolicyGroupLabels(t: TFunction) {
  return {
    routing: {
      title: t('Bridge routing'),
      description: t(
        'Start new operations on the bridge. Eligible failures may use the original route; an unknown generation submission is never resubmitted.'
      ),
    },
    slow: {
      title: t('Slow download detection'),
      description: t(
        'Observe the initial period, then require consecutive slow windows and enough remaining time. Near-complete downloads are protected; no-progress timeouts are evaluated separately.'
      ),
    },
    probe: {
      title: t('Alternative route probes'),
      description: t(
        'Keep the active download while sampling the other route within these byte, time and concurrency limits. A speed switch requires sufficient sample data and estimated savings.'
      ),
    },
    budget: {
      title: t('Switch and recovery budgets'),
      description: t(
        'Budgets belong to one download and do not reset when routes change. Main requests include the first download; probes have a separate limit. Restarting must satisfy both discarded-byte limits. These settings do not authorize replaying a generation request.'
      ),
    },
  }
}
