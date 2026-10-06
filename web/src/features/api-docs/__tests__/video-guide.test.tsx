import { fireEvent, render, screen, act } from '@testing-library/react'
import i18n from 'i18next'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'

import en from '../../../i18n/locales/en.json'
import zh from '../../../i18n/locales/zh.json'
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
        name: en.translation['apiDocs.video.arraysTitle'],
      })
    ).toBeTruthy()
    expect(
      screen
        .getAllByRole('link', {
          name: en.translation['apiDocs.video.arraysTitle'],
        })[0]
        .getAttribute('href')
    ).toBe('#video-arrays')
  })
})
