<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import type { ContextStatus } from "@aiclaw/agent-client";
import { store } from "../store";
import { t } from "../i18n";
const status=ref<ContextStatus|null>(null),opened=ref(false);
let generation=0,timer:ReturnType<typeof setTimeout>|undefined;
async function load():Promise<void>{
 const id=store.sessionId,ticket=++generation;if(!id){status.value=null;return;}
 try{const value=await window.aiclaw.session.context(id) as ContextStatus;if(ticket===generation&&id===store.sessionId)status.value=value;}
 catch{if(ticket===generation)status.value=null;}
}
watch(()=>[store.sessionId,store.sessionInfo],()=>{status.value=null;opened.value=false;void load();},{immediate:true});
const unsubscribe=window.aiclaw.on.agentEvent((raw:unknown)=>{
 const event=raw as {method?:string;params?:{sessionId?:string}};
 if(event.params?.sessionId===store.sessionId&&["item/completed","turn/completed"].includes(event.method??"")){clearTimeout(timer);timer=setTimeout(()=>void load(),200);}
});
onUnmounted(()=>{generation++;clearTimeout(timer);unsubscribe();});
const remaining=computed(()=>status.value&&status.value.window>0?Math.max(0,status.value.window-status.value.estimatedTokens):null);
const percent=computed(()=>status.value&&status.value.window>0?Math.round(status.value.estimatedTokens/status.value.window*100):null);
const number=(value:number)=>value.toLocaleString();
</script>
<template>
 <div class="context-control" @keydown.esc="opened=false">
  <button class="context-trigger" :aria-expanded="opened" :title="t('查看上下文用量和压缩记录')" @click="opened=!opened">{{ t('上下文') }} · {{ status ? (percent===null?t('窗口未知'):t('约 {percent}%',{percent})) : t('未知') }}</button>
  <div v-if="opened" class="context-details">
   <header><strong>{{ t('上下文用量') }}</strong><button @click="load">{{ t('刷新') }}</button><button :aria-label="t('关闭上下文详情')" @click="opened=false">×</button></header>
   <template v-if="status">
    <p>{{ t('当前文本及工具定义估算：约 {tokens} Token',{tokens:number(status.estimatedTokens)}) }}</p>
    <p>{{ status.window>0?t('配置的上下文窗口：{tokens} Token',{tokens:number(status.window)}):t('上下文窗口未配置，无法计算剩余空间。') }}</p>
    <p v-if="remaining!==null">{{ t('估算剩余：约 {tokens} Token',{tokens:number(remaining)}) }}</p>
    <progress v-if="percent!==null" :value="Math.min(percent,100)" max="100" />
    <p>{{ status.lastInputTokens>0?t('最近一次模型服务报告的输入：{tokens} Token',{tokens:number(status.lastInputTokens)}):t('模型服务尚未报告当前会话的输入用量。') }}</p>
    <p class="note">{{ t('估算不等于模型账单用量，也不包含图片的视觉 Token。') }}</p>
    <strong>{{ t('压缩记录 · {count}',{count:status.compactions}) }}</strong>
    <p class="note">{{ t('从本版本开始记录，仅展示最近 50 次；压缩前后用量均为文本估算。') }}</p>
    <ol><li v-for="(record, index) in [...status.records].reverse()" :key="index">{{ new Date(record.at).toLocaleString() }} · {{ number(record.before) }} → {{ number(record.after) }}</li></ol>
    <p v-if="!status.records.length" class="note">{{ t('暂无压缩记录') }}</p>
   </template><p v-else>{{ t('上下文数据暂不可用，请在会话恢复后重试。') }}</p>
  </div>
 </div>
</template>
<style scoped>
.context-control{display:flex;justify-content:flex-end;position:relative;padding:0 2px 4px}.context-control button{font:inherit;font-size:11px;border:0;background:transparent;color:var(--muted);cursor:pointer;border-radius:6px;padding:3px 7px}.context-trigger:hover{background:var(--accent-soft);color:var(--accent)}.context-details{position:absolute;right:0;bottom:100%;z-index:25;width:min(400px,calc(100vw - 290px));max-height:60vh;overflow:auto;padding:16px;background:var(--surface);border:1px solid var(--rule);border-radius:12px;box-shadow:0 8px 30px #0001;font-size:12px;line-height:1.7}.context-details header{display:flex;align-items:center;gap:8px}.context-details header strong{flex:1}.context-details p{margin:9px 0}.context-details .note{color:var(--muted);font-size:11px}.context-details progress{width:100%;height:5px;accent-color:var(--accent)}.context-details ol{padding-left:20px}.context-details li{padding:4px 0}button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
</style>
