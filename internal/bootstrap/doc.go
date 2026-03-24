// Package bootstrap 负责进程级启动编排：配置、工作区、数据库、Agent、HTTP 路由、渠道运行时与优雅退出。
// 业务逻辑仍在各 internal 子包中；此处只做依赖组装与生命周期，避免 cmd 目录膨胀。
package bootstrap
