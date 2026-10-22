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

原有 `config/default.yml` 的配置选项保持不变；与处理过日期冲突的 `match/base.yml` 一起交给 mise。公共目录使用 `symlink-each`，只接管来源中列出的文件，保留本机的其他片段和 `match/packages/`。公共目录映射明确排除 `*.local`、`*.example.toml` 和 `.DS_Store`，私有文件由本机映射单独链接。

`.gitignore` 不会自动成为目录映射的排除清单。2026-10-21 核对发现，YAML 和私有映射均已正确部署，待同步项实际来自 Finder 在公共来源中生成的 `.DS_Store`。两个平台已显式排除此名称，覆盖根目录及子目录；目标中的本机元数据文件继续保留，不需要强制覆盖或重启 Espanso。无 `/` 的 `exclude` 模式会匹配任意深度的文件名，规则见 [mise 官方说明](https://mise.jdx.dev/dotfiles/managed.html#exclude-files)。

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

`dotfiles/common/vscode/extensions.toml` 当前固定 19 个公共扩展及核对或采集时的版本，包含本次确认纳管的 `anthropic.claude-code@2.1.304`。当前均位于 `[common]`，macOS 和 Linux 共用；平台专属扩展放入 `[macos]` 或 `[linux]`，脚本合并当前平台分组，冲突或格式不明时停止。认证状态和扩展存储不进入清单；新增纳管需显式确认，清单外扩展继续保留。

```bash
mise run vscode-status
mise run vscode-sync
```

`vscode-status` 已同时列出安装和版本变更计划，旧名称 `vscode-dry-run` 是它的兼容别名；无需连续运行两个查询。

`vscode-sync` 显示当前安装扩展的编号、标识和版本，并在最后复查版本。安装命令的原始输出继续按现有规则捕获和过滤；每 10 秒显示仍在执行及已等待时间，避免长时间下载没有反馈。新增状态提示只包含阶段、扩展标识、版本和等待时间，不打印原始安装诊断。

清单外扩展会按扩展标识排序，逐项显示 `publisher.name@已安装版本`，保留数量汇总。即使全部公共扩展已符合清单，也会列出这些本机扩展；`vscode-sync` 和兼容预览使用同样的明细，不自动卸载清单外扩展。输出示例：

```text
[~] 保留 1 个清单外扩展：
  - example.local@1.2.3
[+] 19 个公共扩展均符合清单，无需同步。
```

同步只安装缺失项或调整清单内版本，不删除其他扩展；明确同步会按固定版本安装，可能将更新的版本调回清单版本。需要升级时先修改并审阅清单。扩展依赖由 VS Code 安装，若依赖也是清单项，会在最后核对其版本。Settings Sync 的扩展同步若已启用，仍可能与本地固定版本相互覆盖，出现版本漂移时先选择保留的版本再同步。

大型语言扩展可能超过 100 MB，单次 CLI 允许一小时；超时会停止本次 CLI 和子进程，保留已经安装的扩展，重试时先核对已完成项。可复用 VS Code 自身保存的同版本官方 VSIX 缓存。

隔离验收同时指定用户数据和扩展目录，避免使用本机 Profiles、认证和已有扩展；单独传一个目录会被拒绝：

```bash
./helper/scripts/run.sh vscode sync \
  --user-data-dir /tmp/system-sync-code/user-data \
  --extensions-dir /tmp/system-sync-code/extensions
```

macOS 可自动找到标准应用目录中的 CLI；其他位置用 `--code <CLI绝对路径>`。参数依据 [VS Code 官方 CLI 文档](https://code.visualstudio.com/docs/configure/command-line#_working-with-extensions)。扩展清单是独立入口，`config-sync` 只同步应用配置，不自动安装扩展。

当前 `settings.json` 是标准 JSON，符合 mise 合并器要求。VS Code 允许的 JSONC 注释不受当前 mise 合并器支持；文件包含注释、尾逗号或无效内容时，mise 会报错并保留原文件，不会自动剥离注释再覆盖。

### 采集本机未纳管扩展

扩展采集合并在 `vscode-status` task，通过 `--capture` 开启，不另设 `vscode-capture` task。它比较 VS Code 的实际安装列表与当前平台清单，按本机已安装版本生成追加候选，列出待纳管及被筛除扩展的标识、版本和清单 diff。默认只读预览；已有声明、固定版本和注释保持，缺失的旧声明也不会被删除。

```bash
mise run vscode-status --capture
mise run vscode-status --capture --exclude example.local
mise run vscode-status --capture --only example.shared,example.formatter --exclude example.local

# 预览通过后，用相同筛选参数追加；交互输入 yes 确认
mise run vscode-status --capture --only example.formatter --apply

# 自动化显式跳过确认，仍会显示候选并复查后写入
mise run vscode-status --capture --only example.formatter --apply --yes

# 平台专属扩展：在对应系统上显式选择分组，预览通过后再加 --apply
mise run vscode-status --capture --only example.platform --group macos
# Linux 使用 --group linux
```

默认写入 `[common]`，macOS 和 Linux 共用；显式指定 `--group common` 效果相同。平台专属扩展在 macOS 上用 `--group macos`、Linux 上用 `--group linux`。不允许从本机写入另一平台分组；公共声明与任一平台版本冲突时停止，交由人工处理。采集只追加未纳管项，不自动移动已有声明的分组，调整范围需编辑清单。

`--only` 和 `--exclude` 可重复或用逗号分隔，使用准确的 `publisher.name`，不接受通配符或版本号；排除优先于选中，不移除已有声明。`--only` 中未安装的扩展会报错，排除未安装扩展则允许。筛选只影响本次采集，不保存为永久排除规则，后续采集仍需传入相同参数。

与 `package-capture --apply` 只保存本机候选不同，`vscode-status --capture --apply` 会在确认后直接追加受管的 `dotfiles/common/vscode/extensions.toml`。原清单与候选保存在 Git 忽略的 `.system-sync/capture/<时间>-vscode-<平台>-*/extensions.toml.before` 和 `extensions.toml.candidate`，目录 `0700`、备份文件 `0600`；清单原权限保持。写入取得共享操作锁，复查文件身份、内容、权限及待追加扩展的实际版本，再原子替换清单；检查失败保留原文件。重复执行没有新候选时明确提示无需修改，不创建备份。

不加 `--capture` 时仍按原方式只读检查状态，不能使用采集筛选或写入参数。采集不安装、卸载或更新扩展，不读取认证或扩展存储。排除的扩展继续留在本机，`vscode-status` 仍列为清单外扩展；采集成功后，其他机器可通过 `vscode-sync` 按新增声明安装。采集中的 `--dry-run` 等同默认预览，不能与 `--apply` 同用；`--yes` 必须与 `--apply` 同用。`--manifest`、`--code` 及成对的隔离目录参数与其他扩展命令共用，便于验收。

## 应用配置的日常同步

`config-sync` 显示状态查询、备份、应用和复查阶段；耗时查询、备份每 10 秒报告仍在执行。`machine-init` 的配置阶段复用相同能力。`config-detach` 确认后显示正在备份和迁出；`config-save` 显示本机检查点保存状态，长时间保存同样有等待提示，新增提示不包含描述或私有正文。预览和结构化查询保持原格式。

编辑上述公共来源后，运行 `mise run config-status`、`mise run config-dry-run`，确认后运行 `mise run config-sync`。`/shared` 是 mise 的编辑条目标识，不是实际目录。应用界面修改的共享值可与来源产生差异；私有键的变化不会被视为共享配置漂移。

`config-dry-run` 通过兼容脚本 [config-preview.sh](../scripts/config-preview.sh) 进入 Go helper 的 `config diff`，与 `config-sync`、`machine-init --only config` 共用 [configops](../helper/internal/configops/) 模块；由 mise 原生 `status --json` 和 `diff` 读取状态、渲染差异，显示内容、权限、链接结构等真实差异。没有差异时明确输出 `[+] 未发现需要应用的配置差异，无需同步。`；查询失败则返回非零并提示检查失败，不把失败当作无变化。预览不执行 `apply`，也不运行部署钩子；原生 `diff` 会渲染受信任模板，因此模板中的 `exec()` 仍可能运行，见[官方模板说明](https://mise.jdx.dev/dotfiles/managed.html#templates)。

只查看一个目标时可传入路径，例如：

```bash
mise run config-dry-run ~/.config/mise/miserc.toml
```

合并会递归设置来源拥有的键；删除来源中的键只会停止管理它，目标中的原值保留。取消管理也保留合并过的键。含私有项的目标和差异输出只在本机查看，不将整个 `~/.codex`、VS Code User 目录或其完整文件加入 Git。当前 Mac 的原文件、路径及权限记录保存在 `.system-sync/history/*-app-configs/`，权限受限且被 Git 忽略。恢复时取消相应映射，再从该目录的 `home.before` 恢复文件。

隔离测试覆盖本机私有值、应用新增键、重复同步和 JSONC 拒绝写入；当前 Mac 对比接管前后的完整配置，确认设置值未变。Linux 使用同一公共来源，尚未在真实 Linux 主机验证。

`config-dry-run` 和 `config-sync` 只处理来源属于本仓库的已启用映射；仓库外的映射和本机历史登记不参与应用。同一文件存在仓库外的应用映射时停止，要求先核对冲突。`config-status` 保留原生状态视图，可能同时列出其他来源的映射。

日常同步先显示实际差异，再输入小写 `yes` 确认；无变化时直接提示无需同步，不建立备份。可以指定目标物理路径：

```bash
mise run config-sync --dry-run
mise run config-sync ~/.zshrc
# 自动化：先审阅预览，再显式跳过项目确认
mise run config-sync --yes
```

应用过程使用 `.system-sync/operation.lock`，重新核对预览状态，备份目标，调用原生 apply，再复查结果。`machine-init` 使用同一模块，但额外拒绝替换已有普通文件/其他来源链接，以及执行 `absent` 删除；日常同步保留显式接管能力，备份不会替代 mise 自身的冲突检查；需要覆盖冲突文件时使用 `config-sync --force`，仍先备份再交给原生 apply 处理。

备份和事务记录保存在 `.system-sync/history/<时间>-config-sync-*/`；初始化仍保存在 `*-machine-init-*/`。失败保留备份并标记 `needs-review`，需要人工核对，恢复方法见[本机记录](recovery.md#中断与写入失败的恢复)。

## 后续逐项接管其他配置

1. 审阅待接管的配置以及当前平台差异；全局 mise 工具配置已经启用。
2. 在根 `mise.toml` 中启用所需公共映射，再在当前平台的 `mise.macos.toml` 或 `mise.linux.toml` 中启用对应映射。
3. 运行 `mise run config-status` 和 `mise run config-dry-run`，确认目标和来源都属于当前平台。
4. 处理已有真实文件与 symlink 的首次接管冲突后，再运行 `mise run config-sync`。
5. zsh 已接管并启用 mise Shell 激活；接管其他应用时继续保留这一个交互激活入口，不重复初始化旧管理器。

希望保留当前配置并解除仓库链接依赖时，使用 `mise run config-detach --dry-run` 预览，再运行 `mise run config-detach`，见[配置迁出](recovery.md#配置迁出保留本机文件)。

从 `[dotfiles]` 删除条目不会自动删除已经应用的目标。需要移除已部署目标时，先执行 `mise bootstrap dotfiles unapply --dry-run`，确认后执行 `mise bootstrap dotfiles unapply`，再删除映射。


## 参考

- [mise Dotfiles](https://mise.jdx.dev/dotfiles.html)
- [mise config trust](https://mise.jdx.dev/cli/trust.html)
