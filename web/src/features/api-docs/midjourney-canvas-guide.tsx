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
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { CodeExample } from './code-example'
import { getMidjourneyWorkflowSamples } from './midjourney-samples'

const endpoints = [
  [
    'POST',
    '/mj/submit/imagine',
    'prompt, botType, base64Array, speedMode',
    'mj_fast_imagine / mj_relax_imagine',
  ],
  ['GET', '/mj/task/{id}/fetch', 'id', 'Task status and result'],
  [
    'POST',
    '/mj/submit/action',
    'taskId, customId, speedMode',
    'Follow-up button action',
  ],
  [
    'POST',
    '/mj/submit/modal',
    'taskId, prompt?, maskBase64?, speedMode',
    'mj_fast_modal / mj_relax_modal',
  ],
  ['GET', '/mj/image/{id}', 'id', 'Result image forwarding'],
] as const

const parameters = [
  ['prompt', 'string', 'Prompt text with image URLs and MJ parameters'],
  [
    'botType',
    'string',
    'The canvas sends MID_JOURNEY; Niji is selected with --niji in the prompt.',
  ],
  [
    'speedMode',
    'fast | relax',
    'Set speedMode as a top-level JSON field: fast selects mj_fast_imagine; relax selects mj_relax_imagine. Omitted or empty selects mj_imagine. Keep the same speed for follow-up actions and modal submissions.',
  ],
  [
    'base64Array',
    'string[]',
    'Use [] for text-only or public URL reference requests.',
  ],
  [
    '--ar / --v / --niji',
    'prompt',
    'Aspect ratio and version belong in the prompt, for example --ar 4:5 --v 7. Choose either --v or --niji.',
  ],
  [
    '--sref / --p / --raw / --hd',
    'prompt',
    'Style reference URL, personalization code, Raw and HD are prompt parameters. Availability depends on the selected MJ version and channel.',
  ],
] as const

function Section(props: {
  id: string
  title: string
  children: React.ReactNode
}) {
  const { t } = useTranslation()
  return (
    <section id={props.id} className='mt-16 scroll-mt-24'>
      <h2 className='text-2xl font-semibold tracking-tight sm:text-3xl'>
        {t(props.title)}
      </h2>
      <div className='mt-5 space-y-4 text-sm leading-7'>{props.children}</div>
    </section>
  )
}

function JsonExample(props: { value: unknown }) {
  return (
    <pre className='overflow-x-auto bg-[#0b1020] p-4 text-xs leading-6 text-slate-200 dark:bg-black/35'>
      <code>{JSON.stringify(props.value, null, 2)}</code>
    </pre>
  )
}

export function MidjourneyCanvasGuide(props: { baseUrl: string }) {
  const { t } = useTranslation()
  const samples = React.useMemo(
    () => getMidjourneyWorkflowSamples(props.baseUrl),
    [props.baseUrl]
  )
  return (
    <div
      className='border-border/70 mt-16 border-t pt-12'
      data-mj-guide-version='2026-10-12-r5'
    >
      <Section id='midjourney-overview' title='Midjourney: the canvas workflow'>
        <p>
          {t(
            'Use the dedicated Midjourney task API. An OpenAI Images client cannot connect by changing only the model name to midjourney.'
          )}
        </p>
        <ol className='list-decimal space-y-2 pl-5'>
          <li>
            {t(
              'In canvas settings, select OOpii as the Midjourney provider, enter your OOpii API key and enable that provider.'
            )}
          </li>
          <li>
            {t(
              'Create a Midjourney input node, enter a prompt, choose a version, aspect ratio and fast or relax mode, then generate. Results appear in the connected result workflow.'
            )}
          </li>
          <li>
            {t(
              'Connect image nodes as normal or style references. For direct API requests, supply public image URLs in the prompt.'
            )}
          </li>
        </ol>
        <p>
          {t(
            'Use the site root without /v1 as the Midjourney base URL. Send Authorization: Bearer YOUR_API_KEY on submit and task-query requests.'
          )}
        </p>
        <code>{props.baseUrl}</code>
        <p>
          {t(
            'Use a key with sufficient balance and a group/channel supporting the selected MJ speed and operation. Routing names such as mj_fast_imagine describe gateway capabilities; do not send them as a model field to /mj/submit/imagine.'
          )}
        </p>
      </Section>

      <Section
        id='midjourney-quickstart'
        title='Start with a text-only request'
      >
        <div role='note' className='rounded-lg border p-4'>
          <strong>{t('Set speedMode explicitly: "fast"')}</strong>
          <p>
            {t(
              'Omitting speedMode does not default to fast: it selects the legacy mj_imagine route. If your group only exposes mj_fast_imagine, omitting this field causes 503 No available channel. Use fast for this example; use relax only when your group has an enabled mj_relax_imagine channel.'
            )}
          </p>
        </div>
        <p>
          {t(
            'First verify text-only generation with no reference images. POST the following JSON to /mj/submit/imagine with Authorization: Bearer YOUR_API_KEY and Content-Type: application/json.'
          )}
        </p>
        <JsonExample
          value={{
            prompt: 'A cinematic portrait --ar 4:5 --v 7',
            botType: 'MID_JOURNEY',
            speedMode: 'fast',
            base64Array: [],
          }}
        />
        <ol className='list-decimal space-y-2 pl-5'>
          <li>
            {t('Save result from the accepted submission as the task ID.')}
          </li>
          <li>
            {t(
              'Query GET /mj/task/{id}/fetch with the same key about every 5 seconds. Use status to decide whether to continue.'
            )}
          </li>
          <li>
            {t(
              'On SUCCESS, read imageUrl and buttons. On FAILURE, stop and read failReason. The runnable examples below perform these checks for you.'
            )}
          </li>
        </ol>
      </Section>

      <Section
        id='midjourney-endpoints'
        title='Midjourney endpoints used by the canvas'
      >
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full text-left text-sm'>
            <thead>
              <tr>
                {['Method', 'Endpoint', 'Request fields', 'Purpose'].map(
                  (label) => (
                    <th key={label} className='px-4 py-3'>
                      {t(label)}
                    </th>
                  )
                )}
              </tr>
            </thead>
            <tbody>
              {endpoints.map(([method, path, fields, purpose]) => (
                <tr key={path} className='border-t align-top'>
                  <td className='px-4 py-3'>{method}</td>
                  <td className='px-4 py-3'>
                    <code>{path}</code>
                  </td>
                  <td className='px-4 py-3'>
                    <code>{fields}</code>
                  </td>
                  <td className='px-4 py-3'>{t(purpose)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p>
          {t(
            'Generation and follow-up actions use the configured billing rules. Polling does not submit a new generation.'
          )}
        </p>
        <p>{t('Use the result image URL returned by the API.')}</p>
        <p>
          {t(
            'Other compatibility routes include change, simple-change, blend, describe, shorten, edits, video and task/list-by-condition. Route availability alone does not guarantee upstream support; the workflow below covers the operations used by the canvas.'
          )}
        </p>
      </Section>

      <Section
        id='midjourney-parameters'
        title='Reference images and prompt parameters'
      >
        <p>
          {t(
            'Use a publicly accessible HTTPS URL for the reference image. The URL must remain accessible until the task completes.'
          )}
        </p>
        <h3 className='text-lg font-semibold'>
          {t('Submit the reference and style prompt')}
        </h3>
        <code>POST /mj/submit/imagine</code>
        <JsonExample
          value={{
            prompt:
              'https://media.example.com/reference.jpg A cinematic portrait --ar 4:5 --v 7 --sref https://media.example.com/style.jpg',
            botType: 'MID_JOURNEY',
            speedMode: 'fast',
            base64Array: [],
          }}
        />
        <p>
          {t(
            'Replace the example URLs with your public image URLs. Normal references go before the prompt; style references follow --sref. Omit both URLs and --sref for text-only generation.'
          )}
        </p>
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full text-left text-sm'>
            <tbody>
              {parameters.map(([field, type, detail]) => (
                <tr key={field} className='border-t align-top'>
                  <td className='px-4 py-3'>
                    <code>{field}</code>
                  </td>
                  <td className='px-4 py-3'>{type}</td>
                  <td className='px-4 py-3'>{t(detail)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p>
          {t(
            'The canvas limits the final prompt to 6000 characters including URLs and flags. Do not repeat reserved parameters in advanced parameters. This is a canvas validation limit, not a guarantee of every upstream version.'
          )}
        </p>
      </Section>

      <Section id='midjourney-workflow' title='Submit and poll workflow'>
        <p>
          {t(
            'Set NEW_API_KEY. Optionally set REFERENCE_IMAGE_URL and STYLE_IMAGE_URL to public HTTPS image URLs. Save the returned task ID; polling stops after 20 minutes without cancelling the task.'
          )}
        </p>
        <p>
          {t(
            'Run these scripts with Node.js 20+, Python 3.9+ with requests, or Bash with curl and jq. Set environment variables in your shell before running.'
          )}
        </p>
        <CodeExample samples={samples} title={t('Complete task workflow')} />
      </Section>

      <Section id='midjourney-actions' title='Button actions and modal editing'>
        <p>
          {t(
            'After SUCCESS, read buttons from the task response. Send the chosen customId exactly as returned, the source taskId and the same speedMode to /mj/submit/action. Do not assemble customId from an index or assume it equals the task ID.'
          )}
        </p>
        <JsonExample
          value={{
            taskId: 'SOURCE_TASK_ID',
            customId: 'CUSTOM_ID_FROM_BUTTONS',
            speedMode: 'fast',
          }}
        />
        <p>
          {t(
            'A normal accepted action returns a result task ID: save and poll that ID. For an Inpaint or CustomZoom button, code 21 can instead mean that a modal submission is required. The canvas uses the action response result as the modal taskId, falling back to the source task ID only if it is absent. Do not treat every code 21 as an ordinary completed or queued action.'
          )}
        </p>
        <code>POST /mj/submit/modal</code>
        <JsonExample
          value={{
            taskId: 'ACTION_RESULT_OR_SOURCE_TASK_ID',
            prompt: 'A cinematic portrait --zoom 1.5',
            speedMode: 'fast',
          }}
        />
        <p>
          {t(
            'For custom zoom, include the edited prompt and --zoom value. For prompt-based editing, send the edited prompt without that zoom flag. maskBase64 is an optional provider-specific mask field; send a complete mask data URL only when that editing workflow requires it. The current canvas prompt modal does not automatically supply a mask.'
          )}
        </p>
        <p>
          {t(
            'Read the task ID returned by modal submission and poll it to completion. Button availability, mask conventions and edit support depend on the original task and upstream channel. Unsupported modal actions should show an error instead of starting an endless poll.'
          )}
        </p>
      </Section>

      <Section
        id='midjourney-responses'
        title='Responses, downloads and error handling'
      >
        <h3
          id='midjourney-channel-503'
          className='scroll-mt-24 text-lg font-semibold'
        >
          {t('503: No available channel for model mj_imagine')}
        </h3>
        <p>
          {t(
            'This message means the gateway could not select a channel for mj_imagine in the token group. Keep POST /mj/submit/imagine and Bearer authentication. If /v1/models lists mj_fast_imagine, send "speedMode": "fast" in the request JSON. You do not need a different endpoint or a model field.'
          )}
        </p>
        <p>
          {t(
            'The field name is camelCase speedMode, not speed_mode. A prompt flag such as --fast and a model field such as "model": "mj_fast_imagine" do not select this gateway route. If you already set speedMode, inspect the actual HTTP JSON: some SDKs omit unknown fields or nest them incorrectly.'
          )}
        </p>
        <p>
          {t(
            'If the error instead names mj_fast_imagine after setting fast, check enabled channels, token permissions and group access for that exact route. Adding the legacy mj_imagine name is only an operator compatibility choice after defining its speed and billing behavior; it is not required for the documented fast request. Relax and later actions also need their own available routes.'
          )}
        </p>
        <JsonExample
          value={{
            code: 1,
            description: 'Submit success',
            result: 'TASK_ID',
            properties: {},
          }}
        />
        <p>
          {t(
            'Imagine submission accepts code 1 (submitted), 21 (existing task) or 22 (queued), together with a non-empty result task ID. Codes 23 (queue full), 24 (prompt rejected) and other error codes are not accepted generation tasks, even if a result field is present. Action code 21 must be interpreted in the modal context above.'
          )}
        </p>
        <JsonExample
          value={{
            id: 'TASK_ID',
            status: 'SUCCESS',
            progress: '100%',
            imageUrl: props.baseUrl + '/mj/image/TASK_ID',
            buttons: [{ customId: 'CUSTOM_ID_FROM_BUTTONS', label: 'U1' }],
          }}
        />
        <p>
          {t(
            'Task queries return a task object, not the submit envelope. Wait for SUCCESS; progress alone is not sufficient. On FAILURE or CANCELLED, stop and display failReason. A response such as {"code":4,"description":"task_no_found"} is an error even with HTTP 200. Missing or unknown status must stop the example with a diagnostic.'
          )}
        </p>
        <p>
          {t(
            'Download imageUrl after SUCCESS. Resolve a relative image URL against the base URL. Result URLs may point to the public gateway image proxy or an upstream image host. Do not attach your OOpii key to arbitrary third-party image hosts. The canvas can split a returned grid into four images locally; the API does not promise four independent image URLs.'
          )}
        </p>
        <p>
          {t(
            'On network or request timeout, keep the saved task ID and resume querying it. The examples stop on HTTP errors rather than silently resubmitting. If adding retries, bound retries for queries and respect 429 Retry-After; never blindly repeat a generation or action POST because the first request may already have created a charged task.'
          )}
        </p>
        <p>
          {t(
            'notifyHook depends on operator callback settings. Clients should support polling even when they request callbacks.'
          )}
        </p>
      </Section>
    </div>
  )
}
