// Package gateway 提供可声明组装且安全顺序固定的 HTTP 网关处理器。
//
// 公开契约、装配选项和实现按功能就近组织；扩展组件通过接口接入，
// 请求安全阶段的顺序统一由 Gateway 的流水线维护。
package gateway
