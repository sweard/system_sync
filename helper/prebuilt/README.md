# 随仓库保存的 Go helper

本目录作为普通 Git 文件保存，不使用 Git LFS。每个版本目录包含 macOS/Linux × ARM64/x86_64 的四个预编译归档、外层 `SHA256SUMS` 及公共构建输入 `SOURCE_SHA256SUMS`。固定版本由 [`helper/VERSION`](../VERSION) 选择。

首次运行任一使用 helper 的日常 task 时，自动校验并解压本机对应的包到被 Git 忽略的仓库根目录 `.system-sync/helper/`，无需安装 Go、Python 或联网下载 helper。也可提前运行 `mise run helper-install`。`v0.2.0` 还包含单包清单编辑、配置迁出、VS Code 扩展和本机历史管理；归档同时携带 Go 与 TOML 解析库许可。

开发者修改 Go 源码或构建输入后，在仓库根目录执行 `mise run helper-bundle` 更新本目录，再执行 `mise run check`。源码与更新后的归档、两份清单应一同提交；校验清单由构建脚本生成，`SOURCE_SHA256SUMS` 中的路径以 `helper/` 为基准。需要发布新 helper 版本时，先修改 `helper/VERSION` 再重新构建；已有机器通过 `mise run helper-install` 切换。

本目录只分发公共源码构建的工具，不包含 dotfiles、本机配置、历史或日志。完整命令、前置条件和迁移范围见 [Go helper](../README.md)。
