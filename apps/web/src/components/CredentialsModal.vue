<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="cred-overlay"
      role="dialog"
      aria-modal="true"
      @mousedown.self="close"
      @keydown.esc.window="close"
    >
      <div class="cred-dialog">
        <div class="cred-header">
          <span class="cred-title">{{ t('cred.title') }}</span>
          <button class="cred-close" :title="t('common.close')" @click="close">✕</button>
        </div>

        <div class="cred-body">
          <div class="cred-message">{{ message }}</div>

          <label class="cred-field">
            <span class="cred-label">{{ t('cred.password') }}</span>
            <input
              v-model="password"
              class="cred-input"
              type="password"
              autocomplete="off"
              :placeholder="t('cred.password')"
            />
          </label>

          <label class="cred-field">
            <span class="cred-label">{{ t('cred.passphrase') }}</span>
            <input
              v-model="passphrase"
              class="cred-input"
              type="password"
              autocomplete="off"
              :placeholder="t('cred.passphrase')"
            />
          </label>

          <div class="cred-save-row">
            <label class="cred-check">
              <input v-model="savePassword" type="checkbox" />
              <span>{{ t('cred.saveToKeyring') }}</span>
            </label>
          </div>

          <div v-if="error" class="cred-error">{{ error }}</div>
        </div>

        <div class="cred-footer">
          <button class="btn-secondary" :disabled="busy" @click="close">{{ t('common.cancel') }}</button>
          <button class="btn-primary" :disabled="busy || (!password && !passphrase)" @click="submit">
            {{ busy ? t('cred.busy') : t('cred.submit') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { t } from '../utils/i18n'
import type { CredentialPayload } from '../utils/types'

const props = defineProps<{
    open: boolean
    // 提示文案(主机名已由调用方拼好)
    message: string
    // 提交进行中(App 驱动:调 API 期间置 true 防重复提交)
    busy?: boolean
    // 提交失败的错误信息(App 回填,弹窗内展示)
    error?: string
}>()

const emit = defineEmits<{
    (e: 'submit', payload: CredentialPayload): void
    (e: 'close'): void
}>()

const password = ref('')
const passphrase = ref('')
const savePassword = ref(false)
const savePassphrase = ref(false)

// 每次打开清空上次输入
watch(
    () => props.open,
    (open) => {
        if (open) {
            password.value = ''
            passphrase.value = ''
            savePassword.value = false
            savePassphrase.value = false
        }
    },
)

function submit() {
    emit('submit', {
        password: password.value || undefined,
        passphrase: passphrase.value || undefined,
        savePassword: savePassword.value,
        savePassphrase: savePassphrase.value,
    })
}

function close() {
    if (!props.busy) emit('close')
}
</script>

<style scoped>
.cred-overlay {
    position: fixed;
    inset: 0;
    z-index: 1100;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.cred-dialog {
    width: 340px;
    max-width: calc(100vw - 32px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.cred-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-tab);
}

.cred-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--fg-bright);
}

.cred-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 13px;
    line-height: 1;
    padding: 3px 6px;
    border-radius: 3px;
    cursor: pointer;
}

.cred-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.cred-body {
    padding: 16px 14px;
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.cred-message {
    font-size: 13px;
    color: var(--fg);
    line-height: 1.5;
    word-break: break-word;
}

.cred-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.cred-label {
    font-size: 12px;
    color: var(--fg-muted);
}

.cred-input {
    height: 30px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 13px;
    font-family: inherit;
    outline: none;
}

.cred-input::placeholder {
    color: var(--fg-hint);
}

.cred-input:focus {
    border-color: var(--accent);
}

.cred-save-row {
    display: flex;
    align-items: center;
}

.cred-check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--fg-dim);
    cursor: pointer;
    user-select: none;
}

.cred-error {
    font-size: 12px;
    line-height: 1.5;
    color: var(--net-bad);
    word-break: break-word;
}

.cred-footer {
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