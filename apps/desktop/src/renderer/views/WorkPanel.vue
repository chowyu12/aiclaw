<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from "vue";
import type { SessionWork, FileChange, GoalUpdate, PlanStep, Goal } from "@aiclaw/agent-client";
import { store, actions } from "../store";
import { t } from "../i18n";
import { describeError } from "../errors";
import { fileDiff } from "../file-diff";

const sessionApi = window.aiclaw.session;
const work = ref<SessionWork | null>(null);
const changes = ref<FileChange[]>([]);
const saving = ref(false), error = ref(""), creating = ref(false);
const objective = ref(""), acceptance = ref(""), plan = ref(""), evidence = ref("");
const tokenBudget = ref(0), minutes = ref(0), selected = ref("");
const diff = computed(() => { const c = changes.value.find(c => c.id === selected.value); return c ? fileDiff(c.before, c.after, 3000) : []; });
const statusLabels = computed(() => ({active:t("进行中"),paused:t("已暂停"),blocked:t("等待处理"),complete:t("已完成"),budget_exhausted:t("预算已用尽")}));
let generation = 0;
async function refresh() {
  const id = store.sessionId, ticket = ++generation;
  if (!id || store.sessionInfo?.sessionId !== id) { work.value=null;changes.value=[];return; }
  try {
    const [w,c] = await Promise.all([sessionApi.work(id), sessionApi.changes(id)]);
    if (ticket !== generation || id !== store.sessionId) return;
    work.value=w;changes.value=c;
  } catch(e) { if(ticket===generation) error.value=describeError(e); }
}
watch(() => [store.sessionId,store.sessionInfo?.sessionId,store.busy],()=>{void refresh();},{immediate:true});
watch(() => store.sessionId,()=>{error.value="";creating.value=false;selected.value="";});
const unsubscribe=window.aiclaw.on.agentEvent((raw:unknown)=>{
 const event=raw as {method?:string;params?:{sessionId?:string;goal?:Goal;item?:{toolName?:string}}};
 if(event.params?.sessionId===store.sessionId && event.method==="session/workUpdated" && event.params.goal && work.value){work.value.goal=event.params.goal;return;}
 if(event.params?.sessionId===store.sessionId && event.method==="item/completed" && /goal/.test(event.params.item?.toolName??"")) void refresh();
});
onUnmounted(()=>{generation++;unsubscribe();});
async function perform(fn:(id:string)=>Promise<unknown>) {
 if(saving.value||!store.sessionId)return;
 const id=store.sessionId;saving.value=true;error.value="";
 try {await fn(id);await refresh();}catch(e){error.value=describeError(e);}finally{saving.value=false;}
}
async function create(){await perform(async id=>{
 await sessionApi.setGoal(id,{objective:objective.value,acceptance:acceptance.value,steps:plan.value.split("\n").map(text=>text.trim()).filter(Boolean).map(text=>({text,status:"pending"})),tokenBudget:tokenBudget.value,timeBudgetMs:minutes.value*60000});
 creating.value=false;
 if(store.sessionId!==id || !(await actions.send(t("请开始执行已设定的目标，按验收条件验证结果并更新计划。")))) await sessionApi.updateGoal(id,{status:"paused"});
});}
function update(update:GoalUpdate){return perform(id=>sessionApi.updateGoal(id,update));}
async function resume(){await perform(async id=>{
 await sessionApi.updateGoal(id,{status:"active"});
 if(store.sessionId!==id || !(await actions.send(t("请继续已设定的目标。先检查当前状态，避免重复执行结果不确定的操作。")))) await sessionApi.updateGoal(id,{status:"paused"});
});}
function stepStatus(index:number,event:Event){
 const steps=work.value?.goal?.steps.map(s=>({...s}));if(!steps)return;
 steps[index]!.status=(event.target as HTMLSelectElement).value as PlanStep["status"];void update({steps});
}
async function fork(){await perform(async id=>{const next=await sessionApi.fork(id);await actions.refreshSessions();await actions.openSession(next.sessionId);});}
function editBudget(){tokenBudget.value=work.value?.goal?.tokenBudget??0;minutes.value=(work.value?.goal?.timeBudgetMs??0)/60000;}
</script>
<template>
<section v-if="store.sessionId" class="work-panel">
 <div class="work-toolbar">
  <button @click="creating=!creating" :disabled="saving||store.busy||!work||!!work?.goal&&work.goal.status!=='complete'">{{t("设置目标")}}</button>
  <button @click="fork" :disabled="saving||store.busy">{{t("分叉会话")}}</button>
  <button @click="refresh" :disabled="saving">{{t("刷新状态")}}</button>
  <button v-if="work?.forkSourceId" @click="actions.openSession(work.forkSourceId)">{{t("查看来源会话")}}</button>
 </div>
 <p v-if="error" role="alert">{{error}}</p>
 <form v-if="creating" @submit.prevent="create" class="goal-form">
  <label>{{t("目标")}}<textarea v-model="objective" required /></label>
  <label>{{t("验收条件")}}<textarea v-model="acceptance" required /></label>
  <label>{{t("计划步骤（每行一步）")}}<textarea v-model="plan" /></label>
  <label>{{t("Token 预算（0 为不限）")}}<input type="number" min="0" step="1" v-model.number="tokenBudget" /></label>
  <label>{{t("执行时间预算（分钟，0 为不限）")}}<input type="number" min="0" step="1" v-model.number="minutes" /></label>
  <button type="submit" :disabled="saving">{{t("创建并开始")}}</button>
 </form>
 <details v-if="work?.goal" open>
  <summary>{{t("目标")}} · {{statusLabels[work.goal.status]}} · {{work.goal.objective}}</summary>
  <p>{{work.goal.acceptance}}</p>
  <p v-if="work.goal.usageIncomplete">{{ t("供应商未报告部分 Token 用量，累计值不完整。") }}</p>
  <p>{{t("累计用量")}}: {{work.goal.tokensUsed}} / {{work.goal.tokenBudget||'∞'}} tokens · {{(work.goal.elapsedMs/60000).toFixed(1)}} / {{work.goal.timeBudgetMs?(work.goal.timeBudgetMs/60000).toFixed(1):'∞'}} min</p>
  <ol><li v-for="(step,index) in work.goal.steps" :key="index">
   <select :value="step.status" @change="stepStatus(index,$event)" :disabled="saving||store.busy||work.goal.status==='complete'">
    <option value="pending">{{t("待执行")}}</option><option value="in_progress">{{t("进行中")}}</option><option value="completed">{{t("已完成")}}</option>
   </select> {{step.text}}
  </li></ol>
  <p v-if="work.goal.evidence">{{t("验证记录")}}: {{work.goal.evidence}}</p>
  <button v-if="work.goal.status==='active'" @click="update({status:'paused'})" :disabled="saving">{{t("暂停目标")}}</button>
  <button v-else-if="work.goal.status!=='complete'" @click="resume" :disabled="saving||store.busy||!!work.pending.length">{{t("继续目标")}}</button>
  <details v-if="work.goal.status!=='complete'" @toggle="editBudget">
   <summary>{{t("调整预算与完成记录")}}</summary>
   <label>{{t("Token 预算（0 为不限）")}}<input type="number" min="0" step="1" v-model.number="tokenBudget" /></label>
   <label>{{t("执行时间预算（分钟，0 为不限）")}}<input type="number" min="0" step="1" v-model.number="minutes" /></label>
   <button :disabled="saving||store.busy" @click="update({tokenBudget,timeBudgetMs:minutes*60000})">{{t("保存预算")}}</button>
   <label>{{t("验证记录")}}<textarea v-model="evidence" /></label>
   <button :disabled="saving||store.busy||!evidence.trim()||work.goal.steps.some(s=>s.status!=='completed')" @click="update({status:'complete',evidence})">{{t("标记完成")}}</button>
  </details>
  <small>{{t("重启后目标会暂停；处理恢复记录后可继续。")}}</small>
 </details>
 <details v-if="work?.pending.length" open>
  <summary>{{t("待恢复的输入")}} ({{work.pending.length}})</summary>
  <article v-for="pending in work.pending" :key="pending.requestId">
   <strong>{{pending.state==='queued'?t("已接收，尚未处理"):t("执行结果待确认")}}</strong>
   <p>{{pending.payload.text||t("附件消息")}}</p>
   <p v-if="pending.state==='uncertain'">{{t("请先检查执行结果，再发送后续指令；这条输入不会自动重放。")}}</p>
   <button v-if="pending.state==='queued'" :disabled="saving||store.busy" @click="perform(id=>sessionApi.recover(id,pending.requestId,'resume'))">{{t("处理这条输入")}}</button>
   <button :disabled="saving||store.busy" @click="perform(id=>sessionApi.recover(id,pending.requestId,'dismiss'))">{{t("已检查，移除恢复提示")}}</button>
  </article>
 </details>
 <details v-if="changes.length">
  <summary>{{t("文件改动")}} ({{changes.length}})</summary>
  <p>{{t("记录 write_file 和 edit_file 的文本改动；命令及外部工具改动不在此列表。")}}</p>
  <article v-for="change in changes" :key="change.id">
   <button @click="selected=selected===change.id?'':change.id">{{change.path}}</button>
   <span v-if="change.state==='undone'">{{t("已撤销")}}</span>
   <span v-else-if="change.conflict">{{t("文件已变化，禁止覆盖撤销")}}</span>
   <button v-else :disabled="saving||store.busy" @click="perform(id=>sessionApi.undo(id,change.id))">{{t("撤销此改动")}}</button>
   <div v-if="selected===change.id" class="diff" role="region" :aria-label="t('文本差异')">
    <div v-for="(line,index) in diff" :key="index" :class="line.kind"><span>{{line.kind==='add'?'+':line.kind==='remove'?'-':' '}}</span>{{line.text}}</div>
    <small>{{t("最多显示 3000 行差异；撤销前会校验完整文件内容。")}}</small>
   </div>
  </article>
 </details>
 <small>{{t("分叉仅复制对话历史，继续共用原工作目录。")}}</small>
</section>
</template>
<style scoped>
.work-panel{margin:0 auto 18px;max-width:860px;padding:12px;border:1px solid var(--rule);border-radius:10px;font-size:12px}.work-toolbar{display:flex;gap:8px;flex-wrap:wrap}.work-panel details{margin-top:10px}.work-panel summary{cursor:pointer}.work-panel label{display:flex;flex-direction:column;gap:4px;margin:8px 0}.work-panel textarea{min-height:55px;width:100%;box-sizing:border-box}.work-panel input{max-width:200px}.work-panel button{cursor:pointer;padding:5px 10px;border:1px solid var(--rule-strong);border-radius:var(--r-sm);background:var(--surface);color:var(--ink-2);font:inherit}.work-panel button:hover:not(:disabled){background:var(--hover)}.work-panel button:disabled{opacity:.45;cursor:default}.work-panel select{display:inline-block;width:125px;padding:5px;margin-right:8px}.work-panel li{line-height:1.8}.work-panel article{padding:8px 0;border-bottom:1px solid var(--rule)}.work-panel p{white-space:pre-wrap;overflow-wrap:anywhere}.work-panel ol{padding-left:24px}.work-panel li{margin:6px 0}.diff{max-height:360px;overflow:auto;white-space:pre;font-family:monospace;margin-top:8px}.diff .add{background:#19875420}.diff .remove{background:#dc354520}.diff span{display:inline-block;width:16px}.work-panel small{display:block;margin-top:8px;opacity:.7}
</style>
