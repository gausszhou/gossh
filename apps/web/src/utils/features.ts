// 编译期特性开关(与后端 Go build tags 对应,经 Makefile 同源注入):
//   SFTP —— 后端 -tags sftp / Makefile SFTP=1;前端 VITE_ENABLE_SFTP=1 时启用
//   (默认禁用:中栏 SFTP 面板不编译进 UI,后端也不提供 /api/**/sftp/* 端点)。
export const SFTP_ENABLED = import.meta.env.VITE_ENABLE_SFTP === '1'