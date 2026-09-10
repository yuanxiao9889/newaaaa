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
import { runInNewContext } from 'node:vm'

import { describe, expect, it } from 'vitest'

import { getMidjourneyWorkflowSamples } from '../midjourney-samples'

async function executeExample(
  responses: unknown[],
  env: Record<string, string> = {}
) {
  const requests: { url: string; body: unknown }[] = []
  const output: unknown[][] = []
  const script = getMidjourneyWorkflowSamples(
    'https://relay.test'
  ).javascript.replaceAll(/^import .*$/gm, '')
  const context = {
    readFile: async () => Buffer.from('fixture-image'),
    extname: () => '.png',
    AbortSignal: { timeout: () => undefined },
    process: {
      env: { NEW_API_KEY: 'fixture-key', ...env },
      exit: () => {
        throw new Error('EXIT_SUCCESS')
      },
    },
    console: { log: (...values: unknown[]) => output.push(values) },
    fetch: async (url: string, options: { body?: string }) => {
      requests.push({
        url,
        body: options.body ? JSON.parse(options.body) : undefined,
      })
      if (!responses.length) throw new Error('UNEXPECTED_NETWORK_REQUEST')
      return { ok: true, json: async () => responses.shift() }
    },
    setTimeout: (resolve: () => void) => resolve(),
  }
  let error = ''
  try {
    await runInNewContext('(async () => {' + script + '})()', context)
  } catch (caught) {
    error = String((caught as Error).message)
  }
  return { requests, output, error }
}

describe('published Midjourney JavaScript example', () => {
  it('prints a successful result after saving the task ID', async () => {
    const result = await executeExample([
      { code: 1, result: 'task' },
      { status: 'SUCCESS', imageUrl: 'https://images.test/result.png' },
    ])
    expect(result.error).toBe('EXIT_SUCCESS')
    expect(result.output).toEqual([
      ['Task ID:', 'task'],
      ['https://images.test/result.png', []],
    ])
  })

  it('stops on an HTTP 200 task business error without another query', async () => {
    const result = await executeExample([
      { code: 1, result: 'task' },
      { code: 4, description: 'task_no_found' },
    ])
    expect(result.error).toContain('task_no_found')
    expect(result.requests).toHaveLength(2)
  })

  it('rejects a queue-full submission even when it contains a task ID', async () => {
    const result = await executeExample([
      { code: 23, result: 'rejected-task', description: 'Queue full' },
    ])
    expect(result.error).toContain('Submit failed')
    expect(result.requests).toHaveLength(1)
  })

  it('stops on a failed task and exposes its failure reason', async () => {
    const result = await executeExample([
      { code: 22, result: 'task' },
      { status: 'FAILURE', failReason: 'upstream rejected prompt' },
    ])
    expect(result.error).toBe('upstream rejected prompt')
  })

  it('uploads ordinary and style references and places their URLs in the correct prompt positions', async () => {
    const result = await executeExample(
      [
        { code: 1, result: ['https://relay.test/mj/upload/ref.png'] },
        { code: 1, result: ['https://relay.test/mj/upload/style.png'] },
        { code: 1, result: 'task' },
        { status: 'SUCCESS', imageUrl: 'https://images.test/result.png' },
      ],
      { REFERENCE_IMAGE: 'ref.png', STYLE_IMAGE: 'style.png' }
    )
    expect(result.requests[0].url).toBe(
      'https://relay.test/mj/submit/upload-discord-images'
    )
    expect(result.requests[2].body).toEqual({
      prompt:
        'https://relay.test/mj/upload/ref.png A cinematic portrait --ar 4:5 --v 7 --sref https://relay.test/mj/upload/style.png',
      botType: 'MID_JOURNEY',
      speedMode: 'fast',
      base64Array: [],
    })
    expect(result.error).toBe('EXIT_SUCCESS')
  })

  it('rejects a malformed upload response before submitting generation', async () => {
    const result = await executeExample([{ code: 1, result: [] }], {
      REFERENCE_IMAGE: 'ref.png',
    })
    expect(result.error).toContain('Upload failed')
    expect(result.requests).toHaveLength(1)
  })
})
