# Anvil for VS Code

An open-source AI coding agent that works with your own model API or model
gateway—directly inside VS Code.

[English](#english) · [中文](#中文)

- Source: [github.com/tangxiangjiang/anvil](https://github.com/tangxiangjiang/anvil)
- Author: Xiangjiang Tang ([@tangxiangjiang](https://github.com/tangxiangjiang))
- License: [MIT](https://github.com/tangxiangjiang/anvil/blob/master/LICENSE)

## Product tour / 产品预览

![Anvil streaming chat, plan and diagram workflow](https://raw.githubusercontent.com/tangxiangjiang/anvil/master/editors/vscode/media/screenshots/hero-chat.png)

*One editor tab for conversation, plans, tool activity and rich results.*<br>
*在一个编辑器 Tab 中完成对话、计划、工具操作和富文本结果展示。*

![Anvil model settings in Single API mode](https://raw.githubusercontent.com/tangxiangjiang/anvil/master/editors/vscode/media/screenshots/model-settings-single.png)

*Single API mode uses one explicit Model ID.*<br>
*单 API 模式使用一个明确的 Model ID。*

![Anvil model settings in Gateway mode](https://raw.githubusercontent.com/tangxiangjiang/anvil/master/editors/vscode/media/screenshots/model-settings-gateway.png)

*Gateway mode opens the curated multi-model workflow.*<br>
*网关模式进入多模型管理流程。*

![Anvil sessions and Agent Map](https://raw.githubusercontent.com/tangxiangjiang/anvil/master/editors/vscode/media/screenshots/sessions-agent-map.png)

*Resume stored sessions and inspect the active agent tree.*<br>
*恢复历史会话，并查看当前 Agent 协作关系。*

---

## English

### Get started in four steps

#### 1. Open a trusted folder

Install Anvil, reload VS Code, and open the project folder you want the agent
to work in. Anvil does not run tools in an untrusted workspace.

#### 2. Save your API key

Open the Command Palette with <kbd>Ctrl/Cmd</kbd>+<kbd>Shift</kbd>+<kbd>P</kbd>,
run **Anvil: Set API Key**, and paste the key issued by your API provider or
gateway.

The key is stored in VS Code SecretStorage. It is not written to
`settings.json`, the transcript, or extension logs. You can alternatively
start VS Code with `ANVIL_API_KEY` in its environment.

#### 3. Configure models

Run **Anvil: Configure Models**, choose a provider, and enter the
required Base URL. Then select one connection mode:

**Single API**

- Use this when the endpoint serves one known model.
- Enter the exact **Model ID** expected by the API.
- That Model ID becomes the only model offered in chat.

**Gateway**

- Use this when the endpoint routes requests to multiple models.
- Select **Open Model Manager**.
- Enter the gateway's exact **Model List API** URL, fetch the list, add the
  models your workspace may use, and select a default.

Existing Anvil workspaces use Gateway mode so their curated model lists remain
available.

#### 4. Start coding

Run **Anvil: Open Chat** or select **New session** in the Anvil activity-bar
view. Choose a model and permission mode, describe the task, and review any
operation that needs approval.

### Everyday workflow

1. Ask Anvil to explain, investigate, plan, or implement a change.
2. Follow streamed reasoning, tool activity, edits, and command output in one
   conversation.
3. Approve sensitive operations when prompted.
4. Review file diffs and the final summary.
5. Reopen, rename, archive, fork, or rewind the session when needed.

### Advanced features

- **Plans** — collapsible task plans, plan history, and approve-to-run flow.
- **Sessions** — one titled editor tab per conversation, search, archive,
  restore, fork, rewind, and JSONL persistence.
- **Model control** — per-conversation model and reasoning-effort selection,
  Single API mode, and gateway model curation.
- **Tool review** — readable tool calls, command output, file links, per-hunk
  diff review, checkpoints, and explicit approvals.
- **Permissions** — workspace and user rules, permission modes, protected
  paths, and a visual rule manager.
- **Agent workflows** — subagents, Agent Map, background tasks, and declarative
  workflows.
- **Context management** — live token usage, context-window status,
  compaction, cached-token accounting, and conversation export.
- **Developer tools** — MCP servers, web search, language servers, test/build
  commands, project rules, and editor context.
- **Rich chat** — images, Markdown, syntax highlighting, Mermaid diagrams,
  formulas, quotes, and full tool output in editor tabs.

### Useful commands

- **Anvil: Open Chat**
- **Anvil: Configure Models**
- **Anvil: Open Model Manager**
- **Anvil: Set API Key**
- **Anvil: Clear API Key**
- **Anvil: Open Permission Rules**
- **Anvil: Open MCP Servers**
- **Anvil: Open Tasks**

### Troubleshooting

- **A session will not start:** run **Anvil: Configure Models** and
  check the Provider, Base URL, connection mode, and effective Model ID.
- **Authentication fails:** run **Anvil: Set API Key** again and verify that
  the key belongs to the configured endpoint.
- **A gateway shows no models:** confirm that Model List API is the exact model
  listing endpoint, then fetch it again in Model Manager.
- **Anvil refuses the workspace:** trust the folder through
  **Workspaces: Manage Workspace Trust**.

---

## 中文

### 四步快速开始

#### 1. 打开受信任的项目文件夹

安装 Anvil 后重新加载 VS Code，并打开需要 Agent 操作的项目文件夹。Anvil
不会在未受信任的工作区中运行工具。

#### 2. 配置 API Key

按 <kbd>Ctrl/Cmd</kbd>+<kbd>Shift</kbd>+<kbd>P</kbd> 打开命令面板，执行
**Anvil: Set API Key**，然后粘贴 API 服务商或网关提供的密钥。

密钥保存在 VS Code SecretStorage 中，不会写入 `settings.json`、聊天记录或
扩展日志。也可以通过启动 VS Code 时设置环境变量 `ANVIL_API_KEY` 来提供密钥。

#### 3. 配置模型连接

执行 **Anvil: Configure Models**，选择 Provider，并填写必填的
Base URL，然后选择一种连接模式：

**单 API 模式（Single API）**

- 适合只提供一个已知模型的 API。
- 填写 API 要求的准确 **Model ID**。
- 聊天框中只会提供这个模型。

**网关模式（Gateway）**

- 适合可路由到多个模型的统一网关。
- 点击 **Open Model Manager**。
- 填写网关准确的 **Model List API** 地址，拉取模型列表，将允许使用的模型
  加入工作区列表，并选择默认模型。

已有 Anvil 工作区默认使用网关模式，原有的模型列表会继续保留。

#### 4. 开始使用

执行 **Anvil: Open Chat**，或者在侧边栏 Anvil 视图中选择
**New session**。选择模型和权限模式，描述任务，然后审核需要授权的操作。

### 日常使用流程

1. 让 Anvil 解释代码、调查问题、制定计划或直接实现修改。
2. 在同一个会话中查看流式回复、工具操作、代码修改和命令输出。
3. 对敏感操作进行授权确认。
4. 审核文件 Diff 和最终总结。
5. 随时重新打开、重命名、归档、分叉或回退会话。

### 高级功能

- **计划管理**：可折叠任务计划、Plan 历史，以及审批后执行。
- **会话管理**：一个会话对应一个命名 Tab，支持搜索、归档、恢复、分叉、
  回退和 JSONL 持久化。
- **模型控制**：每个会话独立选择模型和推理强度，支持单 API 和网关模型管理。
- **工具审核**：清晰展示工具调用、命令输出和文件链接，支持分块 Diff、
  Checkpoint 和显式授权。
- **权限系统**：工作区与用户规则、权限模式、受保护路径和可视化规则管理。
- **Agent 工作流**：Subagent、Agent Map、后台任务和声明式工作流。
- **上下文管理**：实时 Token 用量、上下文窗口状态、自动压缩、缓存 Token
  统计和会话导出。
- **开发工具**：MCP、Web Search、语言服务器、测试/构建命令、项目规则和
  编辑器上下文。
- **富文本聊天**：图片、Markdown、代码高亮、Mermaid、公式、引用，以及在
  编辑器 Tab 中打开完整工具输出。

### 常用命令

- **Anvil: Open Chat**：打开聊天
- **Anvil: Configure Models**：配置模型
- **Anvil: Open Model Manager**：管理网关模型
- **Anvil: Set API Key**：保存 API Key
- **Anvil: Clear API Key**：清除 API Key
- **Anvil: Open Permission Rules**：管理权限规则
- **Anvil: Open MCP Servers**：管理 MCP
- **Anvil: Open Tasks**：查看后台任务

### 常见问题

- **会话无法启动**：执行 **Anvil: Configure Models**，检查
  Provider、Base URL、连接模式和实际使用的 Model ID。
- **认证失败**：重新执行 **Anvil: Set API Key**，并确认密钥属于当前配置的
  API 地址。
- **网关没有模型**：确认 Model List API 是准确的模型列表接口，然后在
  Model Manager 中重新拉取。
- **工作区被拒绝**：通过 **Workspaces: Manage Workspace Trust** 信任文件夹。

---

## Open source

Anvil is developed in the open at
[github.com/tangxiangjiang/anvil](https://github.com/tangxiangjiang/anvil).
Issues and contributions are welcome.

Created and maintained by Xiangjiang Tang
([@tangxiangjiang](https://github.com/tangxiangjiang)).

Developer setup, packaging, architecture, and test documentation:
[DEVELOPMENT.md](https://github.com/tangxiangjiang/anvil/blob/master/editors/vscode/DEVELOPMENT.md).
