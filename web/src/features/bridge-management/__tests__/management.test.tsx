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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { BridgeManagement } from '../index'
import type {
  BridgeManagementData,
  BridgeRuntimeConfig,
  SaveBridgePolicy,
} from '../types'

let client: QueryClient
let saved: BridgeManagementData

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  saved = {
    version: 3,
    policy: {
      enabled: true,
      origin_base_url: 'https://www.oopii.cc',
      bridge_base_url: 'https://bridge.opics.top',
      cooldown_failure_count: 2,
      min_savings_ratio: 0.3,
      probe_max_bytes: 4194304,
    },
    fields: [
      {
        key: 'cooldown_failure_count',
        group: 'routing',
        unit: 'count',
        min: 1,
        max: 6,
        default_value: 2,
      },
      {
        key: 'min_savings_ratio',
        group: 'budget',
        unit: 'ratio',
        min: 0.1,
        max: 0.9,
        default_value: 0.3,
      },
      {
        key: 'probe_max_bytes',
        group: 'probe',
        unit: 'bytes',
        min: 524288,
        max: 8388608,
        default_value: 4194304,
      },
    ],
    supported: {
      request_recovery: false,
      download_routing: true,
      bridge_storage: false,
      domestic_asset_retention: false,
    },
    last_check: null,
    history: [],
  }
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: saved },
  }))
})

afterEach(async () => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  await i18next.changeLanguage('en')
})

function show(role: number = ROLE.SUPER_ADMIN) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'administrator', role })
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/bridge-management'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <BridgeManagement />
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

async function editPolicy(value: string = '4') {
  const input = await screen.findByRole('spinbutton', {
    name: 'Failures before route cooldown (count)',
  })
  fireEvent.change(input, { target: { value } })
  fireEvent.change(screen.getByRole('textbox', { name: 'Change reason' }), {
    target: { value: '  Improve unstable route  ' },
  })
  return input
}

describe('bridge policy permissions and state', () => {
  it('shows disabled relay storage without inventing capacity measurements', async () => {
    show(ROLE.ADMIN)
    expect(
      await screen.findByText(
        'Relay direct upload is not enabled on this node.'
      )
    ).toBeVisible()
    expect(screen.queryByText(/Stored:.*MiB/)).not.toBeInTheDocument()
  })

  it('shows actual reference storage capacity and blocked admission', async () => {
    saved.reference_storage = {
      enabled: true,
      available: false,
      measured: true,
      stored_bytes: 1048576,
      reserved_bytes: 2097152,
      ready_assets: 3,
      active_uploads: 1,
      max_cache_bytes: 33554432,
      max_user_cache_bytes: 16777216,
      free_bytes: 1073741824,
      required_free_bytes: 2147483648,
      retention_seconds: 1200,
    }
    show(ROLE.ADMIN)
    expect(
      await screen.findByText('Stored: 1 MiB · Reserved: 2 MiB · Limit: 32 MiB')
    ).toBeVisible()
    expect(
      screen.getByText('Ready references: 3 · Active uploads: 1')
    ).toBeVisible()
    expect(
      screen.getByText('Available disk space: 1 GiB · Required reserve: 2 GiB')
    ).toBeVisible()
    expect(screen.getByText('Unavailable')).toBeVisible()
  })

  it('ordinary administrators see read-only policy and honest unavailable states', async () => {
    show(ROLE.ADMIN)
    expect(
      await screen.findByRole('spinbutton', {
        name: 'Failures before route cooldown (count)',
      })
    ).toBeDisabled()
    const admission = screen.getByRole('switch', {
      name: 'Allow bridge for new client operations',
    })
    expect(admission).toHaveAttribute('aria-disabled', 'true')
    await userEvent.click(admission)
    expect(admission).toHaveAttribute('aria-checked', 'true')
    expect(
      screen.getByRole('textbox', { name: 'Bridge API URL' })
    ).toHaveAttribute('readonly')
    expect(
      screen.queryByRole('button', { name: 'Save bridge policy' })
    ).not.toBeInTheDocument()
    expect(screen.getByText('Not checked yet')).toBeVisible()
    expect(
      screen.getByText(
        /Client fetched version and active task version are unknown/
      )
    ).toBeVisible()
    expect(
      screen.getByText(
        'Bridge runtime status is unavailable; cache and transfer counters are unknown'
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('button', { name: /cleanup/i })
    ).not.toBeInTheDocument()
  })

  it('super administrators save the loaded version, unchanged URLs and byte values', async () => {
    const put = vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
      saved = {
        ...saved,
        version: 4,
        policy: (body as SaveBridgePolicy).policy,
      }
      return { data: { success: true, data: saved } }
    })
    show()
    await editPolicy()
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await screen.findByText('Saved server policy: v4')
    expect(put).toHaveBeenCalledWith('/api/bridge-management', {
      base_version: 3,
      policy: { ...saved.policy, cooldown_failure_count: 4 },
      reason: 'Improve unstable route',
    })
    expect(screen.getByRole('textbox', { name: 'Change reason' })).toHaveValue(
      ''
    )
  })

  it('a version conflict preserves the draft until explicit confirmed reload', async () => {
    vi.spyOn(api, 'put').mockRejectedValue({
      response: { status: 409, data: { message: 'Policy revision conflict' } },
    })
    show()
    const input = await editPolicy()
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await screen.findByText('The saved policy has changed')
    expect(input).toHaveValue(4)
    expect(screen.getByRole('textbox', { name: 'Change reason' })).toHaveValue(
      '  Improve unstable route  '
    )
    expect(
      screen.getByRole('button', { name: 'Save bridge policy' })
    ).toBeDisabled()
    saved = {
      ...saved,
      version: 5,
      policy: { ...saved.policy, cooldown_failure_count: 5 },
    }
    await userEvent.click(
      screen.getByRole('button', { name: 'Reload saved policy' })
    )
    expect(input).toHaveValue(4)
    await userEvent.click(screen.getByRole('button', { name: 'Replace draft' }))
    await screen.findByText('Editing base version: v5')
    expect(input).toHaveValue(5)
  })

  it('a check refreshes reachability without replacing an unsaved policy', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...saved,
          last_check: {
            checked_at: 1792454400,
            ok: false,
            latency_ms: 0,
            error: 'Bridge connection timed out',
          },
        },
      },
    })
    show()
    const input = await editPolicy()
    await userEvent.click(
      screen.getByRole('button', { name: 'Run free bridge check' })
    )
    await screen.findByText('Bridge connection timed out')
    expect(input).toHaveValue(4)
    expect(screen.queryByText('Latency: 0 ms')).not.toBeInTheDocument()
  })

  it('a check completing after a save cannot roll the displayed version backward', async () => {
    const checked = {
      ...saved,
      last_check: {
        checked_at: 1792454400,
        ok: true,
        latency_ms: 42,
        error: '',
      },
    }
    let finishCheck!: () => void
    const waitForCheck = new Promise<void>((resolve) => {
      finishCheck = resolve
    })
    vi.spyOn(api, 'post').mockImplementation(async () => {
      await waitForCheck
      return { data: { success: true, data: checked } }
    })
    vi.spyOn(api, 'put').mockImplementation(async (_url, body) => ({
      data: {
        success: true,
        data: {
          ...saved,
          version: 4,
          policy: (body as SaveBridgePolicy).policy,
        },
      },
    }))
    show()
    await editPolicy()
    await userEvent.click(
      screen.getByRole('button', { name: 'Run free bridge check' })
    )
    expect(screen.getByRole('button', { name: 'Checking...' })).toBeDisabled()
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await screen.findByText('Saved server policy: v4')
    await act(async () => finishCheck())
    await screen.findByText('Latency: 42 ms')
    expect(screen.getByText('Saved server policy: v4')).toBeVisible()
    expect(
      screen.queryByText('The saved policy has changed')
    ).not.toBeInTheDocument()
  })

  it('server-supplied ranges reject invalid values without sending a save', async () => {
    const put = vi.spyOn(api, 'put')
    show()
    const input = await editPolicy('7')
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await screen.findByText('Enter a valid value between 1 and 6')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(put).not.toHaveBeenCalled()
  })

  it('historical policy loads as a draft and saves against the current version', async () => {
    const historical = {
      ...saved.policy,
      enabled: false,
      cooldown_failure_count: 1,
    }
    saved.history = [
      {
        version: 1,
        changed_at: 1792454400,
        changed_by: 1,
        reason: 'Initial setup',
        policy: historical,
      },
    ]
    const put = vi.spyOn(api, 'put').mockImplementation(async () => ({
      data: {
        success: true,
        data: { ...saved, version: 4, policy: historical },
      },
    }))
    show()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Load v1 as draft' })
    )
    expect(put).not.toHaveBeenCalled()
    expect(
      screen.getByRole('spinbutton', {
        name: 'Failures before route cooldown (count)',
      })
    ).toHaveValue(1)
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/bridge-management', {
        base_version: 3,
        policy: historical,
        reason: 'Restore policy version 1',
      })
    )
  })

  it('a business save failure retains the draft and the last saved version', async () => {
    vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: false, message: 'Invalid policy combination' },
    })
    show()
    const input = await editPolicy()
    await userEvent.click(
      screen.getByRole('button', { name: 'Save bridge policy' })
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save bridge policy' })
      ).toBeEnabled()
    )
    expect(input).toHaveValue(4)
    expect(screen.getByText('Saved server policy: v3')).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Change reason' })).toHaveValue(
      '  Improve unstable route  '
    )
  })

  it('a failed initial load exposes retry without editable fallback defaults', async () => {
    vi.mocked(api.get).mockRejectedValueOnce(new Error('Unavailable'))
    show()
    await screen.findByText('Failed to load bridge settings')
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Run free bridge check' })
    ).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('spinbutton', {
        name: 'Failures before route cooldown (count)',
      })
    ).toHaveValue(2)
  })

  it('switching to Chinese updates the page and dynamic field labels', async () => {
    i18next.addResourceBundle('zhCN', 'translation', zh.translation)
    show()
    await screen.findByRole('spinbutton', {
      name: 'Failures before route cooldown (count)',
    })
    await act(async () => {
      await i18next.changeLanguage('zhCN')
    })
    expect(screen.getByRole('heading', { name: '桥接管理' })).toBeVisible()
    expect(
      screen.getByRole('spinbutton', { name: /线路进入冷却前的失败次数/ })
    ).toHaveValue(2)
    expect(screen.getByRole('button', { name: '保存桥接策略' })).toBeVisible()
  })
})

describe('bridge runtime status and configuration', () => {
  beforeEach(() => {
    saved.runtime = {
      available: true,
      checked_at: 1792500000,
      status: {
        version: 2,
        applied_revision: 5,
        config: {
          revision: 5,
          image_cache_enabled: true,
          image_cache_max_bytes: 1024 * 1048576,
          image_cache_max_file_bytes: 32 * 1048576,
          max_resume_attempts: 2,
        },
        image_cache_ttl_seconds: 14400,
        video_disk_cache: false,
        cache: {
          entries: 3,
          bytes: 1048576,
          reserved_bytes: 0,
          active_readers: 1,
        },
        active_downloads: 2,
        metrics: {
          requests: 12,
          upstream_requests: 8,
          relay_requests: 3,
          unknown_route_requests: 0,
          bytes_sent: 10485760,
          upstream_bytes: 5242880,
          relay_bytes: 4194304,
          cache_bytes: 1048576,
          cache_hits: 1,
          cache_misses: 2,
          cache_writes: 2,
          cache_errors: 0,
          resume_attempts: 2,
          resume_successes: 1,
          resume_rejected: 1,
          stream_failures: 1,
          cancellations: 0,
        },
      },
    }
  })

  function enableCapacity() {
    const status = saved.runtime?.status
    if (!status) throw new Error('Runtime fixture missing')
    status.version = 3
    status.config.capacity = {
      upload: {
        global: 8,
        per_account: 2,
        queue: 16,
        per_account_queue: 4,
        wait_ms: 2000,
      },
      download: {
        global: 8,
        per_account: 2,
        queue: 16,
        per_account_queue: 4,
        wait_ms: 2000,
      },
      bandwidth_bytes_per_second: 4 * 1048576,
      api_reserved_bytes_per_second: 512 * 1024,
    }
    status.capacity = {
      upload: {
        active: 3,
        queued: 5,
        rejected: 7,
        timed_out: 2,
        cancelled: 1,
        draining: true,
      },
      download: {
        active: 2,
        queued: 0,
        rejected: 0,
        timed_out: 0,
        cancelled: 0,
        draining: false,
      },
    }
  }

  it('shows real queue counts and read-only limits to ordinary administrators', async () => {
    enableCapacity()
    show(ROLE.ADMIN)
    expect(
      await screen.findByText(
        'Active: 3; queued: 5; rejected: 7; timed out: 2; cancelled: 1'
      )
    ).toBeVisible()
    expect(
      screen.getByText('Existing transfers are draining to the new limits')
    ).toBeVisible()
    const uploads = screen.getByRole('spinbutton', {
      name: 'Uploads: Global active transfers',
    })
    expect(uploads).toHaveValue(8)
    expect(uploads).toBeDisabled()
    expect(
      screen.getByRole('spinbutton', {
        name: 'Downloads: Maximum queued transfers',
      })
    ).toHaveValue(16)
    expect(
      screen.queryByRole('button', { name: 'Apply bridge runtime settings' })
    ).not.toBeInTheDocument()
  })

  it('saves both pools and bandwidth together and waits for the acknowledged revision', async () => {
    enableCapacity()
    const post = vi
      .spyOn(api, 'post')
      .mockImplementation(async (_url, body) => {
        const request = body as { config: BridgeRuntimeConfig }
        const status = saved.runtime?.status
        if (!status) throw new Error('Runtime fixture missing')
        status.config = request.config
        status.applied_revision = request.config.revision
        return { data: { success: true, data: structuredClone(saved) } }
      })
    show()
    await screen.findByText('Applied bridge configuration: v5')
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Uploads: Global active transfers',
      }),
      { target: { value: '6' } }
    )
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Downloads: Maximum queue wait (seconds)',
      }),
      { target: { value: '1.5' } }
    )
    fireEvent.change(
      screen.getByRole('spinbutton', {
        name: 'Bandwidth reserved for task requests (MiB/s)',
      }),
      { target: { value: '1' } }
    )
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Runtime change reason' }),
      { target: { value: 'Reserve task bandwidth' } }
    )
    await userEvent.click(
      screen.getByRole('button', { name: 'Apply bridge runtime settings' })
    )
    await screen.findByText('Applied bridge configuration: v6')
    expect(post).toHaveBeenCalledOnce()
    expect(post).toHaveBeenCalledWith(
      '/api/bridge-management/runtime',
      expect.objectContaining({
        reason: 'Reserve task bandwidth',
        config: expect.objectContaining({
          revision: 6,
          capacity: {
            upload: {
              global: 6,
              per_account: 2,
              queue: 16,
              per_account_queue: 4,
              wait_ms: 2000,
            },
            download: {
              global: 8,
              per_account: 2,
              queue: 16,
              per_account_queue: 4,
              wait_ms: 1500,
            },
            bandwidth_bytes_per_second: 4 * 1048576,
            api_reserved_bytes_per_second: 1048576,
          },
        }),
      })
    )
  })

  it('applies the upload burst adjustment without changing other runtime settings', async () => {
    enableCapacity()
    const original = structuredClone(saved.runtime!.status!.config)
    const post = vi.spyOn(api, 'post').mockImplementation(async (_url, body) => {
      const request = body as { config: BridgeRuntimeConfig }
      const status = saved.runtime!.status!
      status.config = request.config
      status.applied_revision = request.config.revision
      return { data: { success: true, data: structuredClone(saved) } }
    })
    show()
    await screen.findByText('Applied bridge configuration: v5')
    for (const name of [
      'Uploads: Active transfers per account',
      'Uploads: Maximum queue wait (seconds)',
    ]) {
      fireEvent.change(screen.getByRole('spinbutton', { name }), {
        target: { value: '3' },
      })
    }
    fireEvent.change(screen.getByRole('textbox', { name: 'Runtime change reason' }), {
      target: { value: 'Increase upload burst allowance' },
    })
    await userEvent.click(screen.getByRole('button', { name: 'Apply bridge runtime settings' }))
    await screen.findByText('Applied bridge configuration: v6')
    expect(post).toHaveBeenCalledOnce()
    expect(post).toHaveBeenCalledWith('/api/bridge-management/runtime', {
      reason: 'Increase upload burst allowance',
      config: {
        ...original,
        revision: 6,
        capacity: {
          ...original.capacity,
          upload: { ...original.capacity!.upload, per_account: 3, wait_ms: 3000 },
        },
      },
    })
  })

  it.each([
    ['Uploads: Active transfers per account', '9'],
    ['Uploads: Maximum queued transfers', '0'],
    ['Downloads: Maximum queue wait (seconds)', '4'],
    ['Bandwidth reserved for task requests (MiB/s)', '4'],
  ])('rejects inconsistent capacity input %s', async (name, value) => {
    enableCapacity()
    const post = vi.spyOn(api, 'post')
    show()
    const field = await screen.findByRole('spinbutton', { name })
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Runtime change reason' }),
      { target: { value: 'Invalid setting' } }
    )
    fireEvent.change(field, { target: { value } })
    expect(screen.getByText(/^Invalid capacity settings:/)).toHaveAttribute(
      'role',
      'alert'
    )
    expect(
      screen.getByRole('button', { name: 'Apply bridge runtime settings' })
    ).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
  })

  it('translates queue labels and live counts when switching to Chinese', async () => {
    enableCapacity()
    i18next.addResourceBundle('zhCN', 'translation', zh.translation)
    show()
    await screen.findByText('Applied bridge configuration: v5')
    await act(async () => {
      await i18next.changeLanguage('zhCN')
    })
    expect(
      screen.getByRole('spinbutton', { name: '上传: 全局传输并发数' })
    ).toHaveValue(8)
    expect(
      screen.getByText('传输中：3；排队：5；拒绝：7；等待超时：2；排队取消：1')
    ).toBeVisible()
  })

  it('does not offer made-up queue defaults on older gateways', async () => {
    show()
    expect(
      await screen.findByText(
        'This bridge version does not report configurable capacity or queues. Upgrade the gateway before editing these limits.'
      )
    ).toBeVisible()
    expect(
      screen.queryByRole('spinbutton', {
        name: 'Uploads: Global active transfers',
      })
    ).not.toBeInTheDocument()
  })

  it('shows measured paths and keeps non-root runtime controls read only', async () => {
    show(ROLE.ADMIN)
    expect(
      await screen.findByText('Applied bridge configuration: v5')
    ).toBeVisible()
    expect(screen.getByText('Bridge directly fetching upstream')).toBeVisible()
    expect(screen.getByText('Bridge fetching through relay')).toBeVisible()
    expect(
      screen.getByRole('spinbutton', { name: 'Image cache capacity (MiB)' })
    ).toBeDisabled()
    expect(
      screen.queryByRole('button', { name: 'Apply bridge runtime settings' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(
        /Original-route downloads and client fallback reasons are not observed here/
      )
    ).toBeVisible()
  })

  it('sends next runtime revision and keeps failed changes unapplied', async () => {
    const post = vi
      .spyOn(api, 'post')
      .mockRejectedValueOnce(new Error('revision conflict'))
    show()
    await screen.findByText('Applied bridge configuration: v5')
    const apply = screen.getByRole('button', {
      name: 'Apply bridge runtime settings',
    })
    expect(apply).toBeDisabled()
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Runtime change reason' }),
      { target: { value: 'Reduce disk use' } }
    )
    fireEvent.change(
      screen.getByRole('spinbutton', { name: 'Image cache capacity (MiB)' }),
      { target: { value: '512' } }
    )
    await userEvent.click(apply)
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/bridge-management/runtime', {
        config: {
          ...saved.runtime?.status?.config,
          revision: 6,
          image_cache_max_bytes: 512 * 1048576,
        },
        reason: 'Reduce disk use',
      })
    )
    expect(screen.getByText('Applied bridge configuration: v5')).toBeVisible()
    expect(
      screen.getByRole('spinbutton', { name: 'Image cache capacity (MiB)' })
    ).toHaveValue(512)
  })

  it('labels all four client paths separately from gateway measurements', async () => {
    const totals = {
      reports: 1,
      bytes: 1048576,
      resumes: 1,
      completed: 1,
      failed: 0,
      cancelled: 0,
      reasons: { slow_route: 1 },
    }
    saved.client_observations = {
      source: 'client_reported',
      started_at: 1792500000,
      routes: {
        bridge_upstream: totals,
        bridge_relay: totals,
        origin_upstream: totals,
        origin_relay: totals,
      },
    }
    show()
    await screen.findByText('Client reported download paths')
    expect(
      screen.getByText('Original route directly fetching upstream')
    ).toBeVisible()
    expect(
      screen.getByText('Original route fetching through relay')
    ).toBeVisible()
    expect(screen.getAllByText('Slow route: 1')).toHaveLength(4)
    expect(
      screen.getByText(/separate from gateway measurements and billing/)
    ).toBeVisible()
  })
})
