<template>
  <div class="aic-page">
    <div class="aic-page-head">
      <h1 class="aic-title">渠道 Channel</h1>
      <p class="aic-sub"></p>
    </div>
    <div class="aic-page-body">
      <el-card class="aic-card" shadow="never">
        <template #header>
          <div class="aic-card-header">
            <span class="aic-card-title">渠道列表</span>
            <div>
              <el-input
                v-model="keyword"
                placeholder="搜索名称/描述"
                clearable
                style="width: 200px; margin-right: 12px"
                @clear="loadData"
                @keyup.enter="loadData"
              >
                <template #prefix
                  ><el-icon><Search /></el-icon
                ></template>
              </el-input>
              <el-button type="primary" @click="openCreate">
                <el-icon><Plus /></el-icon> 新增渠道
              </el-button>
            </div>
          </div>
        </template>

        <el-table :data="list" v-loading="loading" stripe>
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column
            prop="name"
            label="名称"
            width="140"
            show-overflow-tooltip
          />
          <el-table-column label="类型" width="120">
            <template #default="{ row }">
              <el-tag size="small" type="info">{{
                typeLabel(row.channel_type)
              }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column
            prop="enabled"
            label="状态"
            width="140"
            align="center"
          >
            <template #default="{ row }">
              <div class="status-cell">
                <el-switch
                  :model-value="row.enabled"
                  :loading="toggleLoadingId === row.id"
                  inline-prompt
                  active-text="开"
                  inactive-text="关"
                  @change="onToggleEnabled(row, $event as boolean)"
                />
                <el-tag :type="row.enabled ? 'success' : 'info'" size="small">{{
                  row.enabled ? "已启用" : "已禁用"
                }}</el-tag>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="Webhook / 接入" min-width="200">
            <template #default="{ row }">
              <template v-if="row.channel_type === 'wecom'">
                <span class="webhook-muted">WebSocket 长连接</span>
                <el-tooltip
                  content="HTTP 入口仅作校验/探测；业务消息走 WS"
                  placement="top"
                >
                  <code class="webhook-snippet webhook-secondary">{{
                    webhookURL(row.uuid)
                  }}</code>
                </el-tooltip>
              </template>
              <template v-else>
                <code class="webhook-snippet">{{ webhookURL(row.uuid) }}</code>
                <el-button
                  link
                  type="primary"
                  size="small"
                  @click="copyText(webhookURL(row.uuid))"
                  >复制</el-button
                >
              </template>
            </template>
          </el-table-column>
          <el-table-column
            prop="updated_at"
            label="更新时间"
            width="170"
            show-overflow-tooltip
          />
          <el-table-column label="操作" width="230" fixed="right">
            <template #default="{ row }">
              <el-button link type="success" @click="openChannelConversations(row)"
                >查看会话</el-button
              >
              <el-button link type="primary" @click="openEdit(row)"
                >编辑</el-button
              >
              <el-popconfirm
                title="确定删除该渠道？"
                @confirm="handleDelete(row.id)"
              >
                <template #reference>
                  <el-button link type="danger">删除</el-button>
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>

        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          style="margin-top: 16px; justify-content: flex-end"
          @size-change="loadData"
          @current-change="loadData"
        />
      </el-card>

      <el-card v-if="selectedChannel" class="aic-card" shadow="never" style="margin-top: 16px">
        <template #header>
          <div class="aic-card-header">
            <span class="aic-card-title">
              最近会话 · {{ selectedChannel.name }}
            </span>
            <div class="conv-filter-bar">
              <el-input
                v-model="convFilterThread"
                placeholder="Thread 关键字"
                clearable
                style="width: 180px"
                @clear="refreshChannelConversations"
                @keyup.enter="refreshChannelConversations"
              />
              <el-input
                v-model="convFilterSender"
                placeholder="Sender 关键字"
                clearable
                style="width: 180px"
                @clear="refreshChannelConversations"
                @keyup.enter="refreshChannelConversations"
              />
              <el-button @click="refreshChannelConversations">
                <el-icon><Search /></el-icon> 查询
              </el-button>
              <el-button @click="clearChannelConversationPanel">收起</el-button>
            </div>
          </div>
        </template>

        <el-table :data="channelConversations" v-loading="convLoading" stripe>
          <el-table-column prop="conversation_id" label="会话ID" width="90" />
          <el-table-column prop="title" label="标题" min-width="160" show-overflow-tooltip />
          <el-table-column prop="sender_id" label="Sender" width="160" show-overflow-tooltip />
          <el-table-column prop="message_count" label="对话轮数" width="100" align="center">
            <template #default="{ row }">
              <el-tag size="small" type="info">{{ row.message_count ?? 0 }} 轮</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="last_user_message" label="最近用户消息" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ row.last_user_message || "-" }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="last_reply_message" label="最近回复" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ row.last_reply_message || "-" }}</span>
            </template>
          </el-table-column>
          <el-table-column label="Thread Keys" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">
              <span>{{ (row.thread_keys || []).join(' | ') || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="更新时间" width="170">
            <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="150" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openConversationMessages(row)">消息记录</el-button>
              <el-popconfirm title="确定删除该渠道会话？" @confirm="deleteChannelConversation(row.conversation_id)">
                <template #reference>
                  <el-button link type="danger">删除</el-button>
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>

        <el-pagination
          v-model:current-page="convPage"
          v-model:page-size="convPageSize"
          :total="convTotal"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          style="margin-top: 16px; justify-content: flex-end"
          @size-change="loadChannelConversations"
          @current-change="loadChannelConversations"
        />
      </el-card>
    </div>

    <el-drawer
      v-model="msgDrawerVisible"
      :title="'消息记录 · ' + (msgConversation?.title || '')"
      size="640px"
      destroy-on-close
    >
      <div v-loading="msgLoading" class="msg-drawer-body">
        <div v-if="conversationMessages.length === 0 && !msgLoading" class="msg-empty">
          暂无消息记录
        </div>
        <div
          v-for="msg in conversationMessages"
          :key="msg.id"
          class="msg-item"
          :class="'msg-role-' + msg.role"
        >
          <div class="msg-meta">
            <el-tag :type="msg.role === 'user' ? 'primary' : msg.role === 'assistant' ? 'success' : 'info'" size="small">
              {{ msg.role === 'user' ? '用户' : msg.role === 'assistant' ? '助手' : msg.role }}
            </el-tag>
            <span class="msg-time">{{ formatTime(msg.created_at) }}</span>
            <span v-if="msg.tokens_used" class="msg-tokens">{{ msg.tokens_used }} tokens</span>
          </div>
          <div class="msg-content">{{ msg.content }}</div>
        </div>
      </div>
    </el-drawer>

    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑渠道' : '新增渠道'"
      width="720px"
      destroy-on-close
      @closed="resetForm"
    >
      <el-form :model="form" label-width="150px" class="channel-form">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="便于识别的名称" />
        </el-form-item>
        <el-form-item label="类型" required>
          <el-select
            v-model="form.channel_type"
            style="width: 100%"
            :disabled="isEdit"
            placeholder="选择平台"
          >
            <el-option
              v-for="opt in typeOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item v-if="needsWebhookTokenField" label="Webhook 密钥">
          <el-input
            v-model="form.webhook_token"
            type="password"
            show-password
            placeholder="可选；POST 回调须带 X-Webhook-Token 或 ?token="
          />
        </el-form-item>
        <el-form-item label="说明">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="2"
            placeholder="备注"
          />
        </el-form-item>

        <el-divider content-position="left">平台配置</el-divider>

        <el-alert
          v-if="form.channel_type === 'wecom'"
          type="info"
          :closable="false"
          show-icon
          class="config-hint"
        >
          <template #title>企业微信智能机器人（WebSocket）</template>
          <div class="hint-lines">
            保存并启用后，服务端会自动连接企微开放平台。无需配置本页 Webhook
            URL。代码路径：
            <a
              href="https://github.com/chowyu12/aiclaw/tree/main/pkg/wecomaibot"
              target="_blank"
              rel="noopener noreferrer"
              >pkg/wecomaibot</a
            >。
          </div>
        </el-alert>

        <template v-if="form.channel_type === 'wecom'">
          <el-form-item label="Bot ID" required>
            <el-input
              v-model="cfg.bot_id"
              placeholder="智能机器人 Bot ID"
              clearable
            />
          </el-form-item>
          <el-form-item label="Secret" required>
            <el-input
              v-model="cfg.secret"
              type="password"
              show-password
              placeholder="智能机器人 Secret"
              clearable
            />
          </el-form-item>
        </template>

        <template v-else-if="form.channel_type === 'whatsapp'">
          <el-form-item label="Verify Token">
            <el-input
              v-model="cfg.verify_token"
              type="password"
              show-password
              placeholder="与 Meta 控制台 Webhook 校验 token 一致（config.verify_token）"
              clearable
            />
          </el-form-item>
          <el-alert type="info" :closable="false" show-icon class="config-hint">
            <template #title>后端当前仅读取 verify_token</template>
            <div class="hint-lines">
              Phone Number ID、Graph API Token 等请在「附加
              JSON」中自行扩展（适配器未使用则不会生效）。
            </div>
          </el-alert>
        </template>

        <template v-else-if="form.channel_type === 'telegram'">
          <el-form-item label="Bot Token" required>
            <el-input
              v-model="cfg.bot_token"
              type="password"
              show-password
              placeholder="BotFather 下发的 token，写入 config.bot_token"
              clearable
            />
          </el-form-item>
        </template>

        <template v-else-if="form.channel_type === 'feishu'">
          <el-alert type="info" :closable="false" show-icon class="config-hint">
            <template #title>飞书事件与发消息（oapi-sdk-go/v3）</template>
            <div class="hint-lines">
              在开放平台配置事件订阅请求网址为本页 Webhook URL；加密开启时需填写
              Encrypt Key。机器人需具备接收消息与发送消息权限。
            </div>
          </el-alert>
          <el-form-item label="App ID" required>
            <el-input
              v-model="cfg.app_id"
              placeholder="应用凭证 App ID"
              clearable
            />
          </el-form-item>
          <el-form-item label="App Secret" required>
            <el-input
              v-model="cfg.app_secret"
              type="password"
              show-password
              placeholder="应用凭证 App Secret"
              clearable
            />
          </el-form-item>
          <el-form-item label="Verification Token" required>
            <el-input
              v-model="cfg.verification_token"
              placeholder="事件订阅 Verification Token"
              clearable
            />
          </el-form-item>
          <el-form-item label="Encrypt Key">
            <el-input
              v-model="cfg.encrypt_key"
              type="password"
              show-password
              placeholder="事件加密密钥（启用加密时必填，否则无法解密与验签）"
              clearable
            />
          </el-form-item>
        </template>

        <template v-else>
          <el-alert
            type="warning"
            :closable="false"
            show-icon
            class="config-hint"
          >
            <template #title>暂无专用配置项</template>
            <div class="hint-lines">
              当前后端对该类型<strong>不读取</strong>
              <code>config</code> 内凭证字段，仅需将上方 Webhook URL
              配到第三方；回复能力需在服务端扩展。 若有自定义键可写在下方「附加
              JSON」。
            </div>
          </el-alert>
        </template>

        <el-collapse class="extras-collapse">
          <el-collapse-item title="附加配置（可选，JSON）" name="extras">
            <el-input
              v-model="configExtrasStr"
              type="textarea"
              :rows="5"
              placeholder="{}"
              class="mono"
            />
            <p class="extras-tip">
              合并进
              config；与上方已展示字段同名的键以表单为准。无特殊需求可留空
              <code>{}</code>。
            </p>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submitForm"
          >保存</el-button
        >
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, computed, onMounted } from "vue";
import { ElMessage } from "element-plus";
import { channelApi, type Channel, type ChannelType, type ChannelConversationItem, type ChannelMessage } from "@/api/channel";

const list = ref<Channel[]>([]);
const loading = ref(false);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const keyword = ref("");
const selectedChannel = ref<Channel | null>(null);
const channelConversations = ref<ChannelConversationItem[]>([]);
const convLoading = ref(false);
const convTotal = ref(0);
const convPage = ref(1);
const convPageSize = ref(20);
const convFilterThread = ref("");
const convFilterSender = ref("");
const toggleLoadingId = ref<number>(0);

const msgDrawerVisible = ref(false);
const msgConversation = ref<ChannelConversationItem | null>(null);
const conversationMessages = ref<ChannelMessage[]>([]);
const msgLoading = ref(false);

const dialogVisible = ref(false);
const isEdit = ref(false);
const editId = ref(0);
const saving = ref(false);

/**
 * 与 internal/channels 实际读取的 config 键一致（未列出的类型当前不读 config）。
 * @see internal/channels 各适配器
 */
const CONFIG_KEYS_BY_TYPE: Record<ChannelType, readonly string[]> = {
  wecom: ["bot_id", "secret"],
  whatsapp: ["verify_token", "hub_verify_token"],
  telegram: ["bot_token", "token"],
  feishu: ["app_id", "app_secret", "verification_token", "encrypt_key"],
  wechat_kf: [],
  dingtalk: [],
};

const CFG_SHAPE = {
  bot_id: "",
  secret: "",
  verify_token: "",
  bot_token: "",
  app_id: "",
  app_secret: "",
  verification_token: "",
  encrypt_key: "",
} as const;

type CfgKey = keyof typeof CFG_SHAPE;

function emptyCfg(): Record<CfgKey, string> {
  return { ...CFG_SHAPE };
}

const cfg = reactive(emptyCfg());
const configExtrasStr = ref("{}");

function configKeysForType(t: ChannelType): Set<string> {
  return new Set(CONFIG_KEYS_BY_TYPE[t] || []);
}

/** 企微走 WebSocket，业务入站不经 HTTP，一般不需要 Webhook 密钥。 */
const needsWebhookTokenField = computed(() => form.channel_type !== "wecom");

function loadConfigIntoForm(
  raw: Record<string, unknown> | null | undefined,
  channelType: ChannelType,
) {
  const base =
    raw && typeof raw === "object" && !Array.isArray(raw) ? { ...raw } : {};
  const shown = configKeysForType(channelType);
  const extras: Record<string, unknown> = {};
  Object.assign(cfg, emptyCfg());

  for (const [k, v] of Object.entries(base)) {
    if (!shown.has(k)) {
      extras[k] = v;
      continue;
    }
    const s = v === null || v === undefined ? "" : String(v);
    if (channelType === "whatsapp" && k === "hub_verify_token") {
      if (!base.verify_token) {
        cfg.verify_token = s;
      } else {
        extras.hub_verify_token = v;
      }
      continue;
    }
    if (channelType === "telegram" && k === "token") {
      if (!base.bot_token) {
        cfg.bot_token = s;
      } else {
        extras.token = v;
      }
      continue;
    }
    (cfg as Record<string, string>)[k] = s;
  }

  configExtrasStr.value =
    Object.keys(extras).length > 0 ? JSON.stringify(extras, null, 2) : "{}";
}

const typeOptions: { value: ChannelType; label: string }[] = [
  { value: "wecom", label: "企业微信 · 智能机器人" },
  { value: "wechat_kf", label: "微信客服 wechat_kf" },
  { value: "feishu", label: "飞书 feishu" },
  { value: "dingtalk", label: "钉钉 dingtalk" },
  { value: "whatsapp", label: "WhatsApp" },
  { value: "telegram", label: "Telegram" },
];

const form = reactive({
  name: "",
  channel_type: "wecom" as ChannelType,
  enabled: true,
  webhook_token: "",
  description: "",
});

watch(
  () => form.channel_type,
  (t) => {
    if (!dialogVisible.value || isEdit.value) return;
    loadConfigIntoForm({}, t);
  },
);

function typeLabel(t: string) {
  const o = typeOptions.find((x) => x.value === t);
  return o?.label ?? t;
}

function webhookURL(uuid: string) {
  const base = `${window.location.origin}/api/v1/webhooks/channels/${uuid}`;
  return base;
}

function copyText(s: string) {
  navigator.clipboard.writeText(s).then(() => ElMessage.success("已复制"));
}

function formatTime(t: string) {
  if (!t) return "";
  return new Date(t).toLocaleString("zh-CN", { hour12: false });
}

function clearChannelConversationPanel() {
  selectedChannel.value = null;
  channelConversations.value = [];
  convTotal.value = 0;
  convPage.value = 1;
  convFilterThread.value = "";
  convFilterSender.value = "";
}

function openChannelConversations(row: Channel) {
  selectedChannel.value = row;
  convPage.value = 1;
  loadChannelConversations();
}

function refreshChannelConversations() {
  convPage.value = 1;
  loadChannelConversations();
}

async function loadChannelConversations() {
  if (!selectedChannel.value) return;
  convLoading.value = true;
  try {
    const res: any = await channelApi.conversations(selectedChannel.value.id, {
      page: convPage.value,
      page_size: convPageSize.value,
      thread_key: convFilterThread.value || undefined,
      sender_id: convFilterSender.value || undefined,
    });
    channelConversations.value = res.data?.list || [];
    convTotal.value = res.data?.total || 0;
  } finally {
    convLoading.value = false;
  }
}

async function openConversationMessages(row: ChannelConversationItem) {
  if (!selectedChannel.value) return;
  msgConversation.value = row;
  msgDrawerVisible.value = true;
  msgLoading.value = true;
  try {
    const res: any = await channelApi.conversationMessages(
      selectedChannel.value.id,
      row.conversation_id,
      { limit: 100, with_steps: false }
    );
    conversationMessages.value = (res.data || []).filter((m: ChannelMessage) => {
      if (m.role === "user") return true;
      if (m.role === "assistant" && m.content?.trim()) return true;
      return false;
    });
  } catch {
    conversationMessages.value = [];
  } finally {
    msgLoading.value = false;
  }
}

async function deleteChannelConversation(conversationId: number) {
  if (!selectedChannel.value) return;
  try {
    await channelApi.deleteConversation(selectedChannel.value.id, conversationId);
    ElMessage.success("会话已删除");
    if (channelConversations.value.length === 1 && convPage.value > 1) {
      convPage.value -= 1;
    }
    await loadChannelConversations();
  } catch {
    /* error message from interceptor */
  }
}

async function loadData() {
  loading.value = true;
  try {
    const res: any = await channelApi.list({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value,
    });
    list.value = res.data?.list || [];
    total.value = res.data?.total || 0;
  } finally {
    loading.value = false;
  }
}

async function onToggleEnabled(row: Channel, enabled: boolean) {
  if (toggleLoadingId.value) return;
  const prev = row.enabled;
  row.enabled = enabled;
  toggleLoadingId.value = row.id;
  try {
    await channelApi.setEnabled(row.id, enabled);
    ElMessage.success(enabled ? "渠道已启用，监听已刷新" : "渠道已禁用，监听已停止");
    if (selectedChannel.value?.id === row.id) {
      selectedChannel.value.enabled = enabled;
    }
  } catch {
    row.enabled = prev;
  } finally {
    toggleLoadingId.value = 0;
  }
}

function resetForm() {
  form.name = "";
  form.channel_type = "wecom";
  form.enabled = true;
  form.webhook_token = "";
  form.description = "";
  loadConfigIntoForm({}, "wecom");
  editId.value = 0;
}

function openCreate() {
  isEdit.value = false;
  resetForm();
  dialogVisible.value = true;
}

function openEdit(row: Channel) {
  isEdit.value = true;
  editId.value = row.id;
  form.name = row.name;
  form.channel_type = row.channel_type;
  form.enabled = row.enabled;
  form.webhook_token = row.webhook_token || "";
  form.description = row.description || "";
  loadConfigIntoForm(
    (row.config as Record<string, unknown>) || {},
    row.channel_type,
  );
  dialogVisible.value = true;
}

function buildConfigPayload(): Record<string, unknown> | null {
  let extras: Record<string, unknown> = {};
  const rawExtra = configExtrasStr.value.trim();
  if (rawExtra !== "" && rawExtra !== "{}") {
    try {
      const p = JSON.parse(configExtrasStr.value || "{}");
      if (p !== null && typeof p === "object" && !Array.isArray(p)) {
        extras = p as Record<string, unknown>;
      } else {
        ElMessage.error("附加配置须为 JSON 对象");
        return null;
      }
    } catch {
      ElMessage.error("附加配置 JSON 无效");
      return null;
    }
  }

  const out: Record<string, unknown> = { ...extras };
  const t = form.channel_type;
  if (t === "wecom") {
    const bid = cfg.bot_id.trim();
    const sec = cfg.secret.trim();
    if (bid) {
      out.bot_id = bid;
    }
    if (sec) {
      out.secret = sec;
    }
  } else if (t === "whatsapp") {
    const vt = cfg.verify_token.trim();
    if (vt) {
      out.verify_token = vt;
    }
    delete out.hub_verify_token;
  } else if (t === "telegram") {
    const tok = cfg.bot_token.trim();
    if (tok) {
      out.bot_token = tok;
    }
    delete out.token;
  } else if (t === "feishu") {
    const set = (k: keyof typeof CFG_SHAPE) => {
      const v = String(cfg[k] ?? "").trim();
      if (v) {
        out[k] = v;
      }
    };
    set("app_id");
    set("app_secret");
    set("verification_token");
    set("encrypt_key");
  }
  return out;
}

async function submitForm() {
  if (!form.name.trim()) {
    ElMessage.warning("请填写名称");
    return;
  }
  if (form.channel_type === "telegram" && !cfg.bot_token.trim()) {
    ElMessage.warning("Telegram 请填写 Bot Token");
    return;
  }
  if (
    form.channel_type === "wecom" &&
    (!cfg.bot_id.trim() || !cfg.secret.trim())
  ) {
    ElMessage.warning("企业微信请填写 Bot ID 与 Secret");
    return;
  }
  if (
    form.channel_type === "feishu" &&
    (!cfg.app_id.trim() ||
      !cfg.app_secret.trim() ||
      !cfg.verification_token.trim())
  ) {
    ElMessage.warning("飞书请填写 App ID、App Secret 与 Verification Token");
    return;
  }
  const config = buildConfigPayload();
  if (config === null) return;

  saving.value = true;
  try {
    if (isEdit.value) {
      await channelApi.update(editId.value, {
        name: form.name,
        enabled: form.enabled,
        webhook_token: form.webhook_token,
        description: form.description,
        config,
      });
      ElMessage.success("已更新");
    } else {
      await channelApi.create({
        name: form.name,
        channel_type: form.channel_type,
        enabled: form.enabled,
        webhook_token: form.webhook_token,
        description: form.description,
        config,
      });
      ElMessage.success("已创建");
    }
    dialogVisible.value = false;
    await loadData();
  } finally {
    saving.value = false;
  }
}

async function handleDelete(id: number) {
  try {
    await channelApi.delete(id);
    ElMessage.success("已删除");
    if (selectedChannel.value?.id === id) {
      clearChannelConversationPanel();
    }
    loadData();
  } catch {
    /* el message from interceptor */
  }
}

onMounted(() => loadData());
</script>

<style scoped>
.webhook-snippet {
  font-size: 11px;
  word-break: break-all;
  color: var(--el-text-color-secondary);
}
.webhook-muted {
  display: block;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-bottom: 4px;
}
.webhook-secondary {
  display: block;
  margin-top: 2px;
  opacity: 0.8;
}
.status-cell {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.conv-filter-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}
.config-hint {
  margin-bottom: 12px;
}
.hint-lines {
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}
.channel-form :deep(.el-divider) {
  margin: 8px 0 16px;
}
.extras-collapse {
  margin-top: 8px;
}
.extras-tip {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin: 8px 0 0;
}
.mono :deep(.el-textarea__inner) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
}

/* ── 消息记录抽屉 ── */
.msg-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.msg-empty {
  text-align: center;
  color: var(--el-text-color-secondary);
  padding: 48px 0;
  font-size: 13px;
}
.msg-item {
  border-radius: 10px;
  padding: 10px 14px;
  border: 1px solid var(--el-border-color-extra-light);
  background: var(--el-bg-color);
}
.msg-role-user {
  background: var(--el-color-primary-light-9);
  border-color: transparent;
}
.msg-role-assistant {
  background: var(--el-fill-color-lighter);
  border-color: transparent;
}
.msg-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.msg-tokens {
  margin-left: auto;
  font-size: 11px;
  opacity: 0.6;
}
.msg-content {
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--el-text-color-primary);
}
</style>
