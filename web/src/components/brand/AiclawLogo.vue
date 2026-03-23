<template>
  <svg
    class="aiclaw-logo"
    :class="[{ 'aiclaw-logo--compact': compact }, sizeClass]"
    xmlns="http://www.w3.org/2000/svg"
    :viewBox="compact ? '0 0 44 44' : '0 0 212 44'"
    aria-label="AIClaw"
    role="img"
  >
    <title>AIClaw</title>
    <defs>
      <linearGradient :id="gradId" x1="0%" y1="0%" x2="100%" y2="100%">
        <stop offset="0%" stop-color="#22d3ee" />
        <stop offset="100%" stop-color="#6366f1" />
      </linearGradient>
    </defs>
    <!-- 爪痕 + 暗示「A」的折线 -->
    <g :transform="compact ? 'translate(6, 6)' : 'translate(4, 5)'" fill="none">
      <path
        d="M4 32 C4 14 10 6 18 4"
        :stroke="gradRef"
        stroke-width="3.2"
        stroke-linecap="round"
      />
      <path
        d="M12 32 C12 16 18 8 26 6"
        :stroke="gradRef"
        stroke-width="3.2"
        stroke-linecap="round"
      />
      <path
        d="M20 32 C20 18 26 10 34 8"
        :stroke="gradRef"
        stroke-width="3.2"
        stroke-linecap="round"
      />
    </g>
    <g v-if="!compact" transform="translate(48, 0)">
      <text
        x="0"
        y="30"
        font-family="system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
        font-size="22"
        font-weight="700"
        letter-spacing="-0.03em"
        fill="currentColor"
      >
        <tspan fill="currentColor" opacity="0.92">AI</tspan><tspan fill="currentColor">Claw</tspan>
      </text>
    </g>
  </svg>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 仅显示图形标（侧栏折叠） */
    compact?: boolean
    /** 视觉尺寸档位 */
    size?: 'sm' | 'md' | 'lg'
  }>(),
  { compact: false, size: 'md' }
)

const gradId = `aiclaw-grad-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
const gradRef = computed(() => `url(#${gradId})`)
const sizeClass = computed(() => `aiclaw-logo--${props.size}`)
</script>

<style scoped>
.aiclaw-logo {
  display: block;
  flex-shrink: 0;
  color: inherit;
}
.aiclaw-logo--sm {
  height: 22px;
  width: auto;
}
.aiclaw-logo--sm.aiclaw-logo--compact {
  width: 28px;
  height: 28px;
}
.aiclaw-logo--md {
  height: 26px;
  width: auto;
}
.aiclaw-logo--md.aiclaw-logo--compact {
  width: 32px;
  height: 32px;
}
.aiclaw-logo--lg {
  height: 36px;
  width: auto;
}
.aiclaw-logo--lg.aiclaw-logo--compact {
  width: 44px;
  height: 44px;
}
</style>
