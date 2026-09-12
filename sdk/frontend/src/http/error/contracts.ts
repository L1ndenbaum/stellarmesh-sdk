import type { ResponseContext } from '../response/contracts.js';

/** 同步读取响应中的额外错误码；空值表示不提供错误码，不回退到默认字段。 */
export type ErrorCodeExtractor = (
  data: unknown,
  context: Readonly<ResponseContext>,
) => string | number | null | undefined;
