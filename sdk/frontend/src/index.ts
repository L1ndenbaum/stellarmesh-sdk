export { http } from './http/api/api.js';
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
} from './http/api/contracts.js';
export { HttpMethod } from './http/request/contracts.js';
export type {
  HttpHeaders,
  HttpProgress,
  HttpRequest,
  HttpRequestOptions,
} from './http/request/contracts.js';
export { ResponseType } from './http/response/contracts.js';
export type {
  HttpResponse,
  ResponseContext,
  ResponseTransform,
} from './http/response/contracts.js';
export { createAuthSession } from './http/auth/session.js';
export type {
  AuthSession,
  AuthBindingOptions,
  AuthSessionContext,
  AuthSessionEpoch,
  AuthSessionOptions,
} from './http/auth/contracts.js';
export type { RetryOptions } from './http/retry/contracts.js';
export { flattenEnvelopeResponse } from './http/response/envelope.js';
export type { ApiEnvelope, EnvelopeOptions } from './http/response/envelope.js';
export {
  HttpClientError,
  isHttpClientError,
} from './http/error/http-client-error.js';
export type {
  HttpErrorKind,
  HttpClientErrorOptions,
} from './http/error/http-client-error.js';
