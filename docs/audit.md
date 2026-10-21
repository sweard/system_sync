# 只读审计

[返回项目首页](../README.md)

`audit` 汇总本机与当前仓库的 tools、packages、PATH 和配置映射状态。报告区分已有声明的漂移、未纳管候选和按约定留在本机的内容，不把“发现了其他安装”直接等同于“必须移除”。

## 使用方法

在仓库根目录执行：

```bash
# 四类完整报告
mise run audit

# JSON；--quiet 放在任务名前，避免 mise 的任务提示干扰输出
mise run --quiet audit --json

# 只检查指定类别，可重复 --only
mise run audit --only tools --only path
mise run audit --only config

# 除查询失败外，发现漂移也返回非零
mise run audit --strict

# 手动控制文字报告的颜色
mise run audit --color always
mise run audit --color never
```

首次运行会自动从仓库 `prebuilt/<版本>/` 校验并解压当前平台的 [Go helper](go-helper.md) 到本机缓存；也可提前执行 `mise run helper-install`。这一初始化不下载或编译，安装提示仅写入 stderr，保持 `--json` 的 stdout 可解析。审计本身不修改系统包和用户配置。也可通过 `./scripts/system-sync-helper.sh audit` 直接运行；日常审计及其系统包状态查询不依赖 Python 或 Go 编译器。[tests/reference/audit.py](../tests/reference/audit.py) 暂留作行为对照测试的参考实现。

## 查询执行方式

所选类别最多由 4 个 Go goroutine 并发检查，报告仍按 tools、packages、PATH、config 的固定顺序输出。`--only` 只执行选中的类别。tools 与 PATH 共用本次审计的一份工具状态快照；查询失败时两类分别报告未完成核对，不重复查询，也不阻止其他类别完成。

命令来源检查按命令名去重，每批最多并发执行 4 次 `mise which`；原生命令查询失败后不再启动后续批次。结果按原工具顺序写入报告，保留失败前已经完成的检查。查询结果只在本次审计中复用，下次运行重新读取本机状态，不持久缓存审计结论。

并发范围限于现有只读查询，包安装、卸载、信任和事务写入仍使用各自的确认与共享锁。macOS 包入口由 Homebrew 平台脚本执行一次预检；Linux 状态入口直接复用平台脚本读取的原生 JSON，避免外层重复查询。Linux 安装预览仍在调用原生 mise 前单独预检。真实主机延迟也受 Homebrew、磁盘和查询缓存影响，不能仅按 Python 或 Go 的计算速度推算。

Go 迁移前，2026-10-21 在本机对 `11364be` 与 Python 查询优化实现分别预热一次，再交替测量各 3 次：审计核心耗时中位数由 **3.894 秒降至 3.572 秒，减少约 8.3%**。测量包含 Python 启动，未包含 mise 任务启动与终端报告渲染；所有报告规范化后的校验值一致。这是查询优化的历史结果，不是 Go 迁移的性能数据。本机原始计时保存在 Git 忽略的 `.system-sync/audit-performance-comparison.json`。

## 报告分类

| 分类 | 含义 | 默认退出状态 |
|---|---|---|
| 已核对 `ok` | 声明已满足，或命令来源符合 mise 选择 | 不导致失败 |
| 漂移 `drift` | 已声明版本缺失、配置不一致、普通失效 PATH 项或命令被遮蔽 | 默认仍完成报告，`--strict` 时失败 |
| 待纳管候选 `candidate` | 其他安装来源、未声明根包、未纳管配置或 PATH 来源 | 需要人工确认意图，不导致失败 |
| 本机保留 `local` | 已注册的外部安装、系统/宿主路径、虚拟环境、私有文件等 | 不导致失败 |
| 未完成核对 `error` | 命令不可用、查询超时、权限不足或状态结构不支持 | 返回非零，不能视为已同步 |

汇总数量是报告条目数；一条候选记录可能包含多个安装路径或包名。文字报告每组最多展开 8 项，JSON 包含完整列表。单个类别查询失败后仍继续检查其他类别。

文字报告及汇总始终显示固定标记，颜色开启时突出待处理项及其明细：

| 状态 | 标记 | 颜色 |
|---|---|---|
| 漂移，需要处理 | `[!]` | 红色 |
| 待纳管候选，需要确认 | `[?]` | 黄色 |
| 未完成核对 | `[x]` | 红色加粗 |
| 已核对 | `[+]` | 默认颜色 |
| 本机保留 | `[-]` | 默认颜色 |

默认 `--color auto` 仅在标准输出连接终端、未设置 `NO_COLOR` 且 `TERM` 不是 `dumb` 时启用颜色；重定向或管道输出保留标记并关闭颜色。`--color always` 强制启用，可覆盖上述环境限制，`--color never` 始终关闭。汇总中数量为 0 的状态使用默认颜色。`--json` 始终输出原有结构，不添加颜色或标记，即使同时指定 `--color always`。

审计任务使用 [`raw = true`](https://mise.jdx.dev/tasks/task-configuration.html#raw) 直接连接标准输入和输出，使 `mise run audit` 能检测真实终端；查询子命令仍断开标准输入，输出由审计脚本过滤和脱敏。mise 会在这条命令运行期间阻止其他任务命令并行执行。

## 检查范围

| 类别 | 检查内容 | 边界 |
|---|---|---|
| tools | `mise ls --current --json` 的声明与安装状态；nvm、uv Python、pyenv、rbenv、asdf 和 Bun 的常见安装位置 | 其他安装作为候选；被 mise 链接引用的原安装作为本机保留。不检查项目的 pandas 等依赖，也不把有新版可升级当成漂移。 |
| packages | 复用当前平台的 `scripts/system-sync.sh --status`；汇总缺失项、未声明根包、保护项及安装原因差异 | 覆盖项目已接入的 Homebrew、pacman/AUR、APT、DNF、zypper；不扫描手工安装的全部软件。未声明根包和可清理依赖是待审阅候选。 |
| PATH | 空项、相对项、重复项、失效目录；共享 PATH 规则是否落实；常用工具的 PATH 优先命令是否符合 `mise which` | 按路径查找可执行文件，不执行候选程序，不解析调用 Shell 的别名、函数和命令缓存。 |
| config | mise 已声明的文件/合并映射状态；固定白名单中的现有未纳管文件；已取消部署的组文件 | 只读取映射元数据和候选路径，不遍历整个 Home，也不把合并目标中的未声明私有键列为漂移。 |

来源检查覆盖 Node、Python、Ruby、Java、Rust、Bun、uv、Yarn，以及声明后使用的 Go、Deno 和 Flutter 主命令。其他 mise 工具仍检查版本安装状态，不猜测其可执行文件名称。CLI 查询范围及格式依据见 [mise ls](https://mise.jdx.dev/cli/ls.html)、[mise which](https://mise.jdx.dev/cli/which.html)和[dotfiles status](https://mise.jdx.dev/cli/bootstrap/dotfiles/status.html)。

配置候选白名单包括 Git、tmux、Starship、Ghostty、Neovim，以及 VS Code 的快捷键和非空 snippets 目录；具体位置见 [ConfigCandidates](../internal/audit/contracts.go)。只有存在且未被当前映射覆盖的路径才列为候选，不读取其内容。`secrets.env`、`.zshrc.local` 和仓库 `mise.local.toml` 按约定列为本机保留。

## PATH 与调用环境

mise 任务运行器会把工具目录前置到任务 PATH，直接审计任务内部 PATH 可能掩盖调用终端原有的遮蔽问题。因此任务使用 [`get_env()`](https://mise.jdx.dev/templates.html) 读取 mise 启动时的 PATH，通过 `SYSTEM_SYNC_AUDIT_CALLER_PATH` 传给 helper 比较；直接执行 helper 时使用当前进程 PATH。报告在 `context.path_source` 标明来源。

系统、包管理器、IDE 和已知宿主运行时注入的路径归入本机保留，即使临时目录当前不存在也单独说明。普通失效目录仍报告漂移；其他未被共享规则声明的目录列为候选。宿主应用内运行与普通终端内运行的结果可以不同，候选分类不证明文件所有权或重复管理。

Flutter 已通过 mise 链接复用原 SDK，共享 PATH 不再额外添加旧目录。Java 外部 JBR、Flutter 原目录和 Rust 的 rustup/Cargo 路径不直接视为重复管理；审计结合所选版本的安装路径判断。已激活虚拟环境中的 Python 作为项目覆盖保留。

## 只读与隐私约定

根配置将 `settings.task.run_auto_install` 设为 `false`，作用于本仓库全部任务；开发工具使用 `mise install` 显式安装。审计子命令也关闭自动工具安装，并断开标准输入，避免查询进入交互式安装流程。相关设置见 [mise 官方说明](https://mise.jdx.dev/configuration/settings.html)。

审计不安装、卸载、升级软件，不改写声明、真实信任库或应用设置。系统包检查复用已有只读状态流程，Homebrew cleanup 预演使用独立临时信任库；原生命令可能更新查询缓存，临时文件在查询结束后清理。

报告只输出选定的状态、版本和路径字段，不转发外部命令原始错误输出，也不输出完整环境、合并值或凭据。已知敏感环境值进一步脱敏。默认报告只写到标准输出，不自动导入候选、保存报告或运行修复；需要保留本机报告时可写入 Git 忽略的 `.system-sync/`。
