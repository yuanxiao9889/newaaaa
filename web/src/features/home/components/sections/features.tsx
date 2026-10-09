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
import { Braces, Network, Radio, ShieldCheck, Wallet } from 'lucide-react'
import { useTranslation } from 'react-i18next'

export function Features() {
  const { t } = useTranslation()
  const features = [
    {
      id: '01',
      icon: Network,
      title: t('Unified model access'),
      description: t(
        'Connect through OpenAI, Claude, Gemini, and other compatible API routes'
      ),
      detail: 'OpenAI / Claude / Gemini',
    },
    {
      id: '02',
      icon: Radio,
      title: t('Intelligent routing'),
      description: t(
        'Support for high concurrency with automatic load balancing'
      ),
      detail: t('Load Balancing'),
    },
    {
      id: '03',
      icon: Wallet,
      title: t('Transparent Billing'),
      description: t('Pay-as-you-go with real-time usage monitoring'),
      detail: t('Cost Tracking'),
    },
  ]

  return (
    <section
      id='capabilities'
      className='home-section home-capabilities scroll-mt-24'
      aria-labelledby='capabilities-title'
    >
      <div className='home-shell'>
        <div className='mb-14 flex flex-col justify-between gap-6 md:flex-row md:items-end'>
          <div>
            <p className='home-eyebrow mb-6'>01 / {t('Core Features')}</p>
            <h2 id='capabilities-title' className='home-heading'>
              {t('Built for developers,')}
              <br />
              <span className='home-heading-secondary'>
                {t('designed for scale')}
              </span>
            </h2>
          </div>
          <p className='text-muted-foreground max-w-xs text-base leading-7'>
            {t('Compatible API routes for common AI application workflows')}
          </p>
        </div>
        <div className='home-feature-grid grid md:grid-cols-3'>
          {features.map((feature) => (
            <article
              key={feature.id}
              className='home-feature flex min-w-0 flex-col'
            >
              <div
                className='mb-12 flex items-start justify-between'
                aria-hidden='true'
              >
                <span className='home-feature-number'>{feature.id}</span>
                <feature.icon size={25} strokeWidth={1} />
              </div>
              <h3 className='font-serif text-2xl font-normal tracking-tight'>
                {feature.title}
              </h3>
              <p className='home-feature-description mt-4 flex-1 text-base leading-7'>
                {feature.description}
              </p>
              <div className='mt-10 border-t border-current/20 pt-5 font-mono text-[11px] tracking-wide'>
                {feature.detail}
              </div>
            </article>
          ))}
        </div>
        <div className='mt-10 grid gap-8 md:grid-cols-2'>
          <div className='flex items-start gap-4'>
            <ShieldCheck
              className='mt-1 size-5 shrink-0'
              strokeWidth={1.5}
              aria-hidden='true'
            />
            <div>
              <h3 className='text-sm font-medium'>{t('Secure & Reliable')}</h3>
              <p className='text-muted-foreground mt-2 text-sm leading-6'>
                {t(
                  'Enterprise-grade security with comprehensive permission management'
                )}
              </p>
            </div>
          </div>
          <div className='flex items-start gap-4'>
            <Braces
              className='mt-1 size-5 shrink-0'
              strokeWidth={1.5}
              aria-hidden='true'
            />
            <div>
              <h3 className='text-sm font-medium'>{t('Open Source')}</h3>
              <p className='text-muted-foreground mt-2 text-sm leading-6'>
                {t('Community driven, self-hosted, and extensible')}
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
