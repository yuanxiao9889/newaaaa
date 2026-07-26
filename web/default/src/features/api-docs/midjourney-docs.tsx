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
import { AlertCircle, CheckCircle2, Route, Workflow } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { ScrollArea, ScrollBar } from '@/components/ui/scroll-area'

import { CodeExample, type CodeSamples } from './code-example'
import {
  midjourneyCommonParameters,
  midjourneyEndpoints,
  midjourneyTaskFields,
  type MidjourneyParameterDoc,
} from './midjourney-data'

function MidjourneyHeading({
  id,
  title,
  description,
}: {
  id: string
  title: string
  description: string
}) {
  const { t } = useTranslation()
  return (
    <div id={id} className='scroll-mt-24 pt-4'>
      <div className='text-primary mb-2 text-xs font-semibold tracking-[0.14em] uppercase'>
        MIDJOURNEY API
      </div>
      <h2 className='text-2xl font-semibold tracking-tight sm:text-3xl'>
        {t(title)}
      </h2>
      <p className='text-muted-foreground mt-2 max-w-3xl text-sm leading-7'>
        {t(description)}
      </p>
    </div>
  )
}

function FieldTable({ rows }: { rows: MidjourneyParameterDoc[] }) {
  const { t } = useTranslation()
  return (
    <div className='border-border/70 mt-5 overflow-hidden rounded-lg border'>
      <ScrollArea className='w-full'>
        <table className='w-full min-w-[860px] text-left text-sm'>
          <thead className='bg-muted/45 text-muted-foreground'>
            <tr>
              {[
                'Parameter',
                'Type',
                'Required',
                'Allowed values',
                'Description',
              ].map((label) => (
                <th key={label} className='px-4 py-3 font-medium'>
                  {t(label)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className='divide-border divide-y'>
            {rows.map((row) => (
              <tr key={row.name} className='align-top'>
                <td className='px-4 py-3'>
                  <code className='text-primary'>{row.name}</code>
                </td>
                <td className='px-4 py-3'>
                  <code>{row.type}</code>
                </td>
                <td className='px-4 py-3'>{t(row.required)}</td>
                <td className='px-4 py-3'>
                  <code>{row.values}</code>
                </td>
                <td className='text-muted-foreground max-w-sm px-4 py-3 leading-6'>
                  {t(row.description)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <ScrollBar orientation='horizontal' />
      </ScrollArea>
    </div>
  )
}

const payloadExamples = [
  [
    'Imagine',
    '/mj/submit/imagine',
    `{
  "prompt": "A cinematic paper-cut city at sunrise --ar 16:9",
  "botType": "MID_JOURNEY",
  "base64Array": [],
  "speedMode": "fast"
}`,
  ],
  [
    'Upload images',
    '/mj/submit/upload-discord-images',
    `{
  "base64Array": [
    "data:image/png;base64,iVBORw0KGgo..."
  ]
}`,
  ],
  [
    'Button action',
    '/mj/submit/action',
    `{
  "taskId": "TASK_ID",
  "customId": "CUSTOM_ID_FROM_BUTTONS",
  "speedMode": "relax"
}`,
  ],
  [
    'Explicit change',
    '/mj/submit/change',
    `{
  "taskId": "TASK_ID",
  "action": "UPSCALE",
  "index": 1
}`,
  ],
  [
    'Compact change',
    '/mj/submit/simple-change',
    `{
  "content": "TASK_ID U1"
}`,
  ],
  [
    'Blend',
    '/mj/submit/blend',
    `{
  "base64Array": ["data:image/jpeg;base64,...", "data:image/jpeg;base64,..."],
  "botType": "MID_JOURNEY",
  "dimensions": "SQUARE"
}`,
  ],
  [
    'Describe',
    '/mj/submit/describe',
    `{
  "base64": "data:image/png;base64,iVBORw0KGgo...",
  "botType": "MID_JOURNEY"
}`,
  ],
  [
    'Modal edit',
    '/mj/submit/modal',
    `{
  "taskId": "TASK_ID",
  "prompt": "Replace the selected area with wildflowers",
  "maskBase64": "data:image/png;base64,..."
}`,
  ],
  [
    'Create video',
    '/mj/submit/video',
    `{
  "taskId": "TASK_ID"
}`,
  ],
  [
    'Swap face',
    '/mj/insight-face/swap',
    `{
  "sourceBase64": "data:image/jpeg;base64,...",
  "targetBase64": "data:image/jpeg;base64,..."
}`,
  ],
] as const

export function MidjourneyDocs({ baseUrl }: { baseUrl: string }) {
  const { t } = useTranslation()
  const workflowSamples = useMemo<CodeSamples>(
    () => ({
      curl: `#!/usr/bin/env bash
set -euo pipefail

submit=$(curl -sS '${baseUrl}/mj/submit/imagine' \\
  -H "Authorization: Bearer $NEW_API_KEY" \\
  -H 'Content-Type: application/json' \\
  -d '{
    "prompt": "A cinematic paper-cut city at sunrise --ar 16:9",
    "botType": "MID_JOURNEY",
    "speedMode": "fast"
  }')

task_id=$(printf '%s' "$submit" | jq -r '.result // empty')
[ -n "$task_id" ] || { printf '%s\n' "$submit"; exit 1; }

while true; do
  task=$(curl -sS "${baseUrl}/mj/task/$task_id/fetch" \\
    -H "Authorization: Bearer $NEW_API_KEY")
  status=$(printf '%s' "$task" | jq -r '.status')

  case "$status" in
    SUCCESS) printf '%s\n' "$task"; break ;;
    FAILURE) printf '%s\n' "$task" >&2; exit 1 ;;
  esac
  sleep 5
done`,
      python: `import os
import time

import requests

BASE_URL = "${baseUrl}"
headers = {"Authorization": f"Bearer {os.environ['NEW_API_KEY']}"}

response = requests.post(
    f"{BASE_URL}/mj/submit/imagine",
    headers=headers,
    json={
        "prompt": "A cinematic paper-cut city at sunrise --ar 16:9",
        "botType": "MID_JOURNEY",
        "speedMode": "fast",
    },
    timeout=60,
)
response.raise_for_status()
submitted = response.json()
if submitted.get("code") not in (1, 21, 22):
    raise RuntimeError(submitted)
task_id = submitted["result"]

while True:
    response = requests.get(
        f"{BASE_URL}/mj/task/{task_id}/fetch",
        headers=headers,
        timeout=30,
    )
    response.raise_for_status()
    task = response.json()
    if task["status"] == "SUCCESS":
        print(task["imageUrl"])
        print(task.get("buttons", []))
        break
    if task["status"] == "FAILURE":
        raise RuntimeError(task.get("failReason") or task)
    time.sleep(5)`,
      javascript: `const baseURL = '${baseUrl}'
const headers = {
  Authorization: 'Bearer ' + process.env.NEW_API_KEY,
  'Content-Type': 'application/json',
}

const response = await fetch(baseURL + '/mj/submit/imagine', {
  method: 'POST',
  headers,
  body: JSON.stringify({
    prompt: 'A cinematic paper-cut city at sunrise --ar 16:9',
    botType: 'MID_JOURNEY',
    speedMode: 'fast',
  }),
})
if (!response.ok) throw new Error(await response.text())
const submitted = await response.json()
if (![1, 21, 22].includes(submitted.code)) {
  throw new Error(JSON.stringify(submitted))
}

while (true) {
  const response = await fetch(baseURL + '/mj/task/' + submitted.result + '/fetch', {
    headers: { Authorization: headers.Authorization },
  })
  if (!response.ok) throw new Error(await response.text())
  const task = await response.json()
  if (task.status === 'SUCCESS') {
    console.log(task.imageUrl, task.buttons ?? [])
    break
  }
  if (task.status === 'FAILURE') throw new Error(task.failReason)
  await new Promise((resolve) => setTimeout(resolve, 5000))
}`,
    }),
    [baseUrl]
  )

  return (
    <div className='border-border/70 mt-16 border-t pt-12'>
      <section>
        <MidjourneyHeading
          id='midjourney-overview'
          title='Midjourney task API'
          description='Submit Midjourney-compatible image tasks, track their progress, and continue completed jobs with the actions returned by the gateway.'
        />

        <div className='mt-6 grid gap-4 md:grid-cols-4'>
          {[
            ['1', 'Submit a task'],
            ['2', 'Save the result task ID'],
            ['3', 'Poll until a terminal status'],
            ['4', 'Run a returned button action'],
          ].map(([step, label]) => (
            <div
              key={step}
              className='border-border/70 bg-muted/20 rounded-lg border p-4'
            >
              <div className='text-primary text-lg font-semibold'>{step}</div>
              <p className='text-muted-foreground mt-1 text-sm leading-6'>
                {t(label)}
              </p>
            </div>
          ))}
        </div>

        <Alert className='mt-5'>
          <AlertCircle />
          <AlertTitle>{t('Authentication is required')}</AlertTitle>
          <AlertDescription>
            {t(
              'Send Authorization: Bearer $NEW_API_KEY on every Midjourney route except the public image forwarding route /mj/image/{id}. Keep API keys in trusted server-side code.'
            )}
          </AlertDescription>
        </Alert>

        <Alert className='mt-3'>
          <Route />
          <AlertTitle>{t('Fast and relax routing')}</AlertTitle>
          <AlertDescription>
            {t(
              'Set speedMode to fast or relax to select the matching routing model. Omit it to keep the deployment default. This gateway removes speedMode before forwarding unless the selected upstream is ai.t8star.org.'
            )}
          </AlertDescription>
        </Alert>
      </section>

      <section className='mt-16'>
        <MidjourneyHeading
          id='midjourney-endpoints'
          title='Midjourney endpoints'
          description='These are the Midjourney routes exposed by this deployment. Submit and follow-up operations are billed with their configured model keys; task queries do not create a new submission charge.'
        />
        <div className='border-border/70 mt-5 overflow-hidden rounded-lg border'>
          <ScrollArea className='w-full'>
            <table className='w-full min-w-[1000px] text-left text-sm'>
              <thead className='bg-muted/45 text-muted-foreground'>
                <tr>
                  {[
                    'Method',
                    'Endpoint',
                    'Purpose',
                    'Request fields',
                    'Routing model',
                  ].map((label) => (
                    <th key={label} className='px-4 py-3 font-medium'>
                      {t(label)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className='divide-border divide-y'>
                {midjourneyEndpoints.map((endpoint) => (
                  <tr key={endpoint.path} className='align-top'>
                    <td className='px-4 py-3'>
                      <Badge
                        className={
                          endpoint.method === 'GET'
                            ? 'bg-sky-600 text-white hover:bg-sky-600'
                            : 'bg-emerald-600 text-white hover:bg-emerald-600'
                        }
                      >
                        {endpoint.method}
                      </Badge>
                    </td>
                    <td className='px-4 py-3'>
                      <code>{endpoint.path}</code>
                    </td>
                    <td className='text-muted-foreground max-w-sm px-4 py-3 leading-6'>
                      {t(endpoint.purpose)}
                    </td>
                    <td className='px-4 py-3'>
                      <code className='text-xs'>{endpoint.payload}</code>
                    </td>
                    <td className='px-4 py-3'>
                      <code className='text-xs'>{endpoint.modelKey}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <ScrollBar orientation='horizontal' />
          </ScrollArea>
        </div>
      </section>

      <section className='mt-16'>
        <MidjourneyHeading
          id='midjourney-parameters'
          title='Submission parameters'
          description='JSON field names are case-sensitive. Use data URLs for Base64 images unless the selected upstream explicitly documents another format.'
        />
        <FieldTable rows={midjourneyCommonParameters} />

        <h3 className='mt-10 text-lg font-semibold'>{t('Request examples')}</h3>
        <p className='text-muted-foreground mt-2 text-sm leading-6'>
          {t(
            'Only include fields used by the selected operation. Provider-specific optional fields can vary by the configured Midjourney channel.'
          )}
        </p>
        <div className='mt-5 grid gap-4 lg:grid-cols-2'>
          {payloadExamples.map(([title, endpoint, payload]) => (
            <div
              key={endpoint}
              className='border-border/70 overflow-hidden rounded-lg border'
            >
              <div className='border-border/70 bg-muted/25 flex flex-wrap items-center gap-2 border-b px-4 py-3'>
                <span className='font-medium'>{t(title)}</span>
                <code className='text-muted-foreground ml-auto text-xs'>
                  {endpoint}
                </code>
              </div>
              <pre className='overflow-x-auto bg-[#0b1020] p-4 text-xs leading-6 text-slate-200 dark:bg-black/35'>
                <code>{payload}</code>
              </pre>
            </div>
          ))}
        </div>
      </section>

      <section className='mt-16'>
        <MidjourneyHeading
          id='midjourney-workflow'
          title='Submit and poll workflow'
          description='The submit response contains a task ID, not the final image. Poll the local task endpoint and stop on SUCCESS or FAILURE.'
        />
        <Alert className='mt-5'>
          <Workflow />
          <AlertTitle>{t('Actions stay on the original channel')}</AlertTitle>
          <AlertDescription>
            {t(
              'For variations, upscales, modal edits, and video operations, send the original taskId. The gateway automatically reuses the channel that created that task. For /mj/submit/action, send a customId exactly as returned in buttons.'
            )}
          </AlertDescription>
        </Alert>
        <div className='mt-5'>
          <CodeExample
            samples={workflowSamples}
            title={t('Complete task workflow')}
          />
        </div>
      </section>

      <section className='mt-16'>
        <MidjourneyHeading
          id='midjourney-responses'
          title='Task responses'
          description='Submission codes describe queue acceptance. The task status returned by the fetch endpoint is the authoritative lifecycle signal.'
        />
        <div className='mt-5 grid gap-4 lg:grid-cols-2'>
          <div className='border-border/70 overflow-hidden rounded-lg border'>
            <div className='border-border/70 bg-muted/25 border-b px-4 py-3 font-medium'>
              {t('Successful submission')}
            </div>
            <pre className='overflow-x-auto bg-[#0b1020] p-4 text-xs leading-6 text-slate-200 dark:bg-black/35'>
              <code>{`{
  "code": 1,
  "description": "Submit success",
  "result": "TASK_ID",
  "properties": {}
}`}</code>
            </pre>
          </div>
          <div className='border-border/70 overflow-hidden rounded-lg border'>
            <div className='border-border/70 bg-muted/25 border-b px-4 py-3 font-medium'>
              {t('Submission codes')}
            </div>
            <div className='divide-border divide-y text-sm'>
              {[
                ['1', 'Submitted'],
                ['21', 'Existing task returned'],
                ['22', 'Queued'],
                ['23', 'Queue full'],
                ['24', 'Sensitive prompt rejected'],
              ].map(([code, label]) => (
                <div key={code} className='flex items-center gap-3 px-4 py-2.5'>
                  <code className='text-primary w-8'>{code}</code>
                  <span>{t(label)}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        <h3 className='mt-10 text-lg font-semibold'>
          {t('Task object fields')}
        </h3>
        <FieldTable rows={midjourneyTaskFields} />

        <Alert className='mt-5'>
          <CheckCircle2 />
          <AlertTitle>{t('Callbacks and image forwarding')}</AlertTitle>
          <AlertDescription>
            {t(
              'notifyHook is forwarded only when callbacks are enabled by the administrator, so clients should always support polling. imageUrl may point to /mj/image/{id} when image forwarding is enabled; that forwarding route is intentionally public so image clients can load it directly.'
            )}
          </AlertDescription>
        </Alert>

        <Alert variant='destructive' className='mt-3'>
          <AlertCircle />
          <AlertTitle>{t('HTTP and upstream errors')}</AlertTitle>
          <AlertDescription>
            {t(
              'Non-successful upstream HTTP responses are returned with description, type upstream_error, and the upstream status code. Treat FAILURE, code 23, and code 24 as terminal; retry transient HTTP failures with bounded exponential backoff.'
            )}
          </AlertDescription>
        </Alert>
      </section>
    </div>
  )
}
