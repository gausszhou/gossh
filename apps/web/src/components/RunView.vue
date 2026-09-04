<template>
  <div class="run-view">
    <div class="run-meta">
      <div class="meta-row">
        <span class="meta-label">{{ t('run.host') }}</span>
        <span class="meta-value">{{ run.name }}</span>
        <span v-if="run.host_key_ok === false" class="meta-tag hostkey-tag">TOFU</span>
      </div>
      <div class="meta-row">
        <span class="meta-label">{{ t('run.command') }}</span>
        <span class="meta-value cmd-value">{{ run.command }}</span>
      </div>
      <div class="meta-row">
        <span class="meta-label">{{ t('run.exitCode') }}</span>
        <span class="meta-value exit-value" :class="exitClass">{{ run.exit_code }}</span>
        <span class="meta-label label-gap">{{ t('run.duration') }}</span>
        <span class="meta-value">{{ fmtDuration(run.duration_ms) }}</span>
      </div>
      <div v-if="run.error" class="meta-row">
        <span class="meta-label">{{ t('run.error') }}</span>
        <span class="meta-value error-value">{{ run.error }}</span>
      </div>
    </div>

    <div class="run-output-wrap">
      <div class="run-output-head">{{ t('run.output') }}</div>
      <pre v-if="run.output" class="run-output">{{ run.output }}</pre>
      <pre v-else class="run-output run-output-empty">{{ t('run.noOutput') }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { t } from '../utils/i18n'
import type { RunResult } from '../utils/types'

const props = defineProps<{
    run: RunResult
    active?: boolean
}>()

function fmtDuration(ms: number): string {
    if (ms < 1000) return `${ms} ms`
    return `${(ms / 1000).toFixed(2)} s`
}

const exitClass = computed(() => {
    if (props.run.exit_code === 0) return 'exit-ok'
    if (props.run.exit_code === -1) return 'exit-err'
    return 'exit-fail'
})
</script>

<style scoped>
.run-view {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-width: 0;
    min-height: 0;
    background: var(--bg-app);
}

.run-meta {
    flex: 0 0 auto;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border-tab);
    display: flex;
    flex-direction: column;
    gap: 6px;
    background: var(--bg-dialog);
}

.meta-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    min-width: 0;
}

.meta-label {
    color: var(--fg-muted);
    font-size: 12px;
    flex: 0 0 auto;
}

.label-gap {
    margin-left: 12px;
}

.meta-value {
    color: var(--fg);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.cmd-value {
    font-family: 'SF Mono', Consolas, monospace;
    font-size: 12px;
}

.exit-value {
    font-family: 'SF Mono', Consolas, monospace;
    font-weight: 600;
}

.exit-ok {
    color: var(--net-good);
}

.exit-fail {
    color: var(--net-fair);
}

.exit-err {
    color: var(--net-bad);
}

.error-value {
    color: var(--net-bad);
    white-space: normal;
    word-break: break-word;
}

.meta-tag {
    flex: 0 0 auto;
    font-size: 10px;
    line-height: 1;
    padding: 2px 4px;
    border-radius: 3px;
    border: 1px solid var(--border-tab);
}

.hostkey-tag {
    color: var(--net-fair);
}

.run-output-wrap {
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 0 14px 14px;
}

.run-output-head {
    flex: 0 0 auto;
    padding: 8px 0 4px;
    font-size: 12px;
    color: var(--fg-muted);
}

.run-output {
    flex: 1 1 auto;
    margin: 0;
    overflow: auto;
    font-family: 'SF Mono', Consolas, 'DejaVu Sans Mono', monospace;
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--fg);
    background: #000;
    border: 1px solid var(--border-tab);
    border-radius: 4px;
    padding: 10px 12px;
    white-space: pre-wrap;
    word-break: break-word;
}

.run-output-empty {
    color: var(--fg-hint);
}
</style>