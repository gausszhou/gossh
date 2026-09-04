<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="run-overlay"
      role="dialog"
      aria-modal="true"
      @mousedown.self="close"
      @keydown.esc.window="close"
    >
      <div class="run-dialog">
        <div class="run-header">
          <span class="run-title">{{ t('run.title') }} · {{ host?.name }}</span>
          <button class="run-close" :title="t('common.close')" @click="close">✕</button>
        </div>

        <div class="run-body">
          <label class="run-field">
            <span class="run-label">{{ t('run.command') }} *</span>
            <input
              v-model="command"
              class="run-input"
              type="text"
              :placeholder="t('run.commandPlaceholder')"
              spellcheck="false"
              @keydown.enter="run"
            />
          </label>

          <label class="run-field">
            <span class="run-label">{{ t('run.args') }}</span>
            <input
              v-model="argsText"
              class="run-input"
              type="text"
              :placeholder="t('run.argsPlaceholder')"
              spellcheck="false"
            />
          </label>

          <label class="run-field">
            <span class="run-label">{{ t('run.timeout') }}</span>
            <input
              v-model.number="timeoutSec"
              class="run-input run-input-narrow"
              type="number"
              min="1"
              placeholder="60"
            />
          </label>

          <div v-if="error" class="run-error">{{ error }}</div>
        </div>

        <div class="run-footer">
          <button class="btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button>
          <button class="btn-primary" :disabled="busy || !command.trim()" @click="run">
            {{ busy ? t('common.loading') : t('run.submit') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { runCommand, type RunRequest } from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { Host, RunResult } from '../utils/types'

const props = defineProps<{
    open: boolean
    host: Host | null
}>()

const emit = defineEmits<{
    (e: 'close'): void
    // 执行成功:上层据此开页签(并关闭本弹窗)
    (e: 'run', result: RunResult): void
    (e: 'credential-required', message: string): void
}>()

const command = ref('')
const argsText = ref('')
const timeoutSec = ref<number | undefined>(undefined)
const busy = ref(false)
const error = ref('')

// 每次打开重置
watch(
    () => props.open,
    (open) => {
        if (open) {
            command.value = ''
            argsText.value = ''
            timeoutSec.value = undefined
            busy.value = false
            error.value = ''
        }
    },
)

function buildRequest(): RunRequest {
    const req: RunRequest = { host_id: props.host!.id, command: command.value.trim() }
    const args = argsText.value.trim().split(/\s+/).filter(Boolean)
    if (args.length) req.args = args
    if (timeoutSec.value !== undefined && Number.isFinite(timeoutSec.value) && timeoutSec.value > 0) {
        req.timeout_ms = Math.trunc(timeoutSec.value) * 1000
    }
    return req
}

async function run() {
    if (busy.value || !props.host || !command.value.trim()) return
    busy.value = true
    error.value = ''
    try {
        const result = await runCommand(buildRequest())
        logger.info('run', 'run ok host=%s exit=%d duration=%dms', props.host.id, result.exit_code, result.duration_ms)
        emit('run', result)
    } catch (err) {
        logger.warn('run', 'run failed: %s', err)
        error.value = err instanceof Error ? err.message : String(err)
    } finally {
        busy.value = false
    }
}

function close() {
    if (!busy.value) emit('close')
}
</script>

<style scoped>
.run-overlay {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.run-dialog {
    width: 420px;
    max-width: calc(100vw - 32px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.run-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-tab);
}

.run-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--fg-bright);
}

.run-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 13px;
    line-height: 1;
    padding: 3px 6px;
    border-radius: 3px;
    cursor: pointer;
}

.run-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.run-body {
    padding: 16px 14px;
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.run-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.run-label {
    font-size: 12px;
    color: var(--fg-muted);
}

.run-input {
    height: 30px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 13px;
    font-family: inherit;
    outline: none;
    font-family: 'SF Mono', Consolas, monospace;
}

.run-input::placeholder {
    color: var(--fg-hint);
}

.run-input:focus {
    border-color: var(--accent);
}

.run-input-narrow {
    width: 160px;
}

.run-error {
    font-size: 12px;
    line-height: 1.5;
    color: var(--net-bad);
    word-break: break-word;
}

.run-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 14px;
    border-top: 1px solid var(--border-tab);
}

.btn-primary {
    height: 28px;
    padding: 0 14px;
    background: var(--accent);
    border: none;
    border-radius: 3px;
    color: var(--fg-bright);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
}

.btn-primary:hover {
    filter: brightness(1.1);
}

.btn-primary:disabled {
    opacity: 0.6;
    cursor: default;
}

.btn-secondary {
    height: 28px;
    padding: 0 14px;
    background: var(--bg-tab-hover);
    border: none;
    border-radius: 3px;
    color: var(--fg);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
}

.btn-secondary:hover {
    filter: brightness(1.15);
}
</style>