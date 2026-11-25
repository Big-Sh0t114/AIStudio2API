<script setup lang="ts">
import { computed, onActivated, onUnmounted, ref, watch } from 'vue'
import { api, type AlternateModel, type AlternateStatus, type PlaygroundSource } from '@/api'
import { useI18n } from '@/i18n'
import type { ServiceConfig } from '@/types'
import UiIcon from './UiIcon.vue'

const props = defineProps<{ config: ServiceConfig | null; loading: boolean; error: string }>()
const emit = defineEmits<{
  saved: [config: ServiceConfig]
  notice: [message: string, tone: 'success' | 'error']
  settings: []
}>()
const { t, errorText } = useI18n()

const oauthFile = ref('runtime/antigravity/oauth.json')
const clientID = ref('')
const clientSecret = ref('')
const projectID = ref('')
const saving = ref(false)
const startingOAuth = ref(false)
const refreshing = ref(false)
const status = ref<AlternateStatus | null>(null)
const models = ref<AlternateModel[]>([])
const sources = ref<PlaygroundSource[]>([])
const panelError = ref('')
const notice = ref('')
const authorizationURL = ref('')
const copied = ref('')
let oauthPoll: number | undefined

watch(
  () => props.config,
  (config) => {
    if (!config) return
    oauthFile.value = config.antigravity_oauth_file || 'runtime/antigravity/oauth.json'
    clientID.value = config.antigravity_client_id || ''
    projectID.value = config.antigravity_project_id || ''
  },
  { immediate: true },
)

const environment = computed(() => new Set(props.config?.environment ?? []))
const locked = (key: string) => environment.value.has(key)
const antigravityModels = computed(() => models.value.filter((model) => model.id.startsWith('antigravity:')))
const geminiModels = computed(() => models.value.filter((model) => model.id.startsWith('gemini-web:')))
const sourceModels = (source: PlaygroundSource) =>
  geminiModels.value.filter((model) => model.id.startsWith(`gemini-web:${source.id}:`))
const antigravityReady = computed(() => status.value?.antigravity.authenticated ?? false)
const canStartOAuth = computed(
  () =>
    Boolean(props.config?.admin_auth_enabled) &&
    Boolean(oauthFile.value.trim()) &&
    Boolean(clientID.value.trim()) &&
    Boolean(clientSecret.value.trim() || props.config?.antigravity_client_secret_set) &&
    !startingOAuth.value &&
    !props.config?.management_restart_required,
)

async function refreshStatus(): Promise<void> {
  status.value = await api.alternateStatus()
}
async function refreshAll(): Promise<void> {
  refreshing.value = true
  panelError.value = ''
  try {
    const results = await Promise.allSettled([
      api.alternateStatus(),
      api.alternateModels(),
      api.notebookSources(),
    ])
    const failures: string[] = []
    if (results[0]?.status === 'fulfilled') status.value = results[0].value
    else failures.push(errorText(results[0]?.reason))
    if (results[1]?.status === 'fulfilled') models.value = results[1].value
    else failures.push(errorText(results[1]?.reason))
    if (results[2]?.status === 'fulfilled') sources.value = results[2].value
    else failures.push(errorText(results[2]?.reason))
    if (failures.length > 0) panelError.value = failures.join(' · ')
  } catch (e) {
    panelError.value = errorText(e)
  } finally {
    refreshing.value = false
  }
}

async function saveSettings(): Promise<void> {
  if (!props.config) return
  saving.value = true
  panelError.value = ''
  notice.value = ''
  try {
    const saved = await api.saveConfig({
      ...props.config,
      antigravity_oauth_file: oauthFile.value.trim(),
      antigravity_client_id: clientID.value.trim(),
      antigravity_project_id: projectID.value.trim(),
      ...(clientSecret.value.trim() ? { antigravity_client_secret: clientSecret.value.trim() } : {}),
    })
    clientSecret.value = ''
    emit('saved', saved)
    notice.value = saved.management_restart_required
      ? t('providers.savedRestart')
      : t('providers.saved')
    await refreshStatus()
  } catch (e) {
    panelError.value = errorText(e)
  } finally {
    saving.value = false
  }
}

function stopOAuthPolling(): void {
  if (oauthPoll !== undefined) {
    window.clearInterval(oauthPoll)
    oauthPoll = undefined
  }
}
function startOAuthPolling(): void {
  stopOAuthPolling()
  oauthPoll = window.setInterval(async () => {
    try {
      await refreshStatus()
      if (antigravityReady.value) {
        stopOAuthPolling()
        authorizationURL.value = ''
        notice.value = t('providers.connected')
        await refreshAll()
      }
    } catch {
      // Keep polling while the browser completes the local OAuth callback.
    }
  }, 2500)
}
async function startOAuth(): Promise<void> {
  panelError.value = ''
  notice.value = ''
  if (!props.config?.admin_auth_enabled) {
    panelError.value = t('providers.adminAuthRequired')
    return
  }
  const popup = window.open('about:blank', '_blank')
  startingOAuth.value = true
  try {
    const result = await api.startAntigravityOAuth()
    authorizationURL.value = result.authorization_url
    if (popup) popup.location.href = result.authorization_url
    startOAuthPolling()
    notice.value = t('providers.oauthHint')
  } catch (e) {
    popup?.close()
    panelError.value = errorText(e)
  } finally {
    startingOAuth.value = false
  }
}

async function copyModel(id: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(id)
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = id
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const ok = document.execCommand('copy')
    textarea.remove()
    if (!ok) {
      panelError.value = t('providers.copyFailed')
      return
    }
  }
  copied.value = id
  window.setTimeout(() => {
    if (copied.value === id) copied.value = ''
  }, 1800)
}

onActivated(refreshAll)
onUnmounted(stopOAuthPolling)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col">
    <header class="page-x border-b border-line py-4">
      <h2 class="text-xl font-semibold text-white">{{ t('providers.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('providers.description') }}</p>
    </header>

    <div class="min-h-0 flex-1 overflow-auto page-x py-5">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="text-xs text-gray-500">
          {{ t('providers.models') }}：{{ models.length }}
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-2 rounded border border-line px-3 py-2 text-sm text-gray-300 hover:bg-raised disabled:opacity-50"
          :disabled="refreshing"
          @click="refreshAll"
        >
          <UiIcon name="refresh" :size="14" :class="refreshing ? 'animate-spin' : ''" />
          {{ refreshing ? t('common.loading') : t('providers.refresh') }}
        </button>
      </div>

      <p v-if="props.error" role="alert" class="mb-3 rounded border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
        {{ props.error }}
      </p>
      <p v-if="panelError" role="alert" class="mb-3 rounded border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
        {{ panelError }}
      </p>
      <p v-if="notice" role="status" class="mb-3 rounded border border-green-500/30 bg-green-500/10 p-3 text-sm text-green-300">
        {{ notice }}
      </p>

      <div class="grid gap-4 2xl:grid-cols-2">
        <article class="min-w-0 rounded-lg border border-line bg-panel p-4">
          <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-white">{{ t('providers.antigravityTitle') }}</h3>
              <p class="mt-1 text-xs text-gray-500">{{ t('providers.antigravityDescription') }}</p>
            </div>
            <span
              class="rounded-full border px-2.5 py-1 text-xs"
              :class="antigravityReady ? 'border-green-500/40 text-green-300' : 'border-amber-500/40 text-amber-300'"
            >
              {{ antigravityReady ? t('providers.connected') : status?.antigravity.oauth_in_progress ? t('providers.oauthInProgress') : !status?.antigravity.oauth_file_configured || !status?.antigravity.client_id_configured ? t('providers.notConfigured') : t('providers.notConnected') }}
            </span>
          </div>

          <div class="grid gap-3">
            <label class="block text-sm text-gray-300">
              <span class="mb-1 block">{{ t('providers.oauthFile') }}</span>
              <input
                v-model.trim="oauthFile"
                class="w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white disabled:opacity-50"
                :disabled="locked('ANTIGRAVITY_OAUTH_FILE')"
                autocomplete="off"
                spellcheck="false"
                placeholder="runtime/antigravity/oauth.json"
              />
            </label>
            <label class="block text-sm text-gray-300">
              <span class="mb-1 block">{{ t('providers.clientID') }}</span>
              <input
                v-model.trim="clientID"
                class="w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white disabled:opacity-50"
                :disabled="locked('ANTIGRAVITY_CLIENT_ID')"
                autocomplete="off"
                spellcheck="false"
              />
            </label>
            <label class="block text-sm text-gray-300">
              <span class="mb-1 block">{{ t('providers.clientSecret') }}</span>
              <input
                v-model="clientSecret"
                type="password"
                class="w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white disabled:opacity-50"
                :disabled="locked('ANTIGRAVITY_CLIENT_SECRET')"
                autocomplete="new-password"
                :placeholder="props.config?.antigravity_client_secret_set ? t('providers.secretKeep') : ''"
              />
            </label>
            <label class="block text-sm text-gray-300">
              <span class="mb-1 block">{{ t('providers.projectID') }}</span>
              <input
                v-model.trim="projectID"
                class="w-full rounded-md border border-line bg-canvas px-3 py-2 text-sm text-white disabled:opacity-50"
                :disabled="locked('ANTIGRAVITY_PROJECT_ID')"
                autocomplete="off"
                spellcheck="false"
              />
            </label>
          </div>

          <p v-if="['ANTIGRAVITY_OAUTH_FILE', 'ANTIGRAVITY_CLIENT_ID', 'ANTIGRAVITY_CLIENT_SECRET', 'ANTIGRAVITY_PROJECT_ID'].some(locked)" class="mt-2 text-xs text-gray-500">{{ t('providers.envLocked') }}</p>
          <p v-if="props.config?.management_restart_required" class="mt-2 text-xs text-amber-300">{{ t('providers.restartPending') }}</p>
          <p v-if="status && !status.antigravity.credential_file_exists && status.antigravity.oauth_file_configured" class="mt-2 text-xs text-amber-300">
            {{ t('providers.credentialsMissing') }}
          </p>

          <div class="mt-4 flex flex-wrap gap-2">
            <button
              type="button"
              class="rounded bg-blue-600 px-3 py-2 text-sm text-white disabled:opacity-50"
              :disabled="saving || props.config === null"
              @click="saveSettings"
            >
              {{ saving ? t('common.loading') : t('providers.save') }}
            </button>
            <button
              type="button"
              class="rounded border border-line px-3 py-2 text-sm text-gray-200 disabled:opacity-50"
              :disabled="!canStartOAuth"
              @click="startOAuth"
            >
              {{ startingOAuth ? t('common.loading') : t('providers.connect') }}
            </button>
            <button
              v-if="!props.config?.admin_auth_enabled"
              type="button"
              class="rounded border border-amber-500/40 px-3 py-2 text-sm text-amber-200"
              @click="emit('settings')"
            >
              {{ t('providers.openSecuritySettings') }}
            </button>
          </div>
          <a
            v-if="authorizationURL"
            :href="authorizationURL"
            target="_blank"
            rel="noopener noreferrer"
            class="mt-3 block break-all text-xs text-blue-300 underline"
          >
            {{ t('providers.openAuthorization') }}
          </a>

          <div class="mt-5 border-t border-line pt-4">
            <div class="mb-2 flex items-center justify-between">
              <h4 class="text-sm font-medium text-white">{{ t('providers.antigravityModels') }}</h4>
              <span class="text-xs text-gray-500">{{ antigravityModels.length }}</span>
            </div>
            <p v-if="antigravityModels.length === 0" class="text-xs text-gray-500">{{ t('providers.noModels') }}</p>
            <div v-else class="divide-y divide-line">
              <div v-for="model in antigravityModels" :key="model.id" class="flex min-w-0 items-center gap-2 py-2">
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm text-gray-200">{{ model.name }}</div>
                  <code class="block break-all text-xs text-gray-500">{{ model.id }}</code>
                </div>
                <button
                  type="button"
                  class="shrink-0 rounded border border-line p-2 text-gray-400 hover:text-white"
                  :title="t('providers.copy')"
                  @click="copyModel(model.id)"
                >
                  <UiIcon :name="copied === model.id ? 'success' : 'copy'" :size="14" />
                </button>
              </div>
            </div>
          </div>
        </article>

        <article class="min-w-0 rounded-lg border border-line bg-panel p-4">
          <div class="mb-4">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h3 class="text-base font-semibold text-white">{{ t('providers.geminiWebTitle') }}</h3>
              <span class="rounded-full border border-line px-2.5 py-1 text-xs text-gray-300">
                {{ sources.length }} {{ t('providers.sources') }}
              </span>
            </div>
            <p class="mt-1 text-xs text-gray-500">{{ t('providers.geminiWebDescription') }}</p>
          </div>

          <p v-if="sources.length === 0" class="rounded border border-dashed border-line p-4 text-sm text-gray-500">
            {{ t('providers.noSources') }}
          </p>
          <div v-else class="space-y-4">
            <section v-for="source in sources" :key="source.id" class="rounded border border-line bg-canvas p-3">
              <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
                <div class="min-w-0">
                  <div class="truncate text-sm font-medium text-gray-200">{{ source.label }}</div>
                  <code class="block text-xs text-gray-500">{{ source.id }}</code>
                </div>
                <span class="text-xs text-gray-400">{{ source.state }}</span>
              </div>
              <div class="mb-2 text-xs text-gray-500">{{ t('providers.models') }}：{{ sourceModels(source).length }}</div>
              <p v-if="sourceModels(source).length === 0" class="text-xs text-gray-500">{{ t('providers.noModels') }}</p>
              <div v-else class="divide-y divide-line">
                <div v-for="model in sourceModels(source)" :key="model.id" class="flex min-w-0 items-center gap-2 py-2">
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-sm text-gray-200">{{ model.name }}</div>
                    <code class="block break-all text-xs text-gray-500">{{ model.id }}</code>
                  </div>
                  <button
                    type="button"
                    class="shrink-0 rounded border border-line p-2 text-gray-400 hover:text-white"
                    :title="t('providers.copy')"
                    @click="copyModel(model.id)"
                  >
                    <UiIcon :name="copied === model.id ? 'success' : 'copy'" :size="14" />
                  </button>
                </div>
              </div>
            </section>
          </div>
        </article>
      </div>
    </div>
  </section>
</template>
