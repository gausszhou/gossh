// 编译期特性开关(与后端 Go build tags 对应,经 Makefile 同源注入):
//   SFTP —— 后端 -tags sftp / Makefile SFTP=1;前端 VITE_SFTP=1 时启用
//   (默认禁用:中栏 SFTP 面板不编译进 UI,后端也不提供 /api/**/sftp/* 端点)。
//   RUN  —— 后端 -tags run / Makefile RUN=1;前端 VITE_RUN=1 时启用
//   (默认禁用:单命令执行,RunModal/RunView/▶ 快捷键不进入 UI,后端
//   POST /api/run 与 `gossh run` 子命令也不存在)。
export const SFTP_ENABLED = import.meta.env.VITE_SFTP === '1'
export const RUN_ENABLED = import.meta.env.VITE_RUN === '1'