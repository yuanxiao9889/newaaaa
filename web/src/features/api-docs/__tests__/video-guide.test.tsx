import { fireEvent, render, screen, act } from '@testing-library/react'
import i18n from 'i18next'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import en from '../../../i18n/locales/en.json'
import zh from '../../../i18n/locales/zh.json'
import { DocsNavigation } from '../docs-navigation'
import { GenerationDocs, IntegrationExport } from '../generation-docs'
import { videoGroups } from '../video-data'
import { VideoDocs } from '../video-docs'

beforeAll(() => {
  i18n.addResourceBundle('en', 'translation', en.translation, true, true)
  i18n.addResourceBundle('zh', 'translation', zh.translation, true, true)
})
afterEach(async () => {
  await i18n.changeLanguage('en')
})

describe('Video integration guide', () => {
  it('downloads a complete Chinese guide and updates it when the user switches to English', async () => {
    await i18n.changeLanguage('zh')
    render(<VideoDocs baseUrl='https://relay.test' />)
    const link = screen.getByRole('link', {
      name: zh.translation['apiDocs.video.download'],
    })
    expect(link.getAttribute('download')).toBe('oopii-video-api.md')
    const markdown = decodeURIComponent(
      (link.getAttribute('href') ?? '').split(',').slice(1).join(',')
    )
    for (const model of videoGroups.flatMap((group) =>
      group.variants.flatMap((variant) => variant.models)
    )) {
      expect(markdown).toContain(model)
    }
    expect(markdown).toContain('/v1/videos/{task_id}/content')
    expect(markdown).toContain('omni_reference_task_type')
    expect(markdown).toContain('first_frame')
    expect(markdown).toContain('metadata.video_urls')
    expect(markdown).toContain(zh.translation['apiDocs.video.scope'])
    expect(markdown).toContain(zh.translation['apiDocs.video.mediaRules'])
    expect(markdown).toContain('https://your-storage.example.com/')
    expect(
      screen.getByText(zh.translation['apiDocs.video.quickMedia'])
    ).toBeTruthy()
    expect(screen.queryByText(zh.translation['apiDocs.video.scope'])).toBeNull()
    expect(
      screen.getByText(zh.translation['apiDocs.video.quickFlow'])
    ).toBeTruthy()
    expect(markdown).not.toContain('apiDocs.video.')
    expect(document.body.textContent).not.toContain('apiDocs.video.')
    await act(async () => {
      await i18n.changeLanguage('en')
    })
    expect(
      screen.getByRole('heading', {
        name: en.translation['apiDocs.video.title'],
      })
    ).toBeTruthy()
    expect(
      screen
        .getByRole('link', { name: en.translation['apiDocs.video.download'] })
        .getAttribute('href')
    ).toContain('Third-party%20video')
    expect(document.body.textContent).not.toContain('apiDocs.video.')
  })

  it('lets the reader switch request recipes to Python without losing the group parameters', () => {
    render(<VideoDocs baseUrl='https://relay.test' />)
    fireEvent.click(screen.getAllByRole('tab', { name: 'Python' })[0])
    expect(screen.getByText(/payload = json.loads/)).toBeTruthy()
    expect(
      screen.getByRole('heading', {
        name: en.translation['Omni video: URL arrays'],
      })
    ).toBeTruthy()
    expect(
      screen
        .getAllByRole('link', {
          name: en.translation['Omni video: URL arrays'],
        })[0]
        .getAttribute('href')
    ).toBe('#video-arrays')
  })
})

describe('Multimodal documentation', () => {
  it('finds models by their exact API alias and keeps independent image and task routes separate', () => {
    const onNavigate = vi.fn()
    render(<DocsNavigation onNavigate={onNavigate} />)
    const search = screen.getByRole('textbox', {
      name: 'Search models and endpoints',
    })
    fireEvent.change(search, { target: { value: 'wan3.0-video' } })
    expect(
      screen.getByRole('link', { name: 'Wan 3.0' }).getAttribute('href')
    ).toBe('#video-wan30')
    fireEvent.change(search, { target: { value: 'Midjourney v8.2' } })
    const image = screen.getByRole('link', {
      name: 'Midjourney independent image API',
    })
    expect(image.getAttribute('href')).toBe('#midjourney-image')
    fireEvent.click(image)
    expect(onNavigate).toHaveBeenCalledOnce()
    fireEvent.change(search, { target: { value: '/jieapi/v1/audio/speech' } })
    expect(
      screen
        .getByRole('link', { name: 'Audio generation' })
        .getAttribute('href')
    ).toBe('#seed-audio')
    fireEvent.change(search, { target: { value: 'unavailable-model-example' } })
    expect(screen.getByRole('status').textContent).toBe(
      'No matching documentation'
    )
    fireEvent.change(search, { target: { value: '' } })
    expect(
      screen.getByRole('link', { name: 'Midjourney independent image API' })
    ).toBeTruthy()
    expect(
      document.querySelector('a[href="#midjourney-overview"]')
    ).toBeTruthy()
  })

  it('exports and copies the same translated API contracts and correct audio and image paths', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    await i18n.changeLanguage('zh')
    render(
      <>
        <IntegrationExport baseUrl='https://relay.test' />
        <GenerationDocs baseUrl='https://relay.test' />
      </>
    )
    const link = screen.getByRole('link', {
      name: zh.translation['Download Markdown'],
    })
    const markdown = decodeURIComponent(
      (link.getAttribute('href') ?? '').split(',').slice(1).join(',')
    )
    expect(markdown).toContain('https://relay.test/jieapi/v1/audio/speech')
    expect(markdown).toContain('Midjourney v8.2')
    expect(markdown).toContain('/v1/images/edits?async=true')
    expect(markdown).toContain('/v1/images/tasks/{task_id}/content?index=0')
    expect(markdown).toContain('original_duration')
    expect(markdown).toContain('maxVideoAndOutputSeconds')
    expect(markdown).toContain('"quality": "1"')
    expect(markdown).not.toContain('oopii/Midjourney')
    expect(markdown).not.toContain('apiDocs.video.')
    expect(
      screen.getByRole('heading', { name: zh.translation['Audio generation'] })
    ).toBeTruthy()
    await act(async () =>
      fireEvent.click(
        screen.getByRole('button', { name: zh.translation['Copy for AI'] })
      )
    )
    expect(writeText).toHaveBeenCalledWith(markdown)
    await act(async () => {
      await i18n.changeLanguage('en')
    })
    expect(
      screen.getByRole('heading', { name: 'Audio generation' })
    ).toBeTruthy()
    const english = decodeURIComponent(
      (
        screen
          .getByRole('link', { name: 'Download Markdown' })
          .getAttribute('href') ?? ''
      )
        .split(',')
        .slice(1)
        .join(',')
    )
    expect(english).toContain('# Audio, image and video integration')
    expect(english).not.toBe(markdown)
  })
})
