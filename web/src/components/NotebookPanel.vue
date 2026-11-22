<script setup lang="ts">
import { onActivated, onMounted, ref } from 'vue'
type Notebook = { id: string; title: string; source_count: number }
type SourceAccount = { id: string; label: string; state: string }
type Quota = { window: string; used_percent: number; remaining_percent: number; resets_at?: string }
type Account = {
  id: string
  label: string
  source_account_id?: string
  email?: string
  state: string
  plan?: string
  error?: string
  cooldown_until?: string
  notebooks: Notebook[]
  request_count: number
  success_count: number
  usage?: { metering_enabled: boolean; windows: Quota[] }
}
const props = defineProps<{ mode: 'accounts' | 'usage' }>()
const accounts = ref<Account[]>([])
const sources = ref<SourceAccount[]>([])
const sourceAccountID = ref('')
const sourceSelectionInitialized = ref(false)
const loading = ref(false)
const pending = ref(false)
const label = ref('')
const error = ref('')
const notice = ref('')
const copiedModelID = ref('')
const concurrency = ref(4)
const selectedConcurrency = ref(4)
const concurrencySaving = ref(false)
const concurrencyOptions = Array.from({ length: 16 }, (_, i) => i + 1)
async function saveConcurrency() {
  concurrencySaving.value = true
  error.value = ''
  notice.value = ''
  try {
    const response = await call<{ concurrency: number }>('/api/notebook/settings', 'PUT', {
      concurrency: selectedConcurrency.value,
    })
    concurrency.value = response.concurrency
    selectedConcurrency.value = response.concurrency
    notice.value = `Notebook 并发已设为 ${response.concurrency}，对每个 Playground 来源分别生效；进行中的请求不受影响。`
  } catch (e) {
    selectedConcurrency.value = concurrency.value
    error.value = String(e)
  } finally {
    concurrencySaving.value = false
  }
}
// This is the exact model namespace accepted by the OpenAI-compatible
// /v1/chat/completions endpoint; account and Notebook IDs must both be used.
const requestModelID = (account: Account, notebook: Notebook) =>
  `notebook:${account.id}:${notebook.id}`
async function copyRequestModelID(account: Account, notebook: Notebook) {
  const modelID = requestModelID(account, notebook)
  try {
    if (!navigator.clipboard?.writeText) throw new Error('Clipboard API unavailable')
    await navigator.clipboard.writeText(modelID)
    copiedModelID.value = modelID
    return
  } catch {
    // Wails WebView or an HTTP origin may not expose async clipboard.
    // Provide a synchronous clipboard fallback on this user-initiated click.
    const textarea = document.createElement('textarea')
    textarea.value = modelID
    textarea.readOnly = true
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    let success = false
    try {
      success = document.execCommand('copy')
    } catch {
      success = false
    } finally {
      textarea.remove()
    }
    if (success) {
      copiedModelID.value = modelID
    } else {
      error.value = '自动复制失败，请手动选中请求模型 ID 复制。'
    }
  }
}
// Keep legacy duplicates visible; removing them silently can break
// external notebook:<account-id>:<notebook-id> model references.
const boundCount = (sourceID: string) =>
  accounts.value.filter((a) => a.source_account_id === sourceID).length
const duplicateBindingCount = (a: Account) =>
  a.source_account_id ? Math.max(0, boundCount(a.source_account_id) - 1) : 0
const otherPlaygroundSameEmail = (a: Account) =>
  a.email
    ? accounts.value.filter(
        (other) =>
          other.id !== a.id &&
          other.email?.toLowerCase() === a.email?.toLowerCase() &&
          other.source_account_id !== a.source_account_id,
      ).length
    : 0
async function call<T>(url: string, method = 'GET', body?: unknown): Promise<T> {
  const r = await fetch(url, {
    method,
    credentials: 'same-origin',
    ...(body === undefined
      ? {}
      : { headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) }),
  })
  const v = (await r.json()) as T & { error?: { message?: string } }
  if (!r.ok) throw new Error(v.error?.message || `HTTP ${r.status}`)
  return v
}
async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const [foundAccounts, foundSources, settings] = await Promise.all([
      call<{ accounts: Account[] }>('/api/notebook/accounts'),
      call<{ sources: SourceAccount[] }>('/api/notebook/sources'),
      call<{ concurrency: number }>('/api/notebook/settings'),
    ])
    accounts.value = foundAccounts.accounts
    sources.value = foundSources.sources
    concurrency.value = settings.concurrency
    selectedConcurrency.value = settings.concurrency
    if (!sourceSelectionInitialized.value) {
      sourceAccountID.value = sources.value[0]?.id || ''
      sourceSelectionInitialized.value = true
    }
  } catch (e) {
    error.value = String(e)
  } finally {
    loading.value = false
  }
}
async function add() {
  pending.value = true
  error.value = ''
  notice.value = ''
  try {
    const chosenSource = sourceAccountID.value
    const x = await call<{ account: Account; created: boolean }>('/api/notebook/accounts', 'POST', {
      label: label.value.trim(),
      source_account_id: chosenSource,
    })
    label.value = ''
    // The server returns the original Notebook account ID for an existing
    // Playground binding, preserving model IDs, counters and quota state.
    await call(`/api/notebook/accounts/${encodeURIComponent(x.account.id)}/login`, 'POST')
    await refresh()
    notice.value = !x.created
      ? '已找到现有 Notebook 绑定，使用原账号重新认证；未创建新的账号记录。'
      : chosenSource
        ? '已使用关联 Playground 账号读取 Notebook，无需额外 Chrome。'
        : '已打开 Chrome，完成 Google 登录后刷新账号状态。'
  } catch (e) {
    const message = String(e)
    // The account record exists before login. Render it even if login fails,
    // so the next attempt reuses that ID instead of adding another.
    await refresh()
    error.value = message
  } finally {
    pending.value = false
  }
}
async function action(a: Account, task: 'login' | 'refresh' | 'delete') {
  if (task === 'delete' && !confirm(`删除 Notebook 账号 ${a.label} 及对应登录资料？`)) return
  pending.value = true
  error.value = ''
  notice.value = ''
  try {
    const url = `/api/notebook/accounts/${encodeURIComponent(a.id)}`
    await call(url + (task === 'delete' ? '' : `/${task}`), task === 'delete' ? 'DELETE' : 'POST')
    if (task === 'login')
      notice.value = a.source_account_id
        ? '正在复用 Playground 会话刷新 Notebook 认证，不会启动 Chrome。'
        : 'Chrome 登录窗口已打开，完成登录后点击刷新状态。'
    await refresh()
  } catch (e) {
    error.value = String(e)
  } finally {
    pending.value = false
  }
}
const percent = (v: number) => `${Math.max(0, Math.min(100, v)).toFixed(1)}%`
const when = (v?: string) => (v ? new Date(v).toLocaleString() : '未知')
onMounted(refresh)
onActivated(refresh)
</script>
<template>
  <section class="page-x border-t border-line py-4" aria-label="Gemini Notebook">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="text-base font-semibold text-white">Gemini Notebook</h3>
        <p class="text-xs text-gray-400">
          {{
            props.mode === 'accounts'
              ? '复用 Playground 账号 · Camoufox / SpiderMonkey · Notebook 独立冷却'
              : 'Notebook 专用额度（不计入 AI Studio Token 数据）'
          }}
        </p>
      </div>
      <button
        type="button"
        class="rounded border border-line px-3 py-2 text-sm text-gray-300"
        :disabled="loading || pending"
        @click="refresh"
      >
        刷新列表
      </button>
    </div>
    <div
      v-if="props.mode === 'accounts'"
      class="mb-3 flex flex-wrap items-center gap-3 rounded border border-line bg-panel px-3 py-3"
    >
      <label for="notebook-concurrency" class="text-sm font-medium text-white">
        Notebook 单来源最大并发
      </label>
      <select
        id="notebook-concurrency"
        v-model.number="selectedConcurrency"
        class="rounded border border-line bg-panel px-3 py-2 text-sm text-white"
        :disabled="concurrencySaving || loading || pending"
      >
        <option v-for="n in concurrencyOptions" :key="n" :value="n">{{ n }}</option>
      </select>
      <button
        type="button"
        class="rounded bg-blue-600 px-3 py-2 text-sm text-white disabled:opacity-50"
        :disabled="concurrencySaving || loading || pending || selectedConcurrency === concurrency"
        @click="saveConcurrency"
      >
        {{ concurrencySaving ? '保存中…' : '保存并发设置' }}
      </button>
      <span class="text-xs text-gray-400">
        默认 4 · 范围 1～16 · 最多排队 10 条，等待 30 秒。 Camoufox
        标签页容量随设置调整；SpiderMonkey 复用现有 HTTP 会话。
      </span>
    </div>
    <div v-if="props.mode === 'accounts'" class="mb-3 flex flex-wrap gap-2">
      <input
        v-model="label"
        class="min-w-0 flex-1 rounded border border-line bg-panel px-3 py-2 text-sm text-white"
        maxlength="120"
        placeholder="账号备注"
      />
      <select
        v-model="sourceAccountID"
        class="min-w-48 rounded border border-line bg-panel px-3 py-2 text-sm text-white"
        aria-label="Notebook 浏览器模式"
        title="绑定现有 Playground Google 账号，免启动额外 Chrome"
      >
        <option value="">独立 Chrome（兼容模式）</option>
        <option v-for="source in sources" :key="source.id" :value="source.id">
          复用 {{ source.label }}（{{ source.state }}）
          {{ boundCount(source.id) ? '· 已绑定' : '' }}
        </option>
      </select>
      <button
        type="button"
        class="rounded bg-blue-600 px-3 py-2 text-sm text-white"
        :disabled="pending"
        @click="add"
      >
        {{ sourceAccountID && boundCount(sourceAccountID) ? '重新登录已有绑定' : '添加并登录' }}
      </button>
    </div>
    <p v-if="error" role="alert" class="mb-2 text-sm text-red-400">{{ error }}</p>
    <p v-if="notice" class="mb-2 text-sm text-blue-300">{{ notice }}</p>
    <p v-if="accounts.length === 0" class="text-sm text-gray-400">暂无 Notebook 账号</p>
    <div class="grid gap-3 xl:grid-cols-2">
      <article v-for="a in accounts" :key="a.id" class="rounded border border-line bg-panel p-4">
        <div class="flex justify-between gap-2">
          <div>
            <div class="text-sm font-medium text-white">{{ a.label }} · {{ a.plan }}</div>
            <div class="break-all text-xs text-gray-400">{{ a.email || a.id }}</div>
            <div v-if="a.source_account_id" class="text-xs text-blue-400">
              共享 Playground 账号（Camoufox 标签页保活 120 秒 / SpiderMonkey 原生 HTTP）
            </div>
          </div>
          <span class="text-xs text-gray-400">{{ a.state }}</span>
        </div>
        <p v-if="a.error" class="mt-2 text-xs text-red-400">{{ a.error }}</p>
        <p v-if="duplicateBindingCount(a)" class="mt-2 text-xs text-yellow-300">
          检测到同一 Playground 来源的 {{ duplicateBindingCount(a) }} 条旧版重复记录。
          不会自动删除；请确认正在使用的 Notebook 模型 ID 后手动清理。
        </p>
        <p v-else-if="otherPlaygroundSameEmail(a)" class="mt-2 text-xs text-yellow-300">
          另有 {{ otherPlaygroundSameEmail(a) }} 条记录使用相同 Google 邮箱，但关联不同 Playground
          凭证。 其 Google 额度并不独立。
        </p>
        <p
          v-if="a.cooldown_until && new Date(a.cooldown_until) > new Date()"
          class="mt-2 text-xs text-yellow-300"
        >
          冷却至 {{ when(a.cooldown_until) }}
        </p>
        <div class="mt-2 flex flex-wrap gap-3 text-xs text-gray-400">
          <span>Notebook {{ a.notebooks?.length || 0 }} 个</span
          ><span>请求 {{ a.request_count }}</span
          ><span>成功 {{ a.success_count }}</span>
        </div>
        <div v-for="w in a.usage?.windows || []" :key="w.window" class="mt-3">
          <div class="flex justify-between text-xs text-gray-300">
            <span>{{ w.window }} · 已用 {{ percent(w.used_percent) }}</span
            ><span>剩余 {{ percent(w.remaining_percent) }}</span>
          </div>
          <div class="mt-1 h-1.5 rounded bg-raised">
            <div
              class="h-full rounded bg-blue-500"
              :style="{ width: percent(w.used_percent) }"
            ></div>
          </div>
          <div class="mt-1 text-xs text-gray-500">重置 {{ when(w.resets_at) }}</div>
        </div>
        <template v-if="props.mode === 'accounts'">
          <div class="mt-3 text-xs text-gray-400">
            <div class="mb-2">请求模型 ID（用于 OpenAI Chat Completions 的 model 参数）</div>
            <div v-if="!a.notebooks?.length" class="text-gray-500">
              暂无可用 Notebook 模型，请先刷新账号状态。
            </div>
            <div v-else class="max-h-56 space-y-2 overflow-y-auto">
              <div v-for="nb in a.notebooks" :key="nb.id" class="rounded border border-line p-2">
                <div class="mb-1 text-gray-300">{{ nb.title }} · {{ nb.source_count }} 个来源</div>
                <div class="flex items-center gap-2">
                  <code
                    class="min-w-0 flex-1 select-all break-all font-mono text-xs text-blue-300"
                    :title="requestModelID(a, nb)"
                    >{{ requestModelID(a, nb) }}</code
                  >
                  <button
                    type="button"
                    class="shrink-0 rounded border border-line px-2 py-1 text-xs text-gray-300"
                    :aria-label="`复制 ${nb.title} 的请求模型 ID`"
                    @click="copyRequestModelID(a, nb)"
                  >
                    {{ copiedModelID === requestModelID(a, nb) ? '已复制' : '复制 ID' }}
                  </button>
                </div>
              </div>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap gap-2">
            <button
              class="rounded border border-line px-2 py-1 text-xs text-gray-300"
              :disabled="pending"
              @click="action(a, 'login')"
            >
              重新登录
            </button>
            <button
              class="rounded border border-line px-2 py-1 text-xs text-gray-300"
              :disabled="pending"
              @click="action(a, 'refresh')"
            >
              刷新状态
            </button>
            <button
              class="rounded border border-red-500/40 px-2 py-1 text-xs text-red-300"
              :disabled="pending"
              @click="action(a, 'delete')"
            >
              删除
            </button>
          </div>
        </template>
      </article>
    </div>
  </section>
</template>
