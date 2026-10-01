# Staged Quota Warning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Display quota-warning thresholds in the site's configured unit and send at most one warning at each 100%, 50%, and 20% stage per low-balance cycle.

**Architecture:** Keep `quota_warning_threshold` stored in raw quota units for compatibility. Add a service-level warning-stage coordinator backed by an atomic Redis script with an in-memory fallback, then route wallet and subscription warnings through one shared sender using the post-consumption balance. Convert the profile form between display units and raw quota with the existing currency helpers.

**Tech Stack:** Go 1.25, Gin service layer, go-redis v8, testify, React 19, TypeScript, Base UI/shadcn Input, i18next, Bun.

---

### Task 1: Add deterministic warning-stage calculation and deduplication

**Files:**
- Create: `service/quota_notify.go`
- Create: `service/quota_notify_test.go`

- [ ] **Step 1: Write failing stage and memory-state tests**

Create table-driven tests using `testify/assert` and `testify/require` for these contracts:

```go
func TestQuotaWarningStageBoundaries(t *testing.T) {
    tests := []struct {
        name      string
        remaining int64
        threshold int64
        want      quotaWarningStage
    }{
        {"at threshold", 100, 100, quotaWarningStageNone},
        {"below threshold", 99, 100, quotaWarningStageThreshold},
        {"at half", 50, 100, quotaWarningStageThreshold},
        {"below half", 49, 100, quotaWarningStageHalf},
        {"at twenty percent", 20, 100, quotaWarningStageHalf},
        {"below twenty percent", 19, 100, quotaWarningStageCritical},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            assert.Equal(t, tt.want, calculateQuotaWarningStage(tt.remaining, tt.threshold))
        })
    }
}

func TestClaimMemoryQuotaWarningStageOncePerCycle(t *testing.T) {
    resetQuotaWarningMemoryState()
    key := quotaWarningStateKey(7, BillingSourceWallet, 0)

    claimed, err := claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
    require.NoError(t, err)
    assert.True(t, claimed)

    claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
    require.NoError(t, err)
    assert.False(t, claimed)

    claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageHalf)
    require.NoError(t, err)
    assert.True(t, claimed)

    resetMemoryQuotaWarningStage(key)
    claimed, err = claimMemoryQuotaWarningStage(key, 100, quotaWarningStageThreshold)
    require.NoError(t, err)
    assert.True(t, claimed)
}
```

Also test direct entry into stage 3, threshold changes, and key isolation between wallet and subscription IDs.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```powershell
& 'C:\Users\Administrator\.codex\tools\go\bin\go.exe' test ./service -run 'TestQuotaWarning' -count=1
```

Expected: compilation failure because the stage types and functions do not exist.

- [ ] **Step 3: Implement stage calculation and state claiming**

Create `service/quota_notify.go` with:

```go
type quotaWarningStage int

const (
    quotaWarningStageNone quotaWarningStage = iota
    quotaWarningStageThreshold
    quotaWarningStageHalf
    quotaWarningStageCritical
)

const quotaWarningStateTTL = 30 * 24 * time.Hour

type quotaWarningState struct {
    Threshold int64
    Stage     quotaWarningStage
    ExpiresAt time.Time
}

func calculateQuotaWarningStage(remaining, threshold int64) quotaWarningStage {
    if threshold <= 0 || remaining >= threshold {
        return quotaWarningStageNone
    }
    criticalBoundary := threshold/5
    if threshold%5 != 0 {
        criticalBoundary++
    }
    if remaining < criticalBoundary {
        return quotaWarningStageCritical
    }
    halfBoundary := threshold/2 + threshold%2
    if remaining < halfBoundary {
        return quotaWarningStageHalf
    }
    return quotaWarningStageThreshold
}
```

Use `sync.Map` for the fallback store. Implement `claimMemoryQuotaWarningStage` with `LoadOrStore`/`CompareAndSwap` so a stage can only be claimed once, refresh the 30-day expiry on duplicate checks, and treat a threshold change as a fresh cycle. Implement `resetMemoryQuotaWarningStage` and a test-only reset by ranging and deleting entries.

For Redis, define a `redis.NewScript` that atomically reads `threshold:stage`, returns `0` if the same threshold already reached an equal or higher stage, otherwise stores the new state with a 30-day expiry and returns `1`. Use `common.RedisDel` to reset when the calculated stage is none.

Expose one internal coordinator:

```go
func claimQuotaWarningStage(
    userID int,
    source string,
    sourceID int,
    remaining int64,
    threshold int64,
) (quotaWarningStage, bool, error)
```

- [ ] **Step 4: Run the tests and verify GREEN**

Run the same targeted command. Expected: PASS.

### Task 2: Send wallet and subscription warnings through the staged coordinator

**Files:**
- Modify: `logger/logger.go:149-172`
- Create: `logger/logger_test.go`
- Modify: `service/quota.go:457-550`
- Modify: `service/quota_notify.go`
- Modify: `service/quota_notify_test.go`

- [ ] **Step 1: Write failing formatting and post-balance tests**

Add a logger test proving `FormatQuota64` uses the configured site unit without narrowing to `int`:

```go
func TestFormatQuota64UsesCNYDisplay(t *testing.T) {
    oldType := operation_setting.GetGeneralSetting().QuotaDisplayType
    oldRate := operation_setting.USDExchangeRate
    oldUnit := common.QuotaPerUnit
    t.Cleanup(func() {
        operation_setting.GetGeneralSetting().QuotaDisplayType = oldType
        operation_setting.USDExchangeRate = oldRate
        common.QuotaPerUnit = oldUnit
    })

    operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
    operation_setting.USDExchangeRate = 7
    common.QuotaPerUnit = 500000

    assert.Equal(t, "¥14.000000", FormatQuota64(1000000))
}
```

Add service tests for a small pure helper that returns wallet post-balance as `initialQuota - actualQuota`, and subscription post-balance as `total - usedAfterPreConsume - postDelta`.

- [ ] **Step 2: Run targeted tests and verify RED**

```powershell
& 'C:\Users\Administrator\.codex\tools\go\bin\go.exe' test ./logger ./service -run 'TestFormatQuota64|TestQuotaWarning' -count=1
```

Expected: FAIL because `FormatQuota64` and the post-balance helper are missing.

- [ ] **Step 3: Implement 64-bit display formatting**

Change `FormatQuota` to delegate without changing existing output:

```go
func FormatQuota(quota int) string {
    return FormatQuota64(int64(quota))
}

func FormatQuota64(quota int64) string {
    q := float64(quota)
    // Preserve the current USD/CNY/CUSTOM/TOKENS branches.
}
```

Use `%d` with the `int64` value in Tokens mode. Do not use a bare `int(remaining)` cast.

- [ ] **Step 4: Implement the shared staged notification sender**

Move the duplicated content construction into `sendQuotaWarningNotify`. It must:

1. Resolve the user's threshold, falling back to `common.QuotaRemindThreshold`.
2. Call `claimQuotaWarningStage` with wallet source ID `0` or the subscription ID.
3. Return without sending when the stage is none or already claimed.
4. Include stage labels `100%`, `50%`, or `20%` in the message.
5. Format the post-consumption balance with `logger.FormatQuota64`.
6. Preserve the existing Email/Webhook HTML link and Bark/Gotify short-message behavior.

Update wallet handling to pass:

```go
remaining := int64(relayInfo.UserQuota) - int64(quota+preConsumedQuota)
```

Update subscription handling to pass:

```go
remaining := relayInfo.SubscriptionAmountTotal -
    relayInfo.SubscriptionAmountUsedAfterPreConsume -
    relayInfo.SubscriptionPostDelta
```

- [ ] **Step 5: Run targeted tests and verify GREEN**

Run the same targeted command. Expected: PASS.

### Task 3: Display and save the threshold in the configured website unit

**Files:**
- Modify: `web/default/src/features/profile/components/tabs/notification-tab.tsx`
- Temporarily create/delete: `web/default/scripts/add-missing-keys.mjs`
- Modify through script: `web/default/src/i18n/locales/en.json`
- Modify through script: `web/default/src/i18n/locales/zh.json`
- Modify through script: `web/default/src/i18n/locales/fr.json`
- Modify through script: `web/default/src/i18n/locales/ja.json`
- Modify through script: `web/default/src/i18n/locales/ru.json`
- Modify through script: `web/default/src/i18n/locales/vi.json`

- [ ] **Step 1: Add display-value state and conversion imports**

Import:

```ts
import { getCurrencyDisplay } from '@/lib/currency'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
```

Add `quotaWarningAmount` state initialized from `DEFAULT_QUOTA_WARNING_THRESHOLD`. When profile settings load, keep `settings.quota_warning_threshold` as the raw quota and set the input amount with `quotaUnitsToDollars(rawThreshold)`.

- [ ] **Step 2: Convert the displayed amount back to raw quota on save**

Construct the request without mutating the form state:

```ts
const threshold = parseQuotaFromDollars(quotaWarningAmount)
const response = await updateUserSettings({
  ...settings,
  quota_warning_threshold: threshold,
})
```

For the input, use `type='number'`, `min='0'`, `step='any'`, bind it to `quotaWarningAmount`, and update that amount directly.

- [ ] **Step 3: Show the configured unit and staged-warning explanation**

Derive the unit from `getCurrencyDisplay().meta`: use the currency/custom symbol, or `t('Tokens')` for token mode. Render the label as `Quota Warning Threshold (¥)` / `($)` / `(Tokens)`. Replace the old hint with:

```tsx
t(
  'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold'
)
```

- [ ] **Step 4: Add all six translations through the sanctioned script**

Create `web/default/scripts/add-missing-keys.mjs` using the exact structure required by the i18n skill and add:

```js
const newKeys = {
  en: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold',
  },
  zh: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      '余额低于此阈值的 100%、50% 和 20% 时将分别通知一次',
  },
  fr: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      'Une notification est envoyée une fois lorsque le solde passe sous 100 %, 50 % et 20 % de ce seuil',
  },
  ja: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      '残高がこのしきい値の 100%、50%、20% を下回ったときに、それぞれ一度通知します',
  },
  ru: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      'Уведомление отправляется один раз при снижении баланса ниже 100 %, 50 % и 20 % этого порога',
  },
  vi: {
    'Notifications are sent once when balance falls below 100%, 50%, and 20% of this threshold':
      'Thông báo được gửi một lần khi số dư giảm xuống dưới 100%, 50% và 20% của ngưỡng này',
  },
}
```

Run the script, run `bun run i18n:sync`, then delete the temporary script.

- [ ] **Step 5: Verify the frontend**

Install existing dependencies without changing the lockfile if needed:

```powershell
& 'C:\Users\Administrator\.codex\tools\bun\bun-windows-x64\bun.exe' install --frozen-lockfile
```

Then run:

```powershell
& 'C:\Users\Administrator\.codex\tools\bun\bun-windows-x64\bun.exe' run typecheck
& 'C:\Users\Administrator\.codex\tools\bun\bun-windows-x64\bun.exe' run lint
& 'C:\Users\Administrator\.codex\tools\bun\bun-windows-x64\bun.exe' run format:check
& 'C:\Users\Administrator\.codex\tools\bun\bun-windows-x64\bun.exe' run build
```

Expected: all commands exit 0.

### Task 4: Final regression verification

**Files:**
- Verify all files changed by Tasks 1-3.

- [ ] **Step 1: Run focused backend tests**

```powershell
& 'C:\Users\Administrator\.codex\tools\go\bin\go.exe' test ./logger ./service -run 'TestFormatQuota64|TestQuotaWarning' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run package tests**

```powershell
& 'C:\Users\Administrator\.codex\tools\go\bin\go.exe' test ./logger ./service -count=1
```

Expected: PASS. If the pre-existing channel-affinity shared-state test fails while its isolated run passes, record it separately and do not attribute it to this change.

- [ ] **Step 3: Inspect the final diff**

```powershell
git diff --check
git status --short
git diff -- service/quota_notify.go service/quota_notify_test.go service/quota.go logger/logger.go logger/logger_test.go web/default/src/features/profile/components/tabs/notification-tab.tsx web/default/src/i18n/locales
```

Confirm the unrelated existing change in `web/default/src/features/keys/components/api-keys-table.tsx` remains untouched.

