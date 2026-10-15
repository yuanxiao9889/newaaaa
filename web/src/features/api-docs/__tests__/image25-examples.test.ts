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

import en from '../../../i18n/locales/en.json'
import zh from '../../../i18n/locales/zh.json'
import { imageModels } from '../data'
import { buildHyImageSamples } from '../hy-image-samples'
import { buildOpenAISamples } from '../openai-samples'

const groups = imageModels.filter((model) => model.modelIds)

describe('Hunyuan documentation examples', () => {
  it.each(['generate', 'edit'] as const)(
    '%s sends public URLs without unsupported GPT image parameters',
    async (operation) => {
      const samples = buildHyImageSamples('https://relay.test/', operation)
      const curlBody =
        samples.curl.split("<<'JSON'\n")[1]?.split('\nJSON')[0] ?? ''
      const payload = JSON.parse(curlBody)
      let sent: Record<string, unknown> = {}
      let endpoint = ''
      await runInNewContext(`(async () => {${samples.javascript}})()`, {
        fetch: async (url: string, init: RequestInit) => {
          endpoint = url
          sent = JSON.parse(String(init.body))
          expect(init.headers).toEqual({
            Authorization: 'Bearer fixture-key',
            'Content-Type': 'application/json',
          })
          return {
            ok: true,
            json: async () => ({
              data: [{ url: 'https://media.example.com/result.png' }],
            }),
          }
        },
        process: { env: { NEW_API_KEY: 'fixture-key' } },
        AbortSignal,
        console: { log: () => undefined },
      })
      expect(endpoint).toBe(
        `https://relay.test/v1/images/${operation === 'edit' ? 'edits' : 'generations'}`
      )
      expect(sent).toEqual(payload)
      expect(sent.model).toBe('hy-image-v3.5-preview')
      expect(sent.size).toBe('1536x1536')
      expect(sent.n).toBe(1)
      expect(sent.image).toEqual(
        operation === 'edit'
          ? [
              'https://media.example.com/character.jpg',
              'https://media.example.com/scene.png',
            ]
          : undefined
      )
      for (const field of [
        'images',
        'quality',
        'mask',
        'background',
        'output_format',
      ]) {
        expect(sent).not.toHaveProperty(field)
      }
      expect(samples.curl).toContain('-H "Authorization: Bearer $NEW_API_KEY"')
      const literal =
        samples.python.match(/payload = json.loads\((.+)\)/)?.[1] ?? ''
      expect(JSON.parse(JSON.parse(literal))).toEqual(payload)
    }
  )
})

describe('Image 2.5 documentation examples', () => {
  it.each(groups)(
    '$id sends valid sizes and quality in executable generation and edit examples',
    async (model) => {
      for (const operation of ['generate', 'edit'] as const) {
        for (const transparent of [false, true]) {
          const samples = buildOpenAISamples(
            model,
            'https://relay.test',
            operation,
            transparent
          )
          const script = samples.javascript.replaceAll(/^import .*$/gm, '')
          let requestUrl = ''
          let requestBody: Record<string, unknown> = {}
          const context = {
            Blob,
            FormData,
            readFile: async () => new Uint8Array([1, 2, 3]),
            process: { env: { NEW_API_KEY: 'fixture-key' } },
            console: { log: () => undefined },
            fetch: async (
              url: string,
              options: {
                body: string | FormData
                headers: Record<string, string>
              }
            ) => {
              requestUrl = url
              expect(options.headers.Authorization).toBe('Bearer fixture-key')
              if (typeof options.body === 'string') {
                requestBody = JSON.parse(options.body)
              } else {
                requestBody = Object.fromEntries(options.body.entries())
                expect(options.body.getAll('image[]')).toHaveLength(2)
                expect(options.headers['Content-Type']).toBeUndefined()
              }
              return {
                ok: true,
                json: async () => ({ data: [{ b64_json: 'fixture' }] }),
              }
            },
          }
          await runInNewContext(`(async () => { ${script} })()`, context)
          expect(requestUrl).toBe(
            `https://relay.test/v1/images/${operation === 'edit' ? 'edits' : 'generations'}`
          )
          expect(requestBody.model).toBe(model.model)
          expect(String(requestBody.n)).toBe('1')
          expect(requestBody.size).toBe(
            model.id.endsWith('-1k') ? '1280x720' : '2048x1152'
          )
          const allowed = model.parameters
            .find((parameter) => parameter.name === 'quality')
            ?.values.split(' | ')
          expect(allowed).toContain(requestBody.quality)
          expect(requestBody.quality).toBe(
            transparent && allowed?.includes('max') ? 'max' : 'medium'
          )
          expect(requestBody.background).toBe(
            transparent ? 'transparent' : undefined
          )
          expect(requestBody.output_format).toBe(
            transparent ? 'png' : undefined
          )
          expect(requestBody).not.toHaveProperty('transparent_background')
          expect(requestBody).not.toHaveProperty('aspect_ratio')
          expect(samples.curl).toContain(
            '-H "Authorization: Bearer $NEW_API_KEY"'
          )
          if (operation === 'generate') {
            const json = samples.curl.match(/-d '([\s\S]+)'$/)?.[1] ?? ''
            expect(JSON.parse(json)).toEqual(requestBody)
          } else if (transparent) {
            expect(samples.curl).toContain(
              "\\\n  -F 'background=transparent' \\\n  -F 'output_format=png'"
            )
          }
        }
      }
    }
  )

  it('keeps extended quality out of standard routes and low out of token-billed GPT routes', () => {
    const quality = (id: string) =>
      groups
        .find((model) => model.id === id)
        ?.parameters.find((parameter) => parameter.name === 'quality')?.values
    expect(quality('all-image-2.5')).toBe('low | medium | high')
    expect(quality('all-image-2.5-1k')).toBe('low | medium | high')
    expect(quality('all-image-2.5-h')).toBe('low | medium | high | xhigh | max')
    expect(quality('gpt-image-2.5')).toBe('medium | high | xhigh | max')
    expect(quality('gpt-image-2.5-opi')).toBe(
      'low | medium | high | xhigh | max'
    )
  })

  it('provides Chinese and English text for every new parameter and group explanation', () => {
    const keys = groups.flatMap((model) => [
      model.summary,
      model.recommendedFor,
      ...model.requestNotes,
      ...model.parameters.map((parameter) => parameter.description),
    ])
    for (const key of keys.filter((key) =>
      key.startsWith('apiDocs.image25.')
    )) {
      expect(en.translation).toHaveProperty([key])
      expect(zh.translation).toHaveProperty([key])
    }
    const ids = groups.flatMap((model) => model.modelIds ?? [])
    expect(ids).toHaveLength(12)
    expect(new Set(ids).size).toBe(12)
  })
})
