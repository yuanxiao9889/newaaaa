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
const FRONTEND_RECOVERY_KEY = 'oopii_chunk_recovery'

function getFrontendBuildId(): string {
  if (typeof document === 'undefined') return 'unknown'

  for (const script of document.scripts) {
    if (script.src.includes('/static/js/index.')) return script.src
  }

  return 'unknown'
}

export function markFrontendRecoveryAttempt(): boolean {
  if (typeof window === 'undefined') return false

  const recoveryId = `${getFrontendBuildId()}|${window.location.pathname}${window.location.search}`
  try {
    if (window.sessionStorage.getItem(FRONTEND_RECOVERY_KEY) === recoveryId) {
      return false
    }
    window.sessionStorage.setItem(FRONTEND_RECOVERY_KEY, recoveryId)
  } catch {
    // Reload recovery still works when sessionStorage is unavailable, but it
    // cannot safely guard against a loop, so do not reload in that case.
    return false
  }

  return true
}

export function clearFrontendRecoveryAttempt(): void {
  if (typeof window === 'undefined') return

  try {
    window.sessionStorage.removeItem(FRONTEND_RECOVERY_KEY)
  } catch {
    /* empty */
  }
}
