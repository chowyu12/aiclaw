# Agent Harness Runtime 设计

## 目标

Harness Runtime 负责把 Agent 执行从“模型说完成”推进到“执行层基于契约和证据放行”。

它参考 `ickey-claw` 的 Harness 思路，但适配 AiClaw 当前架构：

- 主循环仍由 `internal/agent/harness.go` 管理。
- 稳定协议和校验原语放在独立包 `pkg/harness`。
- Agent 内部由 `harnessVerifierLayer` 负责校验/纠偏编排，并通过 `internal/agent/harness_verifier.go` 把运行态转换为 harness 输入。
- 不新增数据库表，校验和纠偏记录复用 `execution_steps.metadata.harness`。

## 核心模型

```text
Contract -> Evidence -> Validate -> Correct
```

| 模型 | 代码 | 说明 |
| --- | --- | --- |
| Contract | `pkg/harness.TaskContract` | 从用户目标、计划状态、文件意图、工具证据需求推导执行契约。 |
| Evidence | `pkg/harness.EvidenceLedger` | 记录调用过的业务工具、工具事件、文件产物、计划快照、校验事件和纠偏事件。 |
| Validate | `pkg/harness.Validator` | 在工具前、工具后、最终答复前、保存前执行校验。 |
| Correct | `pkg/harness.CorrectionState` | 校验失败时生成结构化纠偏提示；超过次数后失败收口。 |

## 执行流程

```text
prepare
  -> bootstrap context
  -> for each round
       -> compile model request
       -> call model
       -> if tool_calls
            -> StagePreTool for each tool
            -> execute tools
            -> record ToolEvent/File evidence
            -> control plane advances Plan State
            -> verifier runs StagePostTool
            -> correction prompt if needed
          else
            -> verifier runs StagePreFinal
            -> correction prompt if needed
            -> finish when allowed
  -> render attachment links
  -> verifier runs StagePreSave
  -> save assistant message and trace
```

## 校验阶段

| Stage | 调用点 | 行为 |
| --- | --- | --- |
| `pre_tool` | `runOneToolCall` | 校验工具策略；默认无 allowed/blocked 策略时不拦截。 |
| `post_tool` | 工具轮执行后 | 工具失败先作为 warning 进入证据；最终答复前仍需成功证据或明确阻塞原因。 |
| `pre_final` | 模型无 tool_calls 时 | 阻止空答复、纯进度说明、未完成计划、缺少必要证据或缺少声明的文件产物。 |
| `pre_save` | `saveResult` 里附件链接渲染后 | 复核最终内容，确保文件交付类答复包含可引用的文件链接或文件标识。 |

## 证据来源

AiClaw verifier 当前采集这些证据：

- `calledTools`: 去掉 `plan` 和 `tool_search` 后的业务工具集合。
- `ToolEvent`: tool call id、工具名、参数摘要、输出摘要、状态、错误、耗时、文件。
- `Artifacts`: 持久化到 `model.File` 的工具产物，使用 `uuid/storage_path/filename/mime/size` 描述。
- `PlanSnapshot`: 从 `PlanManager.activeState` 转换为轻量快照。
- `ValidationEvents` 和 `CorrectionEvents`: 用于后续 trace 和调试。

## 终态语义

Plan item 的 harness 终态与 AiClaw Plan State 保持一致：

- `completed`
- `skipped`
- `blocked`
- `failed`

`pending`、`running`、`in_progress` 不是终态。最后一个开放项如果是“交付最终答复”类任务，可以允许模型进入最终答复 gate。

## 可观测性

校验或纠偏出现 violation 时记录 `step_type=harness`：

- `validate_pre_tool`
- `validate_post_tool`
- `validate_pre_final`
- `validate_pre_save`
- `correct_pre_final`
- `correct_post_tool`

`metadata.harness` 包含：

- stage
- allowed
- violation_codes
- required_actions
- correction attempt/outcome
- evidence summary: execution tools, tool event count, artifact count, plan terminal

成功且没有 violation 的校验不会落 step，避免执行时间线过噪。

## 设计边界

- Harness Runtime 不替代主执行循环；它只做阶段校验和纠偏决策。
- `harnessControlPlaneLayer` 只负责预算、计划推进、失败收口和保存；`harnessVerifierLayer` 负责 Contract/Evidence/Validate/Correct。
- 当前没有迁移 `ickey-claw` 的 response_format 二次整理器；`pkg/harness` 只保留 JSON 校验能力，等待 AiClaw 有明确结构化输出配置后再接入。
- 文件证据按 AiClaw 的 `model.File` 语义校验，接受 `uuid/storage_path/filename`，不强制要求公网 URL。
- 进度型答复识别是启发式规则，可能对少数“用户询问计划/下一步”的自然语言回答偏严格；如果后续误判频繁，应把该 validator 限制到需要证据的任务。
- `post_tool` 阶段的工具失败目前是 warning，不直接中断；真正的阻断发生在 `pre_final`，除非后续补到了成功证据或最终答复明确说明无法完成。

## 本轮 Review 修正

- 将 `failed` 纳入 Plan terminal 状态，和现有 PlanManager / 文档状态机一致。
- 最后一轮才触发纠偏时，`correct_*` step 记录为 failed，避免没有下一轮可执行时仍显示 correction success。
- 对 evidence 中的 execution tools 排序，保证 trace 稳定。
- 将临时 bridge 收敛为 `harnessVerifierLayer`：校验状态集中在 `harnessTurnState.verifier`，control 不再混入校验/纠偏职责。
