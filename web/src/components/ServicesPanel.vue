<script setup lang="ts">
import { computed, type Component } from 'vue'
import { useQueryParam } from '@/url'
import { useI18n, type TranslationKey } from '@/i18n'
import type {
  Account,
  AccountTask,
  Cooldown,
  Model,
  PreparationStatus,
  ServiceConfig,
} from '@/types'
import type { IconName } from '@/icons'
import AccountsPanel from './AccountsPanel.vue'
import NotebookPanel from './NotebookPanel.vue'
import ProvidersPanel from './ProvidersPanel.vue'
import UiIcon from './UiIcon.vue'

const props = defineProps<{
  config: ServiceConfig | null
  configLoading: boolean
  configError: string
  accounts: Account[]
  tasks: AccountTask[]
  accountsLoading: boolean
  accountsError: string
  chromeImport: boolean
  camoufox: PreparationStatus | undefined
  models: Model[]
  cooldowns: Cooldown[]
}>()

const emit = defineEmits<{
  refresh: []
  updated: [account: Account]
  notice: [message: string, tone: 'success' | 'error']
  delete: [accounts: Account[]]
  saved: [config: ServiceConfig]
  settings: []
}>()

const { t } = useI18n()

type ServiceTab = 'notebook' | 'gemini-chat' | 'antigravity' | 'playground'

const serviceTabs: { id: ServiceTab; label: TranslationKey; icon: IconName }[] = [
  { id: 'notebook', label: 'services.notebook', icon: 'models' },
  { id: 'gemini-chat', label: 'services.geminiChat', icon: 'network' },
  { id: 'antigravity', label: 'services.antigravity', icon: 'access' },
  { id: 'playground', label: 'services.playground', icon: 'accounts' },
]
const serviceIDs: ServiceTab[] = serviceTabs.map((item) => item.id)
const activeService = useQueryParam<ServiceTab>('service', serviceIDs, 'notebook')

const components: Record<ServiceTab, Component> = {
  notebook: NotebookPanel,
  'gemini-chat': ProvidersPanel,
  antigravity: ProvidersPanel,
  playground: AccountsPanel,
}

const activeComponent = computed(() => components[activeService.value])
const activeProps = computed<Record<string, unknown>>(() => {
  switch (activeService.value) {
    case 'notebook':
      return { mode: 'accounts' }
    case 'gemini-chat':
    case 'antigravity':
      return {
        service: activeService.value,
        config: props.config,
        loading: props.configLoading,
        error: props.configError,
      }
    case 'playground':
      return {
        accounts: props.accounts,
        tasks: props.tasks,
        loading: props.accountsLoading,
        error: props.accountsError,
        chromeImport: props.chromeImport,
        camoufox: props.camoufox,
        models: props.models,
        cooldowns: props.cooldowns,
      }
  }
})

function handleRefresh(): void {
  emit('refresh')
}
function handleUpdated(account: Account): void {
  emit('updated', account)
}
function handleNotice(message: string, tone: 'success' | 'error'): void {
  emit('notice', message, tone)
}
function handleDelete(accounts: Account[]): void {
  emit('delete', accounts)
}
function handleSaved(config: ServiceConfig): void {
  emit('saved', config)
}
function handleSettings(): void {
  emit('settings')
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col">
    <header class="page-x border-b border-line py-4">
      <h2 class="text-xl font-semibold text-white">{{ t('services.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500">{{ t('services.description') }}</p>
    </header>

    <nav
      class="page-x flex shrink-0 gap-1 overflow-x-auto border-b border-line py-2"
      role="tablist"
      :aria-label="t('services.title')"
    >
      <button
        v-for="tab in serviceTabs"
        :key="tab.id"
        type="button"
        role="tab"
        :aria-selected="activeService === tab.id"
        :class="[
          'flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm transition',
          activeService === tab.id
            ? 'bg-blue-600 text-white'
            : 'text-gray-400 hover:bg-raised hover:text-white',
        ]"
        @click="activeService = tab.id"
      >
        <UiIcon :name="tab.icon" :size="15" />
        {{ t(tab.label) }}
      </button>
    </nav>

    <div class="flex min-h-0 flex-1 flex-col overflow-auto">
      <KeepAlive>
        <component
          :is="activeComponent"
          :key="activeService"
          v-bind="activeProps"
          @refresh="handleRefresh"
          @updated="handleUpdated"
          @notice="handleNotice"
          @delete="handleDelete"
          @saved="handleSaved"
          @settings="handleSettings"
        />
      </KeepAlive>
    </div>
  </section>
</template>
