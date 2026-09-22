<script setup lang="ts">
import { useId } from "vue";

/**
 * AIClaw 标识：主题色渐变的圆角方块上三道爪痕。
 *
 * 与 `scripts/make-icon.cjs` 里的应用图标是同一份形状，改一处要改另一处。
 *
 * 渐变 id 用 useId 生成：SVG 的 `<defs>` id 是**全文档**唯一的，同一页里出现
 * 两个写死同名渐变的标识时，后一个会引用到前一个的定义。这里目前只用一处，
 * 但这是那种「加第二处时才炸、且看起来像随机掉色」的问题。
 */
withDefaults(defineProps<{ size?: "sm" | "md" }>(), { size: "md" });

const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
const gradId = `ac-grad-${uid}`;
const shineId = `ac-shine-${uid}`;
</script>

<template>
  <div class="logo" :class="`logo--${size}`" role="img" aria-label="AIClaw">
    <span class="mark" aria-hidden="true">
      <svg class="svg" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
        <defs>
          <linearGradient :id="gradId" x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stop-color="#14685c" />
            <stop offset="100%" stop-color="#4fbca7" />
          </linearGradient>
          <linearGradient :id="shineId" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stop-color="#ffffff" stop-opacity="0.35" />
            <stop offset="55%" stop-color="#ffffff" stop-opacity="0" />
          </linearGradient>
        </defs>

        <rect width="32" height="32" rx="9" :fill="`url(#${gradId})`" />
        <rect width="32" height="32" rx="9" :fill="`url(#${shineId})`" class="shine" />

        <!-- 三道爪痕：从左下向右上，越靠右越短。 -->
        <g fill="none" stroke="#ffffff" stroke-width="2.8" stroke-linecap="round">
          <path d="M9 23.5 C 9 16.5 12 12 16.5 9" />
          <path d="M15 24.5 C 15.2 18.5 17.8 14.5 21.5 12" opacity="0.92" />
          <path d="M21 25 C 21.5 20.5 23.3 17.8 26 16" opacity="0.8" />
        </g>
      </svg>
    </span>
    <span class="wordmark">AIClaw</span>
  </div>
</template>

<style scoped>
.logo {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  flex-shrink: 0;
  line-height: 1;
}

.mark {
  display: inline-flex;
  position: relative;
  border-radius: 8px;
  box-shadow:
    0 1px 2px rgba(20, 104, 92, 0.18),
    0 6px 18px -8px rgba(20, 104, 92, 0.45);
}

/* 内描边：深色底上如果没有它，圆角方块的边会糊进背景里。 */
.mark::after {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: inherit;
  border: 1px solid rgba(255, 255, 255, 0.22);
  pointer-events: none;
}

.svg {
  display: block;
  width: 100%;
  height: 100%;
  border-radius: inherit;
}

.shine {
  mix-blend-mode: screen;
  pointer-events: none;
}

.wordmark {
  font-weight: 650;
  letter-spacing: 0.01em;
  color: var(--ink);
  white-space: nowrap;
}

.logo--md .mark {
  width: 26px;
  height: 26px;
}

.logo--md .wordmark {
  font-size: 13.5px;
}

.logo--sm .mark {
  width: 22px;
  height: 22px;
  border-radius: 6px;
}

.logo--sm .wordmark {
  font-size: 12.5px;
}
</style>
