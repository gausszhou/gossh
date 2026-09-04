<template>
  <div class="ssh-view">
    <!-- 会话工具栏:主机标识 + 延迟 + 端口转发入口 -->
    <div class="ssh-toolbar">
      <span class="ssh-host" :title="hostLabel">{{ hostLabel }}</span>
      <span v-if="latency != null" class="net-status" :class="netClass" :title="t('tab.latency')">
        {{ latency }}ms
      </span>
      <div class="ssh-toolbar-spacer"></div>
      <button class="tool-btn" :title="t('ssh.forward')" @click="emit('forwards')">⇄ {{ t('ssh.forward') }}</button>
    </div>

    <TerminalPane
      ref="paneRef"
      class="ssh-pane"
      :session-id="sessionId"
      :host-id="hostId"
      :active="active"
      @close="emit('close')"
      @latency="(ms) => emit('latency', ms)"
      @conn="(c) => emit('conn', c)"
      @tab-title="(ti) => emit('tab-title', ti)"
      @credential-required="(msg) => emit('credential-required', msg)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import TerminalPane from './TerminalPane.vue'
import { t } from '../utils/i18n'

const props = defineProps<{
    sessionId: string
    hostId: string
    // "user@addr[:port]" 展示串
    hostLabel: string
    active?: boolean
    latency?: number | null
}>()

const emit = defineEmits<{
    (e: 'close'): void
    (e: 'latency', ms: number | null): void
    (e: 'conn', connected: boolean): void
    (e: 'tab-title', title: string): void
    (e: 'credential-required', message: string): void
    // 请求打开端口转发弹窗
    (e: 'forwards'): void
}>()

// 延迟颜色分级:绿(<30ms) / 黄(30~100ms) / 红(≥100ms)
const netClass = computed(() => {
    const l = props.latency
    if (l == null) return ''
    if (l < 30) return 'net-good'
    if (l < 100) return 'net-fair'
    return 'net-bad'
})

// 供上层在凭据重建成功后触发重连
const paneRef = ref<InstanceType<typeof TerminalPane>>()
function reattach() {
    paneRef.value?.reattach()
}

defineExpose({ reattach })
</script>

<style scoped>
.ssh-view {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-width: 0;
    min-height: 0;
}

.ssh-toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 28px;
    flex: 0 0 auto;
    padding: 0 10px;
    background: var(--bg-bar);
    border-bottom: 1px solid var(--bg-bar-border);
    user-select: none;
}

.ssh-host {
    font-size: 12px;
    color: var(--fg-bright);
    font-family: 'SF Mono', Consolas, monospace;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
}

.ssh-toolbar-spacer {
    flex: 1 1 auto;
}

.tool-btn {
    flex: 0 0 auto;
    background: none;
    border: 1px solid var(--border-tab);
    border-radius: 3px;
    color: var(--fg-dim);
    font-size: 12px;
    font-family: inherit;
    line-height: 1;
    padding: 4px 8px;
    cursor: pointer;
    white-space: nowrap;
}

.tool-btn:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.net-status {
    flex: 0 0 auto;
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 12px;
    padding: 2px 6px;
    border-radius: 3px;
}

.net-good {
    color: var(--net-good);
}

.net-fair {
    color: var(--net-fair);
}

.net-bad {
    color: var(--net-bad);
}

.ssh-pane {
    flex: 1 1 auto;
    min-height: 0;
}
</style>