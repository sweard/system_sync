# 测试与覆盖迁移

[返回项目首页](../README.md) · [Go helper](../helper/README.md)

日常 task 使用 Go helper、Bash 和 mise 原生命令。原有 7 个 Python 参考实现已在核对覆盖后移除；Python 3.11+ 仍用于开发检查、真实 CLI 集成测试和模拟外部命令，不是日常运行依赖。

## 运行检查

在仓库根目录执行，需要 `dotfiles/common/mise/config.toml` 的 `tools.go` 固定的 Go、Python 3.11+、mise 及支持 Go race 检查的本机 C 工具链：

```bash
toolchain_version="$(./helper/scripts/go-version.sh)"
mise install "go@$toolchain_version"
mise run --tool "go@$toolchain_version" check
```

`check` 依次校验四平台预编译包及源码输入、构建本机 helper、运行前置检查、验证 task、执行 Go race 测试及 Python 集成测试。`--tool` 显式选择本次 task 使用的 Go，不改写工具声明。只修改测试和文档不影响分发包输入，无需重新生成预编译包。

`helper_fixture.py` 通过 Python 标准库 `tomllib` 读取同一 `tools.go` 作为独立预期值，CI 环境及构建反馈替身复用该值；实际 workflow 和构建/测试脚本共用 Bash 入口，以 `mise config get --file ... tools.go` 解析仓库指定文件。回归在临时副本中更改版本，验证所有入口随之选择、提示正确工具链，拒绝缺失字段、错误类型、版本范围、模板及非法 TOML，且不查询 Go 或安装工具。另覆盖 mise 缺失提示、全局/本地覆盖与环境变量不影响读取、不加载私有环境；构建输入校验验证 Go 字段及读取脚本漂移，其他工具、注释和环境设置变化不触发重建。

2026-10-23 在本机 macOS ARM64 的隔离 HOME 和空 mise 全局配置下，完整检查通过：97 个 Go 顶层测试（含 race）、187 项 Python 3.15 回归、24 项 task 校验、四平台归档校验及 `go vet`。固定版本来源、命令反馈及 Bash 3.2 在 `C` locale 下的回归包含在内，Python 3.11.17 另通过 16 项相关回归。真实 Linux 包事务仍待验收。

已有本机 helper 时，可单独执行一组 CLI 回归，例如：

```bash
python3 -B -m unittest discover -s tests -p test_parsers.py -v
```

## 分工

| 位置 | 职责 |
|---|---|
| `helper/internal/*/*_test.go` | 直接测试 Go 解析、安全校验、并发、共享锁、原子写入、事务日志及回滚；完整检查启用 race |
| `tests/test_*.py` | 调用实际 Go helper、Bash 和 mise，验证命令入口、输出、退出码及隔离部署；外部包管理器和 VS Code 使用替身，不修改宿主软件 |
| `helper_fixture.py` | 将本机已构建的 helper 放入隔离仓库；提供不跟随链接的测试快照，以及制造锁竞争的最小 POSIX `flock` 上下文 |
| `audit_fixture.py` | 提供合成的原生查询响应及隔离 HOME/PATH；不实现审计规则或计算预期报告 |
| `check_environment.py` | 先拒绝 Python 3.11 以下版本，再把系统前置检查交给 Go `runtime --check`，透传退出码 |
| `fixtures/audit/reports.json` | 审计的独立完整报告基线，供真实 Go CLI 回归使用 |

## 已迁移的覆盖

| 范围 | 原 Python 测试 | 当前验证 |
|---|---|---|
| mise JSON / zypper XML | 8 项参考解析测试 | 8 项改为调用 Go CLI；错误输入要求非零退出且无部分包名输出，另有 Go 解析单元测试 |
| VS Code 扩展 | 18 项，含参考实现的进程超时 | 17 项真实 Go CLI 回归；超时、私有诊断过滤和后代进程清理转入 Go `internal/extensions` |
| 单包清单编辑 | 34 项 Python 参考实现测试 | `internal/packageedit` 的 12 个 Go 顶层测试及子场景，覆盖五平台、精确信任、保护包、失败和并发；补齐原生命令、别名、cask、AUR 分组与移除、安装预览/取消、完成日志、注释/其他表及其他 profile 保留断言 |
| 配置迁出 | 12 项，其中 5 项直接测试 Python 事务 | 保留 7 项真实 mise/Go 隔离验收；6 个 Go `internal/configdetach` 顶层测试覆盖链接物化、目录、权限、私有备份、取消、失败回滚与并发修改保护 |
| 审计 | 23 项行为测试及 7 组两种实现对照 | 18 项直接调用 Go 的行为/入口测试；查询超时、stdin、脱敏、并发去重及失败批次由 Go 测试覆盖；7 组完整报告回归使用下述冻结基线 |
| 共享运行时 | 1 项 Python 版本前置测试及参考实现锁辅助 | 3 项最小前置检查测试；集成测试使用标准 POSIX 锁制造竞争，锁继承校验和事务逻辑由 Go `internal/syncstate` 等测试验证 |

测试迁移时 Python 用例数由 225 减为 182；Go 顶层测试由 94 增为 97，子场景另计。减少的是参考实现测试及与 Go 重复的测试入口；保留真实 CLI/部署覆盖，安全边界在移除前逐项映射到 Go 测试。固定 Go 版本来源统一及接入受管配置后新增 5 项回归，当前共有 187 项 Python 测试。

## 审计完整报告基线

[`fixtures/audit/reports.json`](fixtures/audit/reports.json) 在移除旧实现前，从提交 `4ca4a25` 的 Python 审计实现采集，并逐组与 Go 核对后冻结。7 个场景方法共覆盖 21 份完整 JSON 报告，部分同时核对完整文字输出及退出码，包括工具与 PATH 来源、包状态契约、配置漂移、私有目标、查询失败、脱敏和严格模式。

输入全部为测试合成值；临时目录根替换为 `<fixture>`，系统 PATH 在输入中映射到受控空目录，避免依赖 CI 主机安装的软件。测试只读取基线并核对 Go 输出，不再执行旧实现，也不从被测 Go 输出自动生成预期值。报告契约需要变更时，应同时审阅输入、完整 JSON/文字差异和对应行为断言，明确更新预期。
