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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { HistoricalRechargeDialog } from './components/historical-recharge-dialog'
import { RechargeRecordsTable } from './components/recharge-records-table'

export function RechargeRecords() {
  const { t } = useTranslation()
  const [reviewOpen, setReviewOpen] = useState(false)
  const user = useAuthStore((state) => state.auth.user)
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Recharge Records')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {hasPermission(user, 'recharge_record', 'write') && (
            <Button
              variant='outline'
              className='self-start'
              onClick={() => setReviewOpen(true)}
            >
              {t('Review historical recharges')}
            </Button>
          )}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <RechargeRecordsTable mode='admin' />
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {reviewOpen && (
        <HistoricalRechargeDialog
          open={reviewOpen}
          onOpenChange={setReviewOpen}
        />
      )}
    </>
  )
}
