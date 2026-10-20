# 应用配置与接管

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。`config-status` / `config-dry-run` / `config-sync` 检查、预览或部署 `[dotfiles]` 映射；全局工具、Shell 和 PATH 的说明见[开发环境](development-environment.md)。

## 公共配置与平台适配器


`dotfiles/` 已重构为公共模块和两个平台适配器：

- `dotfiles/common/`：已启用的 espanso、工具版本、uv、zsh、ChatGPT 和 VS Code 通用配置，以及共用的 Android SDK / Toolbox / PATH 声明；
- `dotfiles/macos/`：espanso 的 macOS 专用片段、Rust 平台参数、locale 和 Homebrew 登录初始化；
- `dotfiles/linux/`：Linux 登录初始化、可选 Linuxbrew 和 locale 候选；
- `mise.macos.toml` 与 `mise.linux.toml`：相同 `config-*` 命令背后的两个平台适配器。

仓库配置和已部署的全局 mise 配置都通过 `auto_env` 自动选择 `macos` 或 `linux`。公共模块不需要知道当前平台，平台路径也不会散落在公共调用入口中。

全局 `~/.config/mise/miserc.toml` 使用 `template` 模式部署，以规避 mise 2026.10.15 在 dotfiles dry-run 中将该文件按普通 `mise.toml` 解析、误报未知字段的问题。`auto_env` 和 `env_conf_d` 保留在这个文件中，符合[官方 miserc 配置规则](https://mise.jdx.dev/configuration.html#miserc)。修改来源后运行 `mise run config-sync`；项目的 `config-dry-run` 已改用原生 `diff` 比较真实差异，模板无变化时不会再显示条件性的 `render … (if changed)`。

目前启用 espanso、全局 mise 配置、macOS Rust 参数、zsh、ChatGPT 和 VS Code 的映射。工具版本、uv 环境、SDK 路径、可选私有文件和输出脱敏已启用；zsh 在激活 mise 前加载共用的用户 PATH，macOS locale 由 mise 平台配置提供。`.miserc.toml` 只控制配置发现。

## Espanso（已启用）

已将 `Snippets 2026-09-11 10.19.05.json` 中的 18 条片段转换为 Espanso YAML。触发词统一使用英文冒号 `:` 前缀和全小写，以原 `name` 作为搜索标签，展开内容保持不变：

- `dotfiles/common/espanso/match/snippets.yml`：12 条公共片段。
- `dotfiles/macos/espanso/match/macos.yml`：4 条 macOS 菜单栏间距/内边距命令，不部署到 Linux。
- `dotfiles/common/espanso/private.local/match/private-snippets.yml`：2 条含调试口令的片段，文件权限为 `0600`，所在 `private.local` 目录被 Git 忽略。通过同样被忽略的根目录 `mise.local.toml` 单独映射。来源和目标都保留 `.yml` 扩展名，避免 Espanso 在解析链接来源时跳过文件。

这些命令片段只展开为文字，导入过程和触发片段本身都不会通过 Espanso shell 扩展执行它们。输入到终端后按回车仍会执行，请先核对内容。

所有受管触发词均不设置 `left_word`、`right_word` 或 `word`，与 `:date` 一致，不依赖前面的空格或换行，也不需要输入后再按空格。用冒号前缀区分普通文字，例如 `:yst` 会展开，而 `system` 不会误触发。旧的无前缀触发词不再保留；冒号使用英文半角 `:`，不是中文全角 `：`。

触发词变更对照：

| 原触发词 | 新触发词 |
| --- | --- |
| `getrnip` | `:getrnip` |
| `topAct` | `:act` |
| `clsdsc` | `:clsdsc` |
| `coli` | `:coli` |
| `topactcn` | `:topactcn` |
| `adbRNFix` | `:adbrnfix` |
| `yst` | `:yst` |
| `setDebugApp` | `:setdebugapp` |
| `clearDebugApp` | `:cleardebugapp` |
| `Tbs` | `:tbs` |
| `Tbp` | `:tbp` |
| `RstTbs` | `:rsttbs` |
| `RstTbp` | `:rsttbp` |
| `pdkit`（本机私有） | `:pdkit` |
| `ddkit`（本机私有） | `:ddkit` |

`:date`、`:cursor`、`:rnlang` 及默认示例 `:espanso`、`:shell` 已符合格式，保持不变。新增触发词时应检查重复和前缀冲突：例如已有 `:act` 时不要再添加 `:actcn`，否则输入到 `:act` 就会提前展开；国区片段因此保留为 `:topactcn`。

原占位符已转换为 Espanso 原生语法：`{date}` → 本地日期 `YYYY-MM-DD`，`{datetime}` → 本地日期时间 `YYYY-MM-DD HH:mm:ss`，`{cursor}` → 光标标记 `$|$`。因此 `:date` 输出日期时间，`:clsdsc` 中的日期动态生成；单独的 `:cursor` 会移除触发词并将光标留在原位。默认 `base.yml` 中重复的 `:date` 示例已移除，`:espanso` 和 `:shell` 仍保留。详见 [Espanso 日期扩展](https://espanso.org/docs/matches/extensions/#date-extension)和[光标标记](https://espanso.org/docs/matches/basics/#cursor-hints)。

原有 `config/default.yml` 的配置选项保持不变；与处理过日期冲突的 `match/base.yml` 一起交给 mise。公共目录使用 `symlink-each`，只接管来源中列出的文件，保留本机的其他片段和 `match/packages/`。公共目录映射明确排除 `*.local` 与 `*.example.toml`，私有文件由本机映射单独链接。

目标位置：macOS 为 `~/Library/Application Support/espanso`，Linux 为 `~/.config/espanso`。如果使用自定义 `XDG_CONFIG_HOME` 或 `ESPANSO_CONFIG_DIR`，先运行 `espanso path`，再将平台适配器和私有映射的目标改成实际路径。跨系统配置目录的依据见 [Espanso 官方入门文档](https://espanso.org/docs/get-started/#configuration)。

后续编辑仓库中的 YAML，已建立的链接会直接指向修改后的内容：

```bash
mise run config-status
mise run config-dry-run
mise run config-sync
espanso match list --only-triggers

# 如果修改链接来源后没有自动重载，可手动重启
espanso service restart
```

首次接管已有普通文件时，mise 会报告冲突。先备份 Espanso 配置并检查 `mise bootstrap dotfiles apply --dry-run --force`；确实需要替换时，只对 `espanso path` 对应的目标执行 `apply --force`，不要顺带强制覆盖其他应用。此次迁移的原文件备份位于 `.system-sync/history/*-espanso-*/`。mise 的链接/冲突行为见[官方 dotfiles 文档](https://mise.jdx.dev/dotfiles.html)。

新的机器只从 Git 取得 16 条非私密片段（Linux 使用其中 12 条），没有私有来源也不会影响公共配置部署。需要私有片段时，通过安全渠道复制 `private.local/match/private-snippets.yml` 并设置 `chmod 600`，再将 `dotfiles/common/espanso/mise.local.example.toml` **合并**到仓库根 `mise.local.toml`。不要把原始 JSON 或含口令的 YAML 加入 Git；`0600` 和 Git 忽略都不是加密，也不能代替私有备份。

当前 Mac 已安装的 `espanso` cask 已加入 Brewfile，仅记录声明，本次未安装或升级软件。其他 Linux 机器仍需按发行版准备 Espanso；这里的配置部署不安装应用，也不授予 macOS 辅助功能权限。若原片段管理器仍在运行且启用了同名关键词，请手动停用重叠片段，避免两边同时展开。


## ChatGPT（已启用）

2026-10-20 核对的本机应用为 `/Applications/ChatGPT.app`，版本 `26.1007.71326`，bundle id 为 `com.openai.codex`。它使用 `~/.codex` 配置目录，与 Codex CLI / IDE 扩展共享用户配置，见 [OpenAI 官方配置说明](https://learn.chatgpt.com/docs/config-file/config-basic)。

| 目标 | 来源 | 管理方式 |
|---|---|---|
| `~/.codex/config.toml` | `dotfiles/common/chatgpt/config.toml` | `merge = true`，只管理来源中的键 |
| `~/.codex/AGENTS.md` | `dotfiles/common/chatgpt/AGENTS.md` | `copy`，保留可由应用编辑的普通文件 |
| `~/.codex/keybindings.json` | `dotfiles/common/chatgpt/keybindings.json` | `copy`，导入当前快捷键 |

共享偏好包括当前模型、推理强度、服务层级、两项功能开关，以及对话详情、队列、语音屏幕上下文和外观设置。全局协作规则与当前禁用头像快捷键的配置按原内容导入。首次接管保持本机有效设置不变。

`config.toml` 中的私有环境变量、通知命令、Hook、MCP、项目路径与信任、插件及技能安装状态、引导状态和头像标识继续留在本机。认证文件、会话、数据库、浏览器数据和 macOS 应用状态也不纳入映射。这里使用 mise 原生[按键合并](https://mise.jdx.dev/dotfiles/edits.html#set-keys-in-a-config-file)，没有增加应用不支持的 `config.local.toml`。

## VS Code（已启用）

本机 VS Code `1.142.0` 使用默认 User 目录，现有 18 项设置中有 15 项通用偏好。已按本机值导入 `dotfiles/common/vscode/settings.json`，包括原候选缺少的 `workbench.tree.indent = 16`。两个平台从同一个公共来源合并：

| 平台 | 目标 |
|---|---|
| macOS | `~/Library/Application Support/Code/User/settings.json` |
| Linux | `~/.config/Code/User/settings.json` |

本机的 `dart.flutterSdkPath`、`git.path`、插件专属偏好和以后新增的未声明设置保留在目标文件中。旧的整份覆盖模板已由公共合并来源替代。Profiles、工作区配置、凭据、`argv.json` 和全局存储继续由本机 VS Code 管理；现有 Settings Sync 配置保持原状。VS Code 的目录与配置作用域见[官方说明](https://code.visualstudio.com/docs/configure/settings)。

### 公共扩展清单

`dotfiles/common/vscode/extensions.toml` 固定筛选后的 18 个扩展及核对时版本。当前均为公共项；以后有平台专属扩展时放入 `[macos]` 或 `[linux]`，脚本合并当前平台分组，冲突或格式不明时停止。认证状态和扩展存储不进入清单，此前移除的扩展不重新纳管；清单外扩展继续保留。

```bash
mise run vscode-status
mise run vscode-dry-run
mise run vscode-sync
```

同步只安装缺失项或调整清单内版本，不删除其他扩展；明确同步会按固定版本安装，可能将更新的版本调回清单版本。需要升级时先修改并审阅清单。扩展依赖由 VS Code 安装，若依赖也是清单项，会在最后核对其版本。Settings Sync 的扩展同步若已启用，仍可能与本地固定版本相互覆盖，出现版本漂移时先选择保留的版本再同步。

大型语言扩展可能超过 100 MB，单次 CLI 允许一小时；超时会停止本次 CLI 和子进程，保留已经安装的扩展，重试时先核对已完成项。可复用 VS Code 自身保存的同版本官方 VSIX 缓存。

隔离验收同时指定用户数据和扩展目录，避免使用本机 Profiles、认证和已有扩展；单独传一个目录会被拒绝：

```bash
python3 scripts/vscode-extensions.py sync \
  --user-data-dir /tmp/system-sync-code/user-data \
  --extensions-dir /tmp/system-sync-code/extensions
```

macOS 可自动找到标准应用目录中的 CLI；其他位置用 `--code <CLI绝对路径>`。参数依据 [VS Code 官方 CLI 文档](https://code.visualstudio.com/docs/configure/command-line#_working-with-extensions)。扩展清单是独立入口，`config-sync` 只同步应用配置，不自动安装扩展。

当前 `settings.json` 是标准 JSON，符合 mise 合并器要求。VS Code 允许的 JSONC 注释不受当前 mise 合并器支持；文件包含注释、尾逗号或无效内容时，mise 会报错并保留原文件，不会自动剥离注释再覆盖。

## 应用配置的日常同步

编辑上述公共来源后，运行 `mise run config-status`、`mise run config-dry-run`，确认后运行 `mise run config-sync`。`/shared` 是 mise 的编辑条目标识，不是实际目录。应用界面修改的共享值可与来源产生差异；私有键的变化不会被视为共享配置漂移。

`config-dry-run` 通过 [config-preview.sh](../scripts/config-preview.sh) 调用 `mise bootstrap dotfiles diff`，显示内容、权限、链接结构等真实差异。没有差异时明确输出 `[+] 未发现需要应用的配置差异，无需同步。`；查询失败则返回非零并提示检查失败，不把失败当作无变化。预览不执行 `apply`，也不运行部署钩子；原生 `diff` 会渲染受信任模板，因此模板中的 `exec()` 仍可能运行，见[官方模板说明](https://mise.jdx.dev/dotfiles/managed.html#templates)。

只查看一个目标时可传入路径，例如：

```bash
mise run config-dry-run ~/.config/mise/miserc.toml
```

合并会递归设置来源拥有的键；删除来源中的键只会停止管理它，目标中的原值保留。取消管理也保留合并过的键。含私有项的目标和差异输出只在本机查看，不将整个 `~/.codex`、VS Code User 目录或其完整文件加入 Git。当前 Mac 的原文件、路径及权限记录保存在 `.system-sync/history/*-app-configs/`，权限受限且被 Git 忽略。恢复时取消相应映射，再从该目录的 `home.before` 恢复文件。

隔离测试覆盖本机私有值、应用新增键、重复同步和 JSONC 拒绝写入；当前 Mac 对比接管前后的完整配置，确认设置值未变。Linux 使用同一公共来源，尚未在真实 Linux 主机验证。


## 后续逐项接管其他配置

1. 审阅待接管的配置以及当前平台差异；全局 mise 工具配置已经启用。
2. 在根 `mise.toml` 中启用所需公共映射，再在当前平台的 `mise.macos.toml` 或 `mise.linux.toml` 中启用对应映射。
3. 运行 `mise run config-status` 和 `mise run config-dry-run`，确认目标和来源都属于当前平台。
4. 处理已有真实文件与 symlink 的首次接管冲突后，再运行 `mise run config-sync`。
5. zsh 已接管并启用 mise Shell 激活；接管其他应用时继续保留这一个交互激活入口，不重复初始化旧管理器。

从 `[dotfiles]` 删除条目不会自动删除已经应用的目标。取消管理时先执行 `mise bootstrap dotfiles unapply --dry-run`，确认后执行 `mise bootstrap dotfiles unapply`，再删除映射。


## 参考

- [mise Dotfiles](https://mise.jdx.dev/dotfiles.html)
- [mise config trust](https://mise.jdx.dev/cli/trust.html)
