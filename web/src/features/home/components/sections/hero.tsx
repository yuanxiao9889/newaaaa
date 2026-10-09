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
import { Link } from '@tanstack/react-router'
import { ArrowDown, ArrowUpRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'

import { HeroButtons } from '../hero-buttons'

interface HeroProps {
  isAuthenticated?: boolean
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const docsUrl = (status?.docs_link as string | undefined) || '/docs'
  const docsLink = docsUrl.startsWith('http') ? (
    <a href={docsUrl} target='_blank' rel='noopener noreferrer' />
  ) : (
    <Link to={docsUrl} />
  )

  return (
    <section className='home-hero home-shell' aria-labelledby='home-title'>
      <div className='home-masthead'>
        <p className='home-eyebrow'>
          {t('AI Application Infrastructure Foundation')}
        </p>
        <span className='home-eyebrow hidden sm:block'>
          OpenAI / Claude / Gemini
        </span>
      </div>

      <div className='home-hero-heading'>
        <h1 id='home-title' className='home-title'>
          <span>{t('One gateway.')}</span>
          <span className='home-title-second'>
            {t('Infinite possibilities.')}
          </span>
        </h1>
        <ArrowUpRight
          className='home-hero-arrow'
          strokeWidth={0.7}
          aria-hidden='true'
        />
      </div>

      <div className='home-hero-detail grid gap-8 md:grid-cols-2 md:gap-16'>
        <div className='flex flex-wrap items-start gap-3'>
          <HeroButtons isAuthenticated={!!props.isAuthenticated} />
          <Button
            variant='ghost'
            className='home-action home-action-docs'
            render={docsLink}
          >
            {t('Docs')}
            <ArrowUpRight className='size-4' aria-hidden='true' />
          </Button>
        </div>
        <p className='home-lead'>
          {t(
            'Access a vast selection of models via a standard, unified API protocol. Power AI applications, manage digital assets, and connect the Future.'
          )}
        </p>
      </div>

      <div className='home-hero-foot flex flex-wrap items-center justify-between gap-x-8 gap-y-5'>
        <div>
          <div className='flex flex-wrap items-center gap-x-5 gap-y-2 text-xs'>
            <span className='home-eyebrow'>{t('Supported Applications')}</span>
            <a
              href='https://cherry-ai.com'
              target='_blank'
              rel='noopener noreferrer'
              className='home-text-link'
            >
              Cherry Studio
              <ArrowUpRight className='size-3' aria-hidden='true' />
            </a>
            <a
              href='https://ccswitch.io'
              target='_blank'
              rel='noopener noreferrer'
              className='home-text-link'
            >
              CC Switch
              <ArrowUpRight className='size-3' aria-hidden='true' />
            </a>
          </div>
          <p className='text-muted-foreground mt-3 max-w-md text-xs leading-5'>
            {t(
              'Supports one-click configuration and perfectly adapts to NewAPI multi-protocol configuration.'
            )}
          </p>
        </div>
        <a href='#capabilities' className='home-text-link home-eyebrow gap-5'>
          {t('Explore capabilities')}
          <ArrowDown className='size-3.5' aria-hidden='true' />
        </a>
      </div>
    </section>
  )
}
