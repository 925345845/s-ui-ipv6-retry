<template>
  <main class="pairing-workbench">
    <header>
      <p class="eyebrow">IPv6 RETRY · 独立配对工具</p>
      <h1>IPv4 / IPv6 配对工作台</h1>
      <p>粘贴 IPv4 上游，每条配一个可用 IPv6。失败自动更换，保留成功配对。</p>
    </header>
    <form class="pairing-form" @submit.prevent="create">
      <section class="pairing-panel">
        <label class="section-title" for="pairing-ipv4-list">1. IPv4 上游列表（必填）</label>
        <p id="pairing-ipv4-help">每行一条，支持 IP:端口:账号:密码、socks5:// 链接和供应商 JSON。IPv4 列表决定配对数量。</p>
        <textarea id="pairing-ipv4-list" v-model="form.upstream_text" name="ipv4_upstreams" rows="8" required
          spellcheck="false" autocomplete="off" dir="ltr" aria-describedby="pairing-ipv4-help"
          placeholder="203.0.113.10:1080:username:password&#10;socks5://username:password@203.0.113.11:1080" />
        <p class="count" role="status">已输入 {{ upstreamCount }} 条上游，最多 500 条</p>
      </section>
      <section class="pairing-panel">
        <h2>2. IPv6 和入口设置</h2>
        <div class="pairing-grid">
          <label>批次名称<input v-model="form.name" placeholder="可选，方便区分批次" /></label>
          <label>对外连接地址<input v-model="form.public_host" required /></label>
          <label>起始端口<input v-model.number="form.port_start" type="number" min="1" max="65535" required /></label>
          <label>网卡<input v-model="form.interface" list="pairing-interfaces" placeholder="例如 eth0" required /></label>
          <datalist id="pairing-interfaces"><option v-for="name in interfaces" :key="name" :value="name" /></datalist>
          <label>基准 IPv6<input v-model="form.base_ipv6" placeholder="服务器已路由的 IPv6 地址" required /></label>
          <label>前缀长度<input v-model.number="form.prefix" type="number" min="1" max="127" required /></label>
          <label>配对模式<select v-model="form.mode"><option value="paired">IPv4 / IPv6 固定配对</option><option value="dualstack">双栈出口（IPv6 失败回退 IPv4）</option></select></label>
          <label>入口账号前缀<input v-model="form.username_prefix" required /></label>
          <label>密码长度<input v-model.number="form.password_length" type="number" min="8" max="64" required /></label>
        </div>
        <label class="check"><input v-model="form.add_system_addresses" type="checkbox" />自动添加到系统网卡，失败时生成新 IPv6 补齐</label>
        <label class="check"><input v-model="form.apple_id_ipv4_only" type="checkbox" />Apple ID 专用 IPv4，其余仅 IPv6</label>
        <p class="muted">Apple ID 专用模式沿用原分流规则；关闭后按上面选择的配对/双栈模式运行。</p>
        <details><summary>指定初始 IPv6（可选）</summary><textarea v-model="ipv6Text" aria-label="指定初始 IPv6" rows="3" placeholder="每行一个；不填则自动生成。检测失败的地址会替换。" /></details>
        <p v-if="capabilityError" class="error" role="alert">{{ capabilityError }}</p>
        <p class="muted">补齐最长等待 8 分钟。前缀整体不可用时会报错并清理本次新地址，不保存未完成的新批次。</p>
        <button class="primary" type="submit" :disabled="busy || upstreamCount < 1 || upstreamCount > 500">{{ busy ? '正在检测并补齐 IPv6，请勿重复提交…' : `创建 ${upstreamCount} 条配对` }}</button>
      </section>
    </form>
    <p v-if="message" :class="messageError ? 'error' : 'success'" role="status">{{ message }}</p>
    <section class="pairing-panel">
      <div class="section-heading"><h2>3. 已创建的配对</h2><button type="button" :disabled="busy" @click="load">刷新列表</button></div>
      <p v-if="!pools.length" class="muted">暂无批次。创建成功后，连接链接和每条 IPv6 的刷新链接会显示在这里。</p>
      <article v-for="pool in pools" :key="pool.id" class="batch">
        <h3>{{ pool.name }} · {{ pool.items.length }} 条</h3>
        <textarea :value="pool.items.map(item => item.export).join('\n')" :aria-label="`${pool.name} 连接链接`" rows="4" readonly />
        <div class="batch-actions"><button type="button" @click="copy(pool)">复制全部连接</button><a :href="`api/relay/${pool.id}/bitbrowser.xlsx`">下载比特浏览器导入表</a><button type="button" :disabled="busy" @click="remove(pool)">删除此批次</button></div>
        <details><summary>查看 IPv4 / IPv6 对应关系和刷新链接</summary><div class="table-scroll"><table><thead><tr><th>IPv4 上游</th><th>IPv6 出口</th><th>入口端口</th><th>独立刷新链接</th></tr></thead><tbody><tr v-for="item in pool.items" :key="item.listen_port"><td>{{ item.upstream_server }}:{{ item.upstream_port }}</td><td>{{ item.ipv6 }}</td><td>{{ item.listen_port }}</td><td><input v-if="item.refresh_token" :value="refreshURL(item.refresh_token)" readonly aria-label="IPv6 刷新链接" /></td></tr></tbody></table></div></details>
      </article>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { copyText } from '@/utils/clipboard'

interface Item { export: string; upstream_server: string; upstream_port: number; ipv6: string; listen_port: number; refresh_token?: string }
interface Pool { id: number; name: string; items: Item[] }
const form = reactive({ name: '', upstream_text: '', mode: 'paired', public_host: window.location.hostname,
  port_start: 40000, interface: '', base_ipv6: '', prefix: 64, username_prefix: 'retry', password_length: 12,
  add_system_addresses: true, apple_id_ipv4_only: true })
const ipv6Text = ref(''), busy = ref(false), message = ref(''), messageError = ref(false), capabilityError = ref('')
const pools = ref<Pool[]>([]), interfaces = ref<string[]>([])
const countJSON = (value: any): number => {
  if (typeof value === 'string') return value.trim() ? 1 : 0
  if (Array.isArray(value)) return value.reduce((sum, item) => sum + countJSON(item), 0)
  if (!value || typeof value !== 'object') return 0
  const fields = Object.fromEntries(Object.entries(value).map(([key, child]) => [key.toLowerCase(), child]))
  if (['host', 'server', 'ip', 'address'].some(key => typeof fields[key] === 'string' && fields[key].trim())) return 1
  for (const key of ['data', 'proxies', 'proxy', 'list', 'result', 'items']) if (key in fields) return countJSON(fields[key])
  return 0
}
const upstreamCount = computed(() => {
  const text = form.upstream_text.trim().replace(/^\uFEFF/, '')
  if (text.startsWith('[') || text.startsWith('{')) { try { return countJSON(JSON.parse(text)) } catch { return 0 } }
  return text.split(/\r?\n/).filter(line => line.trim() && !line.trim().startsWith('#')).length
})
const api = async (url: string, body?: object) => {
  const response = await fetch(url, { method: body ? 'POST' : 'GET', credentials: 'include',
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' }, body: body ? JSON.stringify(body) : undefined })
  if (response.status === 401) { window.location.assign(new URL('login', document.baseURI).href); throw new Error('请先登录独立工具') }
  const result = await response.json()
  if (!response.ok || !result.success) throw new Error(result.msg || '请求失败，请检查服务日志')
  return result.obj
}
const load = async () => {
  try {
    const data = await api('api/relay')
    pools.value = (data.pools || []).map((pool: any) => ({ ...pool, items: typeof pool.items === 'string' ? JSON.parse(pool.items) : pool.items || [] }))
    interfaces.value = [...new Set<string>((data.ipv6 || []).map((ip: any) => ip.interface))]
    if (!form.interface && data.ipv6?.[0]) { form.interface = data.ipv6[0].interface; form.base_ipv6 = data.ipv6[0].address; form.prefix = data.ipv6[0].prefix }
    capabilityError.value = data.capabilities?.can_add_system_ipv6 === false ? '当前服务无法添加 IPv6，请确认在 Linux 上以 root 运行，并安装 iproute2。' : ''
  } catch (error: any) { message.value = error.message; messageError.value = true }
}
const create = async () => {
  if (busy.value || upstreamCount.value < 1 || upstreamCount.value > 500) return
  busy.value = true; message.value = ''; messageError.value = false
  try {
    const result = await api('api/relay/create', { ...form, count: upstreamCount.value, protocol: 'socks', core_type: 'sing-box',
      domain_strategy: 'prefer_ipv6', tls_id: 0, transport: '', upstreams: [], ipv6_addresses: ipv6Text.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean) })
    message.value = `已创建 ${result.count} 条配对，端口 ${result.port_start}–${result.port_start + result.count - 1}。`
    form.port_start = Math.min(65535, result.port_start + result.count)
    await load()
  } catch (error: any) { message.value = error.message; messageError.value = true }
  finally { busy.value = false }
}
const refreshURL = (token: string) => new URL(`refresh/${encodeURIComponent(token)}`, document.baseURI).href
const copy = async (pool: Pool) => { try { await copyText(pool.items.map(item => item.export).join('\n')); message.value = '已复制连接链接'; messageError.value = false } catch { message.value = '复制失败，请从文本框手动复制'; messageError.value = true } }
const remove = async (pool: Pool) => {
  if (!window.confirm(`确定删除“${pool.name}”及其节点吗？`)) return
  busy.value = true
  try { await api(`api/relay/${pool.id}/delete`, {}); await load() } catch (error: any) { message.value = error.message; messageError.value = true } finally { busy.value = false }
}
onMounted(load)
</script>

<style scoped>
.pairing-workbench { max-width: 1120px; margin: 0 auto; color: rgb(var(--v-theme-on-surface)); }
header { margin-bottom: 24px; } h1 { font-size: 28px; margin: 6px 0 10px; } h2,.section-title { font-size: 19px; font-weight: 650; } h3 { font-size: 17px; }
.eyebrow { font-size: 12px; letter-spacing: 2px; color: rgb(var(--v-theme-primary)); font-weight: 700; }
.pairing-panel { padding: 24px; margin-bottom: 22px; border: 1px solid rgba(var(--v-theme-on-surface),.15); border-radius: 12px; background: rgb(var(--v-theme-surface)); }
.section-title { display: block; } .pairing-panel p { margin: 10px 0; line-height: 1.6; }
.pairing-workbench textarea,.pairing-workbench input:not([type=checkbox]),.pairing-workbench select { display: block; width: 100%; box-sizing: border-box; border: 1px solid rgba(var(--v-theme-on-surface),.35); border-radius: 8px; padding: 12px; background: rgb(var(--v-theme-surface)); color: inherit; font: inherit; }
.pairing-workbench textarea { resize: vertical; min-height: 110px; font-family: monospace; } #pairing-ipv4-list { min-height: 190px; margin-top: 14px; }
.pairing-workbench :is(input,textarea,select):focus { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.pairing-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 18px; margin: 20px 0; } .pairing-grid label { display: grid; gap: 7px; font-size: 14px; }
.check { display: flex; gap: 10px; align-items: center; margin: 18px 0; } .check input { width: 18px; height: 18px; accent-color: rgb(var(--v-theme-primary)); }
.pairing-workbench button { border: 1px solid rgba(var(--v-theme-on-surface),.3); border-radius: 8px; padding: 10px 18px; cursor: pointer; } .pairing-workbench button:disabled { opacity: .5; cursor: not-allowed; }
.pairing-workbench .primary { background: rgb(var(--v-theme-primary)); color: rgb(var(--v-theme-on-primary)); border: none; margin-top: 12px; min-height: 46px; }
.muted,#pairing-ipv4-help { opacity: .72; font-size: 14px; } .count { font-size: 14px; font-weight: 600; } .error { color: #c62828; white-space: pre-wrap; } .success { color: #16824a; }
.section-heading,.batch-actions { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; } .batch { border-top: 1px solid rgba(var(--v-theme-on-surface),.15); margin-top: 20px; padding-top: 20px; } .batch h3 { margin-bottom: 12px; } .batch-actions { justify-content: flex-start; margin: 12px 0; }
details { margin-top: 14px; } summary { cursor: pointer; margin-bottom: 12px; } .table-scroll { overflow-x: auto; } table { width: 100%; border-collapse: collapse; font-size: 13px; } th,td { text-align: left; padding: 10px; border-bottom: 1px solid rgba(var(--v-theme-on-surface),.12); } td { overflow-wrap: anywhere; }
@media(max-width:700px) { .pairing-grid { grid-template-columns: 1fr; } .pairing-panel { padding: 16px; } h1 { font-size: 23px; } }
</style>
