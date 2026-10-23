import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { ScrollArea, ScrollBar } from '@/components/ui/scroll-area'

import { CodeExample } from './code-example'
import { ContractParameters } from './contract-parameters'
import {
  pendingVideoModels,
  pendingVideoNote,
  videoCommonParameters,
  videoGroups,
  videoResponseExamples,
} from './video-data'
import { buildVideoMarkdown } from './video-guide'
import {
  buildVideoLifecycleSamples,
  buildVideoSubmitSamples,
} from './video-samples'

export function VideoDocs({ baseUrl }: { baseUrl: string }) {
  const { t } = useTranslation()
  const markdown = useMemo(
    () => buildVideoMarkdown(baseUrl, (key) => t(key)),
    [baseUrl, t]
  )
  const lifecycle = useMemo(
    () => buildVideoLifecycleSamples(baseUrl),
    [baseUrl]
  )
  return (
    <section className='mt-16 space-y-8' aria-labelledby='video-api'>
      <div className='space-y-4'>
        <h2 id='video-api' className='scroll-mt-32 text-2xl font-semibold'>
          {t('apiDocs.video.title')}
        </h2>
        <a
          className='text-primary inline-block text-sm underline underline-offset-4'
          download='oopii-video-api.md'
          href={`data:text/markdown;charset=utf-8,${encodeURIComponent(markdown)}`}
        >
          {t('apiDocs.video.download')}
        </a>
        <ul className='bg-muted/30 list-disc space-y-2 rounded-xl border py-4 pr-5 pl-9 text-sm leading-6'>
          <li>{t('apiDocs.video.quickConnection')}</li>
          <li>{t('apiDocs.video.quickFlow')}</li>
          <li>{t('apiDocs.video.quickModels')}</li>
          <li className='text-foreground font-medium'>
            {t('apiDocs.video.quickMedia')}
          </li>
        </ul>
        <ContractParameters fields={videoCommonParameters} />
        <nav
          aria-label={t('apiDocs.video.title')}
          className='flex flex-wrap gap-3 text-sm'
        >
          {videoGroups.map((group) => (
            <a
              key={group.id}
              href={`#${group.id}`}
              className='text-primary underline underline-offset-4'
            >
              {t(group.title)}
            </a>
          ))}
          <a
            href='#video-lifecycle'
            className='text-primary underline underline-offset-4'
          >
            {t('apiDocs.video.lifecycle')}
          </a>
        </nav>
      </div>
      {videoGroups.map((group) => (
        <section
          key={group.id}
          className='space-y-5'
          aria-labelledby={group.id}
        >
          <h3 id={group.id} className='scroll-mt-32 text-xl font-semibold'>
            {t(group.title)}
          </h3>
          <ScrollArea className='border-border/70 w-full rounded-xl border'>
            <table className='w-full min-w-[900px] text-left text-sm'>
              <thead className='bg-muted/45 text-muted-foreground'>
                <tr>
                  {['models', 'duration', 'resolution', 'ratios', 'limits'].map(
                    (key) => (
                      <th key={key} className='px-4 py-3 font-medium'>
                        {t(`apiDocs.video.${key}`)}
                      </th>
                    )
                  )}
                </tr>
              </thead>
              <tbody className='divide-border divide-y'>
                {group.variants.map((variant) => (
                  <tr key={variant.models[0]} className='align-top'>
                    <td className='px-4 py-3'>
                      {variant.models.map((model) => (
                        <code
                          key={model}
                          className='text-primary block leading-7 whitespace-nowrap'
                        >
                          {model}
                        </code>
                      ))}
                    </td>
                    <td className='px-4 py-3'>{variant.seconds}</td>
                    <td className='px-4 py-3'>{variant.resolution}</td>
                    <td className='px-4 py-3'>{variant.ratios}</td>
                    <td className='text-muted-foreground px-4 py-3 leading-6'>
                      <div>
                        {t(
                          'Reference limits: {{images}} images, {{videos}} videos, {{audios}} audio clips; {{total}} total.',
                          {
                            images: variant.constraints.maxImages,
                            videos: variant.constraints.maxVideos,
                            audios: variant.constraints.maxAudios,
                            total: variant.constraints.maxTotal,
                          }
                        )}
                      </div>
                      <details className='mt-2'>
                        <summary className='text-primary cursor-pointer'>
                          {t('All constraints (JSON)')}
                        </summary>
                        <p className='mt-2'>{t(variant.limits)}</p>
                        <pre className='mt-2 max-w-80 overflow-x-auto text-xs'>
                          <code>
                            {JSON.stringify(variant.constraints, null, 2)}
                          </code>
                        </pre>
                      </details>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <ScrollBar orientation='horizontal' />
          </ScrollArea>
          <h4 className='text-sm font-semibold'>
            {t('apiDocs.video.parameters')}
          </h4>
          <ContractParameters fields={group.parameters} />
          <ul className='text-muted-foreground list-disc space-y-2 pl-5 text-sm leading-7'>
            {group.notes.map((note) => (
              <li key={note}>{t(note)}</li>
            ))}
          </ul>
          {group.examples.map((example) => (
            <CodeExample
              key={example.title}
              title={t(example.title)}
              samples={buildVideoSubmitSamples(baseUrl, example.body)}
            />
          ))}
        </section>
      ))}
      <section className='space-y-4' aria-labelledby='video-responses'>
        <h3 id='video-responses' className='scroll-mt-32 text-xl font-semibold'>
          {t('apiDocs.video.responses')}
        </h3>
        {Object.entries(videoResponseExamples).map(([name, body]) => (
          <div key={name}>
            <h4 className='mb-2 text-sm font-medium'>
              {t(`apiDocs.video.${name}`)}
            </h4>
            <pre className='bg-muted/30 overflow-x-auto rounded-xl border p-4 text-xs leading-6'>
              <code>{JSON.stringify(body, null, 2)}</code>
            </pre>
          </div>
        ))}
      </section>
      <section className='space-y-4' aria-labelledby='video-lifecycle'>
        <h3 id='video-lifecycle' className='scroll-mt-32 text-xl font-semibold'>
          {t('apiDocs.video.lifecycle')}
        </h3>
        <ul className='text-muted-foreground list-disc space-y-2 pl-5 text-sm leading-6'>
          <li>{t('apiDocs.video.flowStatus')}</li>
          <li>{t('apiDocs.video.flowRetry')}</li>
          <li>{t('apiDocs.video.flowDownload')}</li>
          <li>{t('apiDocs.video.sampleRuntime')}</li>
        </ul>
        <CodeExample title={t('apiDocs.video.lifecycle')} samples={lifecycle} />
      </section>
      <aside className='bg-muted/30 space-y-2 rounded-xl border p-5 text-sm leading-7'>
        <h3 className='font-semibold'>{t('Model mappings to confirm')}</h3>
        <p className='text-muted-foreground'>{t(pendingVideoNote)}</p>
        {pendingVideoModels.map((model) => (
          <code key={model} className='block'>
            {model}
          </code>
        ))}
      </aside>
      <aside className='bg-muted/30 space-y-2 rounded-xl border p-5 text-sm leading-7'>
        <h3 className='font-semibold'>{t('apiDocs.video.legacy')}</h3>
        <p className='text-muted-foreground'>{t('apiDocs.video.legacyNote')}</p>
      </aside>
    </section>
  )
}
