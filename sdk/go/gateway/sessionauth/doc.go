// Package sessionauth 提供依赖 Redis 的网关会话认证与会话管理。
//
// 业务方负责 Redis 安装、单实例或 Sentinel 部署，以及 Client 的创建和关闭。
// 本包不支持 Redis Cluster，不设置 Cookie，不实现登录或 CSRF 策略。
package sessionauth
