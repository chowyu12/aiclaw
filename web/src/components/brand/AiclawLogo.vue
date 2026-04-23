<template>
  <svg
    class="aiclaw-logo"
    :class="[{ 'aiclaw-logo--compact': compact }, sizeClass]"
    xmlns="http://www.w3.org/2000/svg"
    :viewBox="compact ? '0 0 44 44' : '0 0 196 44'"
    aria-label="AIClaw"
    role="img"
  >
    <title>AIClaw</title>
    <defs>
      <linearGradient :id="bgId" x1="0%" y1="0%" x2="100%" y2="100%">
        <stop offset="0%" stop-color="#0ea5e9" />
        <stop offset="55%" stop-color="#6366f1" />
        <stop offset="100%" stop-color="#ec4899" />
      </linearGradient>
      <linearGradient :id="strokeId" x1="0%" y1="100%" x2="100%" y2="0%">
        <stop offset="0%" stop-color="#ffffff" stop-opacity="0.85" />
        <stop offset="100%" stop-color="#ffffff" stop-opacity="1" />
      </linearGradient>
      <linearGradient :id="textId" x1="0%" y1="0%" x2="100%" y2="0%">
        <stop offset="0%" stop-color="#0ea5e9" />
        <stop offset="100%" stop-color="#a855f7" />
      </linearGradient>
    </defs>

    <!-- 六边形容器（科技感，区分于 squircle） -->
    <polygon
      points="22,2 39.32,12 39.32,32 22,42 4.68,32 4.68,12"
      :fill="bgRef"
      stroke="rgba(255,255,255,0.08)"
      stroke-width="0.5"
    />

    <!-- 顶部高光 -->
    <path
      d="M22 2 L39.32 12 L39.32 18 L4.68 18 L4.68 12 Z"
      fill="#ffffff"
      opacity="0.10"
    />

    <!-- 3 道神经爪痕，端点带节点 -->
    <g fill="none" :stroke="strokeRef" stroke-width="2.6" stroke-linecap="round">
      <path d="M14 33 C14 23, 17 19, 21 16" />
      <path d="M21 34 C21 24, 24 20, 28 17" />
      <path d="M28 33 C28 24, 31 21, 35 19" />
    </g>
    <g fill="#ffffff">
      <circle cx="21" cy="16" r="2" />
      <circle cx="28" cy="17" r="2" />
      <circle cx="35" cy="19" r="2" />
    </g>

    <!-- 字标 -->
    <g v-if="!compact" transform="translate(54, 0)">
      <text
        x="0"
        y="29"
        font-family="'Inter', system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
        font-size="22"
        font-weight="700"
        letter-spacing="-0.025em"
      >
        <tspan :fill="textRef">AI</tspan><tspan fill="currentColor">Claw</tspan>
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

const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
const bgId = `aiclaw-bg-${uid}`
const strokeId = `aiclaw-stroke-${uid}`
const textId = `aiclaw-text-${uid}`
const bgRef = computed(() => `url(#${bgId})`)
const strokeRef = computed(() => `url(#${strokeId})`)
const textRef = computed(() => `url(#${textId})`)
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
