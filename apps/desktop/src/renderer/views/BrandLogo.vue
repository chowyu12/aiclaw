<script setup lang="ts">
import { useId } from "vue";

/**
 * AIClaw 标识：主题色渐变的圆角方块上一枚爪印。
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
            <stop offset="0%" stop-color="#1b7f45" />
            <stop offset="100%" stop-color="#4fcd7f" />
          </linearGradient>
          <linearGradient :id="shineId" x1="0%" y1="0%" x2="0%" y2="100%">
            <stop offset="0%" stop-color="#ffffff" stop-opacity="0.35" />
            <stop offset="55%" stop-color="#ffffff" stop-opacity="0" />
          </linearGradient>
        </defs>

        <rect width="32" height="32" rx="9" :fill="`url(#${gradId})`" />
        <rect width="32" height="32" rx="9" :fill="`url(#${shineId})`" class="shine" />

        <!-- 爪印：一个掌垫、三个趾垫。比三道划痕更像一个「会做事的家伙」的印记。 -->
        <g fill="#ffffff">
          <circle cx="9.6" cy="11.4" r="3.1" />
          <circle cx="16" cy="8.6" r="3.3" />
          <circle cx="22.4" cy="11.4" r="3.1" />
          <path
            d="M16 26.6
               C 11.2 26.6 8.4 23.8 8.9 20.3
               C 9.4 16.9 12.4 15.2 16 15.2
               C 19.6 15.2 22.6 16.9 23.1 20.3
               C 23.6 23.8 20.8 26.6 16 26.6 Z"
          />
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
    0 1px 2px rgba(27, 127, 69, 0.18),
    0 6px 18px -8px rgba(27, 127, 69, 0.45);
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
