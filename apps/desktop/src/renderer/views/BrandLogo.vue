<script setup lang="ts">
import { useId } from "vue";

/**
 * AIClaw 标识：绿色渐变的圆角方块上一枚爪印。
 *
 * 与 `scripts/make-icon.cjs` 里的应用图标是同一份形状与配色（量自设计稿
 * apps/desktop/assets/logo-source.png），改一处要改另一处。
 *
 * 渐变 id 用 useId 生成：SVG 的 `<defs>` id 是**全文档**唯一的，同一页里出现
 * 两个写死同名渐变的标识时，后一个会引用到前一个的定义。
 */
withDefaults(defineProps<{ size?: "sm" | "md" }>(), { size: "md" });

const uid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
const gradId = `ac-grad-${uid}`;
const liftId = `ac-lift-${uid}`;
</script>

<template>
  <div class="logo" :class="`logo--${size}`" role="img" aria-label="AIClaw">
    <span class="mark" aria-hidden="true">
      <svg class="svg" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
        <defs>
          <linearGradient :id="gradId" x1="40%" y1="0%" x2="60%" y2="100%">
            <stop offset="0%" stop-color="#6ae67d" />
            <stop offset="24%" stop-color="#46d06d" />
            <stop offset="50%" stop-color="#1cb85c" />
            <stop offset="76%" stop-color="#2cc868" />
            <stop offset="100%" stop-color="#3bdc7b" />
          </linearGradient>
          <radialGradient :id="liftId" cx="85%" cy="8%" r="70%">
            <stop offset="0%" stop-color="#ffffff" stop-opacity="0.16" />
            <stop offset="100%" stop-color="#ffffff" stop-opacity="0" />
          </radialGradient>
        </defs>

        <rect width="32" height="32" rx="7.2" :fill="`url(#${gradId})`" />
        <rect width="32" height="32" rx="7.2" :fill="`url(#${liftId})`" />

        <!-- 爪印：三个趾垫、一个椭圆掌垫，左右对称。 -->
        <g fill="#ffffff">
          <circle cx="16" cy="9.03" r="3.35" />
          <circle cx="9.05" cy="12.12" r="3.22" />
          <circle cx="22.95" cy="12.12" r="3.22" />
          <ellipse cx="16" cy="20.91" rx="7.72" ry="6.04" />
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
  border-radius: 7px;
  box-shadow:
    0 1px 2px rgba(11, 61, 31, 0.16),
    0 6px 18px -8px rgba(28, 184, 92, 0.55);
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
