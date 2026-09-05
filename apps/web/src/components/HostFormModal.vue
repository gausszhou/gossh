<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      @mousedown.self="close"
      @keydown.esc.window="close"
    >
      <div class="modal-dialog host-form">
        <div class="modal-header">
          <span class="modal-title">{{ isEdit ? t('hostForm.edit') : t('hostForm.create') }}</span>
          <button class="modal-close" :title="t('common.close')" @click="close">✕</button>
        </div>

        <div class="modal-body">
          <div class="form-grid">
            <label class="form-field">
              <span class="form-label">{{ t('hostForm.name') }} *</span>
              <input v-model="draft.name" class="form-input" type="text" spellcheck="false" />
            </label>
            <label class="form-field">
              <span class="form-label">{{ t('hostForm.address') }} *</span>
              <input v-model="draft.address" class="form-input" type="text" spellcheck="false" placeholder="host or ip" />
            </label>
            <label class="form-field">
              <span class="form-label">{{ t('hostForm.port') }}</span>
              <input v-model.number="draft.port" class="form-input" type="number" min="1" max="65535" placeholder="22" />
            </label>
            <label class="form-field">
              <span class="form-label">{{ t('hostForm.user') }} *</span>
              <input v-model="draft.user" class="form-input" type="text" spellcheck="false" />
            </label>
            <label class="form-field">
              <span class="form-label">{{ t('hostForm.group') }}</span>
              <input v-model="draft.group" class="form-input" type="text" spellcheck="false" />
            </label>
          </div>

          <!-- 凭据:方式单选 -->
          <div class="form-section">
            <div class="form-label">{{ t('hostForm.credentialKind') }}</div>
            <div class="cred-kinds">
              <button
                v-for="k in kinds"
                :key="k"
                class="kind-btn"
                :class="{ active: draft.credential.kind === k }"
                @click="setKind(k)"
              >{{ kindLabel(k) }}</button>
            </div>
            <label v-if="draft.credential.kind === 'key'" class="form-field key-path-field">
              <span class="form-label">{{ t('hostForm.credentialKeyPath') }}</span>
              <input v-model="draft.credential.key_path" class="form-input" type="text" spellcheck="false" placeholder="~/.ssh/id_ed25519" />
            </label>
            <!-- 密码:default/password 方式可直接填,勾选保存到系统钥匙串 -->
            <label v-if="draft.credential.kind === 'password' || draft.credential.kind === 'default'" class="form-field key-path-field">
              <span class="form-label">{{ t('hostForm.password') }}</span>
              <input v-model="draft.password" class="form-input" type="password" autocomplete="new-password" :placeholder="t('hostForm.passwordPlaceholder')" />
              <label class="form-check">
                <input v-model="draft.savePassword" type="checkbox" />
                <span>{{ t('hostForm.savePassword') }}</span>
              </label>
            </label>
          </div>

          <div v-if="formError" class="form-error">{{ formError }}</div>
        </div>

        <div class="modal-footer">
          <button class="btn-secondary" @click="close">{{ t('common.cancel') }}</button>
          <button class="btn-primary" :disabled="saving" @click="save">
            {{ saving ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { createHost, updateHost, APIError } from '../utils/api'
import { t } from '../utils/i18n'
import { logger } from '../utils/logger'
import type { CredentialKind, Host } from '../utils/types'

const props = defineProps<{
    open: boolean
    // null = 新建;非空 = 编辑
    host: Host | null
    hosts: Host[]
}>()

const emit = defineEmits<{
    (e: 'close'): void
    (e: 'saved'): void
}>()

const kinds: CredentialKind[] = ['default', 'key', 'agent', 'password']

function kindLabel(k: CredentialKind): string {
    return (t(`host.cred${k[0].toUpperCase()}${k.slice(1)}`) as string) || k
}

const isEdit = computed(() => !!props.host)

interface Draft {
    name: string
    address: string
    port: number | undefined
    user: string
    group: string
    credential: { kind: CredentialKind; key_path: string }
    password: string
    savePassword: boolean
}

function emptyDraft(): Draft {
    return {
        name: '',
        address: '',
        port: undefined,
        user: '',
        group: '',
        credential: { kind: 'default', key_path: '' },
        password: '',
        savePassword: false,
    }
}

const draft = ref<Draft>(emptyDraft())
const saving = ref(false)
const formError = ref('')

// 打开时:新建 → 空表单;编辑 → 回填
watch(
    () => props.open,
    (open) => {
        if (!open) return
        formError.value = ''
        const h = props.host
        if (h) {
            draft.value = {
                name: h.name || '',
                address: h.address || '',
                port: h.port || undefined,
                user: h.user || '',
                group: h.group || '',
                credential: {
                    kind: h.credential?.kind || 'default',
                    key_path: h.credential?.key_path || '',
                },
                password: '',
                savePassword: false,
            }
        } else {
            draft.value = emptyDraft()
        }
    },
)

function setKind(k: CredentialKind) {
    draft.value.credential.kind = k
}

function close() {
    if (!saving.value) emit('close')
}

// 保存:校验 → POST(新建)/ PUT(编辑,整字段替换)
async function save() {
    if (saving.value) return
    const d = draft.value

    if (!d.name.trim() || !d.address.trim() || !d.user.trim()) {
        formError.value = t('hostForm.required')
        return
    }
    if (d.port !== undefined) {
        const p = Math.trunc(d.port)
        if (!Number.isInteger(p) || p < 1 || p > 65535) {
            formError.value = t('hostForm.portInvalid')
            return
        }
        d.port = p
    }

    saving.value = true
    formError.value = ''
    const payload: Host & { password?: string; save_password?: boolean } = {
        id: props.host?.id || '',
        name: d.name.trim(),
        address: d.address.trim(),
        port: d.port,
        user: d.user.trim(),
        group: d.group.trim() || undefined,
        credential: {
            kind: d.credential.kind,
            ...(d.credential.kind === 'key' && d.credential.key_path
                ? { key_path: d.credential.key_path.trim() }
                : {}),
        },
        forwards: props.host?.forwards || [],
    }
    // 服务端不会把密码写入 hosts.json;save_password=true 时存入系统 keyring
    if (d.password) {
        payload.password = d.password
        payload.save_password = d.savePassword
    }
    try {
        if (props.host) {
            await updateHost(payload)
        } else {
            await createHost(payload)
        }
        logger.info('host', 'saved host=%s', payload.name)
        emit('saved')
    } catch (err) {
        logger.warn('host', 'save failed: %s', err)
        if (err instanceof APIError) formError.value = err.message
        else formError.value = t('hostForm.saveFailed')
    } finally {
        saving.value = false
    }
}
</script>

<style scoped>
.form-check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--fg-muted);
    cursor: pointer;
    margin-top: 4px;
}

.form-check input[type='checkbox'] {
    accent-color: var(--accent);
}
/* ── 弹窗布局(与设置弹窗同一视觉体系) ── */
.modal-overlay {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--overlay);
}

.modal-dialog {
    width: 420px;
    max-width: calc(100vw - 32px);
    background: var(--bg-dialog);
    border: 1px solid var(--border-dialog);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    max-height: calc(100vh - 48px);
}

.modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-bottom: 1px solid var(--border-tab);
}

.modal-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--fg-bright);
}

.modal-close {
    background: none;
    border: none;
    color: var(--fg-dim);
    font-size: 13px;
    line-height: 1;
    padding: 3px 6px;
    border-radius: 3px;
    cursor: pointer;
}

.modal-close:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.modal-body {
    padding: 16px 14px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 16px;
}

.form-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
}

.form-field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
}

.form-label {
    font-size: 12px;
    line-height: 1.4;
    color: var(--fg-muted);
}

.form-input {
    height: 30px;
    padding: 0 8px;
    background: var(--bg-input);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg);
    font-size: 13px;
    font-family: inherit;
    outline: none;
    width: 100%;
    min-width: 0;
}

.form-input::placeholder {
    color: var(--fg-hint);
}

.form-input:focus {
    border-color: var(--accent);
}

.form-section {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.cred-kinds {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
}

.kind-btn {
    padding: 5px 12px;
    background: var(--bg-tab);
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    color: var(--fg-dim);
    font-size: 12px;
    font-family: inherit;
    line-height: 1.4;
    cursor: pointer;
}

.kind-btn:hover {
    background: var(--bg-tab-hover);
    color: var(--fg-bright);
}

.kind-btn.active {
    background: var(--bg-tab-active);
    border-color: var(--accent);
    color: var(--fg-bright);
}

.key-path-field {
    margin-top: 2px;
}

.form-error {
    font-size: 12px;
    line-height: 1.5;
    color: var(--net-bad);
    word-break: break-word;
}

.modal-footer {
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