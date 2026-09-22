// Package logging 为标准库 slog.Handler 提供字段清洗装饰器。
//
// 输出流、等级、Collector 和持久化由应用拥有；本包不启动后台任务。
// 清洗不等于任意文本的秘密扫描，调用方仍应避免把凭证写进消息正文。
// 派生 Handler 不深拷贝嵌套业务值，日志处理期间不要并发修改这些值。
package logging
