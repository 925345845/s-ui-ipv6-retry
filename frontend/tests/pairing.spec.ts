import { expect, test } from '@playwright/test'

test('IPv4 input is visible and submitted in order; successful links are shown', async ({ page }) => {
  let pools: any[] = []
  let submitted: any
  await page.route('**/app/api/**', async route => {
    const path = new URL(route.request().url()).pathname
    let obj: any = { onlines: [], inbounds: [], clients: [] }
    if (path.endsWith('/relay/create')) {
      submitted = route.request().postDataJSON()
      pools = [{ id: 1, name: 'browser-test', count: 2, port_start: 40000, items: [
        { export: '198.51.100.1:40000:retry1:pass1', upstream_server: '203.0.113.10', upstream_port: 1080, ipv6: '2001:db8::10', listen_port: 40000, refresh_token: 'test-token-1' },
        { export: '198.51.100.1:40001:retry2:pass2', upstream_server: '203.0.113.11', upstream_port: 1080, ipv6: '2001:db8::11', listen_port: 40001, refresh_token: 'test-token-2' },
      ] }]
      obj = pools[0]
    } else if (path.endsWith('/relay')) {
      obj = { pools, ipv6: [{ interface: 'eth0', address: '2001:db8::1', prefix: 64 }], capabilities: { can_add_system_ipv6: true } }
    }
    await route.fulfill({ json: { success: true, msg: '', obj } })
  })
  await page.goto('/app/')
  await expect(page.getByRole('heading', { name: 'IPv4 / IPv6 配对工作台' })).toBeVisible()
  const input = page.getByRole('textbox', { name: '1. IPv4 上游列表（必填）' })
  await expect(input).toBeVisible()
  await expect(input).toBeInViewport()
  await expect(page.getByRole('button', { name: '创建 0 条配对' })).toBeDisabled()
  const lines = '203.0.113.10:1080:user1:pass1\nsocks5://user2:pass2@203.0.113.11:1080'
  await input.fill(lines)
  await expect(page.getByText('已输入 2 条上游，最多 500 条')).toBeVisible()
  await page.screenshot({ path: 'test-results/ipv4-input-desktop.png', fullPage: true })
  await page.getByRole('button', { name: '创建 2 条配对' }).click()
  await expect(page.getByText('已创建 2 条配对，端口 40000–40001。')).toBeVisible()
  expect(submitted.upstream_text).toBe(lines)
  expect(submitted.count).toBe(2)
  expect(submitted.mode).toBe('paired')
  expect(submitted.domain_strategy).toBe('prefer_ipv6')
  expect(submitted.upstreams).toEqual([])
  await expect(page.getByRole('textbox', { name: 'browser-test 连接链接' })).toHaveValue(/40000.*\n.*40001/)
  await page.getByText('查看 IPv4 / IPv6 对应关系和刷新链接').click()
  await expect(page.getByRole('cell', { name: '203.0.113.10:1080' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'IPv6 刷新链接' }).first()).toHaveValue(/\/app\/refresh\/test-token-1$/)
  await page.screenshot({ path: 'test-results/pairing-result-desktop.png', fullPage: true })
})

test('IPv4 field remains usable on a narrow screen and accepts provider JSON', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.route('**/app/api/**', route => route.fulfill({ json: { success: true, msg: '', obj: { pools: [], ipv6: [], onlines: [] } } }))
  await page.goto('/app/')
  const input = page.getByRole('textbox', { name: '1. IPv4 上游列表（必填）' })
  await expect(input).toBeVisible()
  await input.fill(JSON.stringify([{ ip: '203.0.113.10', port: 1080 }, { ip: '203.0.113.11', port: 1080 }]))
  await expect(page.getByText('已输入 2 条上游，最多 500 条')).toBeVisible()
  const box = await input.boundingBox()
  expect(box!.width).toBeGreaterThan(200)
  expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  await page.screenshot({ path: 'test-results/ipv4-input-mobile.png', fullPage: true })
})
