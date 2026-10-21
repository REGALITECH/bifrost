import { test, expect } from '../../core/fixtures/base.fixture'
import type { CreatePricingOverrideRequest, PricingOverride } from '../../../../ui/lib/types/governance'

test('creates and edits decision pricing without losing a small input rate or a zero output rate', async ({ page }) => {
  const name = `Jev pricing ${Date.now()}`
  let saved: PricingOverride | undefined
  const writes: CreatePricingOverrideRequest[] = []

  // Exercise the real form and API serialization without changing a running
  // gateway's billing configuration or calling an inference provider.
  await page.route('**/api/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path.startsWith('/api/governance/pricing-overrides')) {
      if (request.method() === 'POST' || request.method() === 'PUT') {
        const body: CreatePricingOverrideRequest = request.postDataJSON()
        writes.push(body)
        saved = {
          id: 'decision-pricing-test',
          name: body.name,
          scope_kind: body.scope_kind,
          provider_id: body.provider_id,
          match_type: body.match_type,
          pattern: body.pattern,
          request_types: body.request_types,
          pricing_patch: JSON.stringify(body.patch),
          created_at: '2026-10-21T00:00:00Z',
          updated_at: '2026-10-21T00:00:00Z',
        }
        await route.fulfill({ status: request.method() === 'POST' ? 201 : 200, json: { pricing_override: saved } })
      } else {
        await route.fulfill({ json: { pricing_overrides: saved ? [saved] : [], count: saved ? 1 : 0, total_count: saved ? 1 : 0 } })
      }
      return
    }
    if (path === '/api/config') {
      await route.fulfill({ json: { is_db_connected: true, is_logs_connected: true, client_config: {} } })
    } else if (path === '/api/providers') {
      await route.fulfill({ json: { providers: [{ name: 'typesafe', keys: [] }] } })
    } else if (path === '/api/keys') {
      await route.fulfill({ json: [] })
    } else if (path === '/api/version') {
      await route.fulfill({ json: 'test' })
    } else {
      await route.fulfill({ json: {} })
    }
  })

  await page.goto('/workspace/custom-pricing/overrides')
  await page.getByTestId('pricing-override-create-btn').click()
  await page.getByTestId('pricing-override-name-input').fill(name)
  await page.getByTestId('pricing-override-provider-select').click()
  await page.getByRole('option', { name: /typesafe/i }).click()
  await page.getByTestId('pricing-override-pattern-input').fill('jev-latest')
  await page.getByTestId('pricing-override-request-types-btn').click()
  const decisions = page.getByTestId('pricing-override-request-type-checkbox-decisions')
  await expect(decisions).toBeVisible()
  await decisions.click()
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('pricing-override-request-types-btn')).toHaveText('Decisions')

  await page.getByTestId('pricing-field-activate-input_cost_per_token').click()
  await expect(page.getByTestId('pricing-field-activate-cache_read_input_token_cost')).toHaveCount(0)
  await expect(page.getByTestId('pricing-group-toggle-audio')).toHaveCount(0)
  await page.getByTestId('pricing-override-field-input-input_cost_per_token').fill('0.000000042')
  await page.getByTestId('pricing-field-activate-output_cost_per_token').click()
  await page.getByTestId('pricing-override-field-input-output_cost_per_token').fill('0')
  await page.getByTestId('pricing-override-save-btn').click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0]).toEqual({
    name,
    scope_kind: 'provider',
    provider_id: 'typesafe',
    match_type: 'exact',
    pattern: 'jev-latest',
    request_types: ['decisions'],
    patch: { input_cost_per_token: 0.000000042, output_cost_per_token: 0 },
  })

  // Reload so edit hydration uses the serialized API response rather than
  // the just-submitted form or the optimistic client cache.
  await page.reload()
  await page.getByTestId('pricing-override-actions-btn-decision-pricing-test').click()
  await page.getByTestId('pricing-override-edit-btn-decision-pricing-test').click()
  await expect(page.getByTestId('pricing-override-request-types-btn')).toHaveText('Decisions')
  await expect(page.getByTestId('pricing-override-field-input-input_cost_per_token')).toHaveValue(String(0.000000042))
  await expect(page.getByTestId('pricing-override-field-input-output_cost_per_token')).toHaveValue('0')
  await page.getByTestId('pricing-override-field-input-input_cost_per_token').fill('0.00000004')
  await page.getByTestId('pricing-override-save-btn').click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[1].request_types).toEqual(['decisions'])
  expect(writes[1].patch).toEqual({ input_cost_per_token: 0.00000004, output_cost_per_token: 0 })
})
