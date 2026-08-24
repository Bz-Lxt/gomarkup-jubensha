import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

/**
 * 端到端用例。
 *
 * 定位：只测「必须有真实浏览器才能发现」的问题。席位并发、幂等、离线补齐
 * 这些后端契约已由 tests/api_smoke.py 与 tests/ws_smoke.py 覆盖，这里不重复。
 * 本文件专攻三类：
 *   1. 渲染与布局（墙的分栏、卡片文案、移动端）；
 *   2. 前端本地状态与服务端真相的对账（乐观气泡、席位数字实时刷新）；
 *   3. 交付入口是否真的能用（演示账号按钮）。
 *
 * 全程 Mock/离线：本项目零外部计费依赖，单轮花费 ¥0。
 */

const API = process.env.API_BASE ?? 'http://backend:8080'
const DEMO = { username: 'alice', password: 'jbs12345' }

/** 后端直连的建号/开车工具。E2E 用 API 铺状态、用 UI 做断言。 */
async function register(req: APIRequestContext) {
  const username = 'e2e' + Math.random().toString(36).slice(2, 10)
  const res = await req.post(`${API}/api/auth/register`, {
    data: { username, password: 'e2e12345', nickname: username, city: '测试城' },
  })
  expect(res.ok(), `注册应成功，实际 ${res.status()} ${await res.text()}`).toBeTruthy()
  const body = await res.json()
  return {
    username,
    nickname: body.data.user.nickname as string,
    userID: body.data.user.id as number,
    token: body.data.tokens.access_token as string,
  }
}

async function createRoom(req: APIRequestContext, token: string, seats = 6) {
  const start = new Date(Date.now() + 6 * 3600_000).toISOString()
  const res = await req.post(`${API}/api/rooms`, {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      title: '端到端测试专用车，勿上',
      script_name: 'E2E-' + Math.random().toString(36).slice(2, 8),
      venue_name: 'E2E 测试密室',
      city: '测试城',
      address: '测试路 1 号',
      room_type: 'SCRIPT',
      difficulty: 3,
      theme: '硬核推理',
      notes: '端到端测试创建',
      start_at: start,
      male_seats: 0,
      female_seats: 0,
      any_seats: seats,
      min_viable: 2,
      owner_seat: 'ANY',
    },
  })
  expect(res.ok(), `开车应成功，实际 ${res.status()} ${await res.text()}`).toBeTruthy()
  const body = await res.json()
  return { id: body.data.room.id as number, script: body.data.room.script_name as string }
}

/**
 * 把令牌塞进 localStorage 以跳过登录 UI。
 *
 * 必须在导航「之前」用 addInitScript 写入：登录态是在应用启动时读取的，
 * 页面加载完再写就已经晚了一步，只会看到未登录的界面。
 * 键名与 frontend-user/src/lib/api.ts 中的 tokens 保持一致。
 */
async function authenticate(page: Page, token: string) {
  await page.addInitScript(
    ([t]) => {
      localStorage.setItem('jbs.access', t)
      localStorage.setItem('jbs.refresh', t)
    },
    [token],
  )
}

test.describe('拼车墙渲染', () => {
  test('卡片给出席位缺口与倒计时', async ({ page }) => {
    await page.goto('/')

    // 「N缺M」是本产品的核心信息，必须出现在墙上而不是藏在详情页。
    await expect(page.getByText(/\d+缺\d+|\d+人满员/).first()).toBeVisible()
    // 倒计时是「再不来车就炸了」这个卖点的载体。
    await expect(page.getByText(/\d+分\d+秒|\d+小时\d+分|\d+天\d+小时/).first()).toBeVisible()
  })

  test('宽屏下四个状态分栏同时可见', async ({ page }, testInfo) => {
    // 手机上只渲染选中的那一列（DesignSpec §4.2），因此这条只在桌面成立。
    test.skip(testInfo.project.name !== 'desktop', '仅桌面端项目执行')
    await page.goto('/')
    for (const col of ['招募中', '开局中', '已收工']) {
      await expect(page.getByRole('heading', { name: new RegExp(col) }).first()).toBeVisible()
    }
  })

  test('未登录也能看墙，且登录入口始终有可读名称', async ({ page }) => {
    await page.goto('/')
    // 用 accessible name 定位而非可见文字：窄屏下按钮塌成纯图标，
    // 这条断言同时守住了「图标按钮必须有 aria-label」这个无障碍底线。
    await expect(page.getByRole('button', { name: '登录' })).toBeVisible()
  })
})

test.describe('交付入口', () => {
  test('演示账号按钮填入的凭据真能登录', async ({ page }) => {
    // 这条用例的价值：演示账号是硬编码在前端的字符串，与后端种子数据没有
    // 编译期关联。种子改了而前端没改时，交付出去的第一个入口就是坏的。
    await page.goto('/login')
    await page.getByRole('button', { name: /演示账号/ }).click()

    await expect(page.getByPlaceholder('例如 alice')).toHaveValue(DEMO.username)
    await page.getByRole('button', { name: '登录', exact: true }).last().click()

    // 登录成功的判据：头部出现昵称与退出入口。
    await expect(page.getByRole('button', { name: '退出登录' })).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(0)
  })
})

test.describe('房内聊天', () => {
  test('★ 发出的消息只渲染一条，且不会卡在「发送中」', async ({ page, request }) => {
    // 回归防线：曾经乐观气泡的去重键算错符号，服务端回执到达后旧气泡不被
    // 移除，于是同一条消息出现两次、其中一条永远停在「发送中」。
    // 后端契约完全正常，只有真实浏览器能看见这个缺陷。
    const owner = await register(request)
    const room = await createRoom(request, owner.token)
    await authenticate(page, owner.token)
    await page.goto(`/rooms/${room.id}`)

    const input = page.getByLabel('消息输入框')
    await expect(input).toBeVisible()

    const text = '端到端唯一性验证-' + Math.random().toString(36).slice(2, 8)
    await input.fill(text)
    await input.press('Enter')

    // 服务端确认后应恰好剩一条，且没有残留的发送中状态。
    await expect(page.getByText(text, { exact: true })).toHaveCount(1)
    await expect(page.getByText('发送中')).toHaveCount(0)

    // 刷新后仍然只有一条 —— 证明落库也没重复。
    await page.reload()
    await expect(page.getByText(text, { exact: true })).toHaveCount(1)
  })

  test('一键标签发送后作为消息出现在流里', async ({ page, request }) => {
    const owner = await register(request)
    const room = await createRoom(request, owner.token)
    await authenticate(page, owner.token)
    await page.goto(`/rooms/${room.id}`)

    // 标签快发是需求点名的一级交互，不能只是个装饰按钮。
    const tag = page.getByRole('button', { name: '硬核' })
    await expect(tag).toBeVisible()
    await tag.click()

    await expect(page.getByText('发送中')).toHaveCount(0, { timeout: 15_000 })
    await expect(page.getByLabel('消息输入框')).toBeVisible()
  })

  test('★ 两个浏览器会话之间消息实时互达', async ({ browser, request }) => {
    const owner = await register(request)
    const guest = await register(request)
    const room = await createRoom(request, owner.token)

    const joined = await request.post(`${API}/api/rooms/${room.id}/join`, {
      headers: { Authorization: `Bearer ${guest.token}` },
      data: { seat_gender: 'ANY' },
    })
    expect(joined.ok(), '上车应成功').toBeTruthy()

    const ctxA = await browser.newContext()
    const ctxB = await browser.newContext()
    try {
      const pageA = await ctxA.newPage()
      const pageB = await ctxB.newPage()
      await authenticate(pageA, owner.token)
      await authenticate(pageB, guest.token)
      await pageA.goto(`/rooms/${room.id}`)
      await pageB.goto(`/rooms/${room.id}`)

      await expect(pageA.getByLabel('消息输入框')).toBeVisible()
      await expect(pageB.getByLabel('消息输入框')).toBeVisible()

      const text = '实时互达-' + Math.random().toString(36).slice(2, 8)
      await pageA.getByLabel('消息输入框').fill(text)
      await pageA.getByLabel('消息输入框').press('Enter')

      // B 没有刷新页面，消息必须由 WebSocket 主动推到眼前。
      await expect(pageB.getByText(text, { exact: true })).toHaveCount(1, { timeout: 15_000 })
    } finally {
      await ctxA.close()
      await ctxB.close()
    }
  })
})

test.describe('席位实时性', () => {
  test('★ 别人上车后，我这边的席位数字自己会变', async ({ page, request }) => {
    const owner = await register(request)
    const room = await createRoom(request, owner.token, 6)
    await authenticate(page, owner.token)
    await page.goto(`/rooms/${room.id}`)

    // 车主自己占 1 席，6 席车此时是「6缺5」。
    await expect(page.getByText('6缺5').first()).toBeVisible({ timeout: 15_000 })

    const guest = await register(request)
    const res = await request.post(`${API}/api/rooms/${room.id}/join`, {
      headers: { Authorization: `Bearer ${guest.token}` },
      data: { seat_gender: 'ANY' },
    })
    expect(res.ok(), '上车应成功').toBeTruthy()

    // 关键：不刷新页面。席位广播必须把文案推成 6缺4。
    await expect(page.getByText('6缺4').first()).toBeVisible({ timeout: 15_000 })
  })
})

test.describe('移动端', () => {
  test('窄屏下分栏收成可切换的标签，且不横向溢出', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'mobile', '仅移动端项目执行')
    await page.goto('/')

    // 手机上四列并排是不可用的，必须退化成 tab 切换。
    const tab = page.getByRole('button', { name: /招募中/ }).first()
    await expect(tab).toBeVisible()
    await tab.click()
    await expect(page.getByText(/\d+缺\d+|\d+人满员|还没有/).first()).toBeVisible()

    // 横向溢出是最常见的移动端事故：卡片被挤出屏幕外，用户根本够不到。
    // 留 2px 容差给亚像素舍入。
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, `页面横向溢出 ${overflow}px`).toBeLessThanOrEqual(2)
  })
})
