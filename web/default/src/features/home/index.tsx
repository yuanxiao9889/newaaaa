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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { PublicLayout } from '@/components/layout'
import { Footer } from '@/components/layout/components/footer'
import { CTA, Features, Hero, HowItWorks, Stats } from './components'

export function Home() {
  const { i18n } = useTranslation()
  const { auth } = useAuthStore()
  const isAuthenticated = !!auth.user
  const previousLanguageRef = useRef<string | null>(null)
  const [englishReady, setEnglishReady] = useState(i18n.resolvedLanguage === 'en')

  useEffect(() => {
    let mounted = true

    previousLanguageRef.current = i18n.language
    const ensureEnglish = async () => {
      if (i18n.resolvedLanguage !== 'en') {
        await i18n.changeLanguage('en')
      }
      if (mounted) {
        setEnglishReady(true)
      }
    }

    ensureEnglish()

    return () => {
      mounted = false
      const previousLanguage = previousLanguageRef.current
      if (previousLanguage && previousLanguage !== i18n.language) {
        void i18n.changeLanguage(previousLanguage)
      }
    }
  }, [i18n])

  if (!englishReady) {
    return (
      <PublicLayout
        showMainContainer={false}
        headerProps={{ showLanguageSwitcher: false }}
      >
        <main className='flex min-h-screen items-center justify-center'>
          <div className='text-muted-foreground'>Loading...</div>
        </main>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout
      showMainContainer={false}
      headerProps={{ showLanguageSwitcher: false }}
    >
      <Hero isAuthenticated={isAuthenticated} />
      <Stats />
      <Features />
      <HowItWorks />
      <CTA isAuthenticated={isAuthenticated} />
      <Footer />
    </PublicLayout>
  )
}
