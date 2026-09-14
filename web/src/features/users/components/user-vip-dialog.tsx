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
import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { handleServerError } from '@/lib/handle-server-error'

import { updateUserVIP } from '../api'
import type { User } from '../types'

interface UserVIPDialogProps {
  user: User
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function UserVIPDialog(props: UserVIPDialogProps) {
  const { t } = useTranslation()
  const mutation = useMutation({
    mutationFn: () => updateUserVIP(props.user.id, !props.user.is_vip),
    onSuccess: () => {
      toast.success(t('VIP membership updated'))
      props.onOpenChange(false)
      props.onSuccess()
    },
    onError: (error) => handleServerError(error),
  })

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={(open) => {
        if (!mutation.isPending) props.onOpenChange(open)
      }}
      title={props.user.is_vip ? t('Cancel VIP') : t('Enable VIP')}
      desc={
        props.user.is_vip
          ? t(
              'Cancel VIP for {{username}} (ID: {{id}})? The 5-reward limit per invited user will apply again, including rewards already granted during VIP membership. Existing rewards are kept.',
              { username: props.user.username, id: props.user.id }
            )
          : t(
              'Enable VIP for {{username}} (ID: {{id}})? Future valid wallet top-ups from invited users will earn 10% rewards without a count limit. Past skipped rewards will not be paid retroactively.',
              { username: props.user.username, id: props.user.id }
            )
      }
      confirmText={props.user.is_vip ? t('Cancel VIP') : t('Enable VIP')}
      destructive={!!props.user.is_vip}
      isLoading={mutation.isPending}
      handleConfirm={() => mutation.mutate()}
    />
  )
}
