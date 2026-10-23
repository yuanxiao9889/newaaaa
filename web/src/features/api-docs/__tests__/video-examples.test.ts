import { Buffer } from 'node:buffer'
import { runInNewContext } from 'node:vm'

import { describe, expect, it, vi } from 'vitest'

import { generationContracts } from '../generation-data'
import { buildJsonRequestSamples } from '../request-samples'
import { videoGroups } from '../video-data'
import { buildVideoLifecycleSamples } from '../video-samples'

const examples = [
  ...videoGroups.flatMap((group) =>
    group.examples.map((example) => ({ ...example, path: '/v1/videos' }))
  ),
  ...generationContracts.flatMap((contract) => contract.examples),
]

describe('Video documentation request recipes', () => {
  it.each(examples)(
    '$title sends the documented JSON with real bearer interpolation',
    async (example) => {
      const samples = buildJsonRequestSamples(
        'https://relay.test/',
        example.body,
        example.path
      )
      const fetch = vi.fn(async () => ({
        ok: true,
        json: async () => ({ id: 'task_public' }),
      }))
      await runInNewContext(`(async () => {${samples.javascript}})()`, {
        fetch,
        process: { env: { NEW_API_KEY: 'fixture-key' } },
        console: { log: vi.fn() },
        AbortSignal,
      })
      expect(fetch).toHaveBeenCalledWith(
        `https://relay.test${example.path}`,
        expect.objectContaining({
          method: 'POST',
          headers: {
            Authorization: 'Bearer fixture-key',
            'Content-Type': 'application/json',
          },
          body: JSON.stringify(example.body),
        })
      )
      const curlBody =
        samples.curl.split("<<'JSON'\n")[1]?.split('\nJSON')[0] ?? ''
      expect(JSON.parse(curlBody)).toEqual(example.body)
      expect(samples.curl).toContain('-H "Authorization: Bearer $NEW_API_KEY"')
      expect(samples.curl).not.toMatch(/^\+/m)
      const pythonLiteral =
        samples.python.match(/payload = json.loads\((.+)\)/)?.[1] ?? ''
      expect(JSON.parse(JSON.parse(pythonLiteral))).toEqual(example.body)
    }
  )

  it('documents current public aliases without offering hidden adapter IDs as models', () => {
    const models = videoGroups.flatMap((group) =>
      group.variants.flatMap((variant) => variant.models)
    )
    expect(models).toHaveLength(23)
    expect(new Set(models).size).toBe(23)
    expect(models).toEqual(
      expect.arrayContaining([
        '全能视频2.5-301010-c',
        '全能视频2.5-301010-T',
        'wan3.0-video',
        '灵动视频-video-v4',
        '全能视频2.0-903-720-c1',
        '全能视频2.0-930-720-c1',
        '全能视频2.5-301010-1080',
        '全能-fast-10s',
        'Minimax-H3-15s',
        'muse-video',
        '万能视频-3.0-720',
        '全能视频2.5-301010-480',
        '全能视频2.5-301010-720',
      ])
    )
    expect(models).not.toContain('seedance-2.0-mini-deal')
    expect(models).not.toContain('MiniMax-H3')
  })

  it('keeps different duration and reference contracts separate', () => {
    const muse = videoGroups.find((group) => group.id === 'video-muse')
    if (!muse) throw new Error('Missing Muse documentation')
    expect(muse.examples[0].body).toMatchObject({
      duration: 6,
      ratio: '16:9',
      resolution: '720p',
    })
    expect(muse.examples[0].body).not.toHaveProperty('aspect_ratio')
    const variants = videoGroups.flatMap((group) => group.variants)
    expect(
      variants.find((variant) => variant.models.includes('wan3.0-video'))
        ?.constraints
    ).toMatchObject({ maxTotalVideoSeconds: 15, maxVideoAndOutputSeconds: 30 })
    expect(
      variants.find((variant) =>
        variant.models.includes('全能视频2.0-930-720-c1')
      )?.constraints
    ).toMatchObject({ maxImages: 9, maxVideos: 3, maxAudios: 0 })
    expect(
      variants.find((variant) => variant.models.includes('ok-imagine-video'))
    ).toMatchObject({
      resolution: '480p / 720p',
      constraints: { maxImages: 7 },
    })
    for (const group of videoGroups.filter(
      (group) => group.id === 'video-fixed'
    )) {
      for (const example of group.examples) {
        expect(example.body).not.toHaveProperty('duration')
        expect(example.body).not.toHaveProperty('seconds')
      }
    }
  })
})

// Execute the actual published JavaScript; only HTTP, filesystem and time are replaced.
async function executeLifecycle(responses: unknown[], tick = 0) {
  const fetch = vi.fn(async () => {
    const next = responses.shift()
    if (next instanceof Error) {
      throw next
    }
    if (!next) {
      throw new Error('Unexpected extra request')
    }
    return {
      ok: true,
      json: async () => next,
      arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer,
    }
  })
  const writeFile = vi.fn()
  let now = 0
  const result = runInNewContext(
    `(async () => {${buildVideoLifecycleSamples('https://relay.test').javascript.replaceAll(/^import .*$/gm, '')}})()`,
    {
      fetch,
      writeFile,
      Buffer,
      AbortSignal,
      process: { env: { NEW_API_KEY: 'fixture-key' } },
      console: { log: vi.fn() },
      setTimeout: (callback: () => void) => callback(),
      Date: {
        now: () => {
          now += tick
          return now
        },
      },
    }
  ) as Promise<void>
  return { result, fetch, writeFile }
}

describe('Video lifecycle example', () => {
  it('persists the public ID, polls pending state and downloads only after success', async () => {
    const { result, fetch, writeFile } = await executeLifecycle([
      { id: 'task_public', upstream_task_id: 'private' },
      { status: 'in_progress', progress: 100 },
      { status: 'SUCCESS' },
      { bytes: true },
    ])
    await result
    expect(fetch.mock.calls.map((call) => (call as unknown[])[0])).toEqual([
      'https://relay.test/v1/videos',
      'https://relay.test/v1/videos/task_public',
      'https://relay.test/v1/videos/task_public',
      'https://relay.test/v1/videos/task_public/content',
    ])
    expect(writeFile).toHaveBeenCalledWith(
      'video-task.json',
      JSON.stringify({ id: 'task_public', baseURL: 'https://relay.test' })
    )
    expect(writeFile).toHaveBeenCalledWith('result.mp4', Buffer.from([1, 2, 3]))
  })

  it('stops on FAILURE without downloading or resubmitting', async () => {
    const { result, fetch, writeFile } = await executeLifecycle([
      { task_id: 'task_legacy' },
      { status: 'FAILURE', error: { message: 'Rejected' } },
    ])
    await expect(result).rejects.toThrow('Rejected')
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(writeFile).toHaveBeenCalledTimes(1)
  })

  it('preserves an uncertain submission failure without retrying the billed POST', async () => {
    const { result, fetch, writeFile } = await executeLifecycle([
      new Error('Connection lost'),
    ])
    await expect(result).rejects.toThrow('Connection lost')
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(writeFile).not.toHaveBeenCalled()
  })

  it('reports a bounded polling timeout with the saved ID and never resubmits', async () => {
    const { result, fetch, writeFile } = await executeLifecycle(
      [{ id: 'task_resume' }, { status: 'queued' }],
      700000
    )
    await expect(result).rejects.toThrow('Resume GET for task_resume')
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(writeFile).toHaveBeenCalledTimes(1)
  })
})
