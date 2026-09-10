export { httpClient } from './http/client.js';
export { HttpMethod, ResponseType } from './http/types.js';
export { createAuthSession } from './http/auth.js';
export type { AuthSession } from './http/auth.js';
export { flattenEnvelopeResponse } from './http/envelope.js';
export type { ApiEnvelope, EnvelopeOptions } from './http/envelope.js';
export { HttpClientError, isHttpClientError } from './http/errors.js';
export type { HttpErrorKind, HttpClientErrorOptions } from './http/errors.js';
export type {
  AuthBindingOptions,
  AuthSessionContext,
  AuthSessionEpoch,
  AuthSessionOptions,
  ConfigurableHttpClient,
  HttpClient,
  HttpBodyMethod,
  HttpHeaders,
  HttpProgress,
  HttpRequest,
  HttpRequestOptions,
  HttpResponse,
  ResponseContext,
  ResponseTransform,
  RetryOptions,
} from './http/types.js';
