export { httpClient } from './http/client.js';
export { flattenEnvelopeResponse } from './http/envelope.js';
export type { ApiEnvelope, EnvelopeOptions } from './http/envelope.js';
export { HttpClientError, isHttpClientError } from './http/errors.js';
export type { HttpErrorKind, HttpClientErrorOptions } from './http/errors.js';
export type {
  AuthOptions,
  ConfigurableHttpClient,
  HttpClient,
  HttpBodyMethod,
  HttpHeaders,
  HttpMethod,
  HttpProgress,
  HttpRequest,
  HttpRequestOptions,
  HttpResponse,
  ResponseContext,
  ResponseTransform,
  ResponseType,
  RetryOptions,
} from './http/types.js';
