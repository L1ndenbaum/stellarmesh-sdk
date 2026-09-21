export { http } from './http/api/api';
export type {
  HttpApi,
  HttpApiBodyMethod,
  HttpApiDeclarationOptions,
  HttpApiQueryDeclarationOptions,
  HttpApiQueryMethod,
  HttpApiQueryOptions,
  HttpApiRequest,
  HttpApiRequestDescriptor,
  HttpApiRequestMethod,
  HttpApiResult,
} from './http/api/contracts';
export { HttpMethod } from './http/request/contracts';
export type {
  HttpHeaders,
  HttpProgress,
  HttpRequest,
  HttpRequestOptions,
} from './http/request/contracts';
export { ResponseType } from './http/response/contracts';
export type {
  HttpResponse,
  ResponseContext,
  ResponseTransform,
} from './http/response/contracts';
export { AuthRefreshResult } from './http/auth/contracts';
export { createAuthSession } from './http/auth/session';
export type {
  AuthSession,
  AuthBindingOptions,
  AuthSessionContext,
  AuthSessionEpoch,
  AuthSessionOptions,
} from './http/auth/contracts';
export type { RetryOptions } from './http/retry/contracts';
export { flattenEnvelopeResponse } from './http/response/envelope';
export type { ApiEnvelope, EnvelopeOptions } from './http/response/envelope';
export {
  HttpClientError,
  HttpErrorKind,
  isHttpClientError,
} from './http/error/http-client-error';
export type { HttpClientErrorOptions } from './http/error/http-client-error';
export type { ErrorCodeExtractor } from './http/error/contracts';

export type {
  SseApi,
  SseMessage,
  SseOptions,
  SseDeclarationOptions,
  SseQueryOptions,
  SseQueryDeclarationOptions,
  SseRequest,
  SseRequestDescriptor,
} from './http/sse/contracts';
