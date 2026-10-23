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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  bridgeManagementQueryKey,
  checkBridge,
  getBridgeManagement,
} from './api'
import { BridgeObservations } from './components/observations'
import { BridgeOverview } from './components/overview'
import { PolicyEditor } from './components/policy-editor'
import { BridgeRuntime } from './components/runtime'
import type { BridgeManagementData } from './types'

export function BridgeManagement() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const canEdit = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const query = useQuery({
    queryKey: bridgeManagementQueryKey,
    queryFn: getBridgeManagement,
    refetchOnWindowFocus: false,
  })
  const check = useMutation({
    mutationFn: checkBridge,
    retry: false,
    onSuccess: (data) =>
      client.setQueryData<BridgeManagementData>(
        bridgeManagementQueryKey,
        (current) => {
          if (current && current.version > data.version) {
            return { ...current, last_check: data.last_check }
          }
          return data
        }
      ),
    onError: (error) => handleServerError(error),
  })
  const storage = query.data?.reference_storage

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Bridge Management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          disabled={!query.data || check.isPending}
          onClick={() => check.mutate()}
        >
          {check.isPending ? t('Checking...') : t('Run free bridge check')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <Alert>
            <AlertTitle>
              {t('Bridge first, original route as fallback')}
            </AlertTitle>
            <AlertDescription>
              {t(
                'The bridge forwards API traffic to the relay. Domestic source files are not transferred into bridge storage. A generation submission with an unknown outcome is never resubmitted merely because its route failed.'
              )}
            </AlertDescription>
          </Alert>
          {query.isPending && (
            <LoadingState message={t('Loading bridge settings...')} />
          )}
          {query.isError && !query.data && (
            <ErrorState
              title={t('Failed to load bridge settings')}
              onRetry={() => void query.refetch()}
            />
          )}
          {query.data && (
            <>
              <BridgeOverview data={query.data} />
              <PolicyEditor data={query.data} canEdit={canEdit} />
              <SettingsCard title={t('Relay reference storage')}>
                <div className='space-y-2 text-sm'>
                  <p className='text-muted-foreground'>
                    {t(
                      'Image references follow the retention duration configured in Retry policy. New preparation or task use renews retention; status checks do not. Domestic video inputs remain on their original service.'
                    )}
                  </p>
                  {!storage?.enabled && (
                    <p>
                      {t('Relay direct upload is not enabled on this node.')}
                    </p>
                  )}
                  {storage?.enabled && (
                    <>
                      <Badge
                        variant={
                          storage.available ? 'secondary' : 'destructive'
                        }
                      >
                        {storage.available ? t('Available') : t('Unavailable')}
                      </Badge>
                      {storage.measured && (
                        <>
                          <p>
                            {t(
                              'Stored: {{stored}} MiB · Reserved: {{reserved}} MiB · Limit: {{limit}} MiB',
                              {
                                stored: formatNumber(
                                  storage.stored_bytes / 1048576,
                                  locale
                                ),
                                reserved: formatNumber(
                                  storage.reserved_bytes / 1048576,
                                  locale
                                ),
                                limit: formatNumber(
                                  storage.max_cache_bytes / 1048576,
                                  locale
                                ),
                              }
                            )}
                          </p>
                          <p>
                            {t(
                              'Ready references: {{count}} · Active uploads: {{uploads}}',
                              {
                                count: formatNumber(
                                  storage.ready_assets,
                                  locale
                                ),
                                uploads: formatNumber(
                                  storage.active_uploads,
                                  locale
                                ),
                              }
                            )}
                          </p>
                          <p>
                            {t(
                              'Available disk space: {{free}} GiB · Required reserve: {{reserve}} GiB',
                              {
                                free: formatNumber(
                                  storage.free_bytes / 1073741824,
                                  locale
                                ),
                                reserve: formatNumber(
                                  storage.required_free_bytes / 1073741824,
                                  locale
                                ),
                              }
                            )}
                          </p>
                        </>
                      )}
                    </>
                  )}
                </div>
              </SettingsCard>
              <BridgeRuntime data={query.data} canEdit={canEdit} />
              <BridgeObservations data={query.data} />
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
