package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCredentialExtractors(t *testing.T) {
	cookie, err := CookieCredential("sid")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, header string
		values       []string
		extract      CredentialExtractor
		want         string
		invalid      bool
	}{
		{"missing bearer", "Authorization", nil, BearerCredential(), "", false},
		{"bearer", "Authorization", []string{"bEaReR abc.def-123"}, BearerCredential(), "abc.def-123", false},
		{"duplicate bearer", "Authorization", []string{"Bearer a", "Bearer b"}, BearerCredential(), "", true},
		{"wrong scheme", "Authorization", []string{"Basic abc"}, BearerCredential(), "", true},
		{"empty bearer", "Authorization", []string{"Bearer "}, BearerCredential(), "", true},
		{"combined bearer", "Authorization", []string{"Bearer a, Bearer b"}, BearerCredential(), "", true},
		{"invalid bearer", "Authorization", []string{"Bearer a=b"}, BearerCredential(), "", true},
		{"cookie", "Cookie", []string{"other=a; sid=abc"}, cookie, "abc", false},
		{"missing cookie", "Cookie", nil, cookie, "", false},
		{"empty cookie", "Cookie", []string{"sid="}, cookie, "", false},
		{"duplicate cookie", "Cookie", []string{"sid=a; sid=b"}, cookie, "", true},
		{"duplicate lines", "Cookie", []string{"sid=a", "sid=b"}, cookie, "", true},
		{"malformed cookie", "Cookie", []string{"sid=\"unterminated"}, cookie, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header[tt.header] = tt.values
			got, err := tt.extract(r)
			if got != tt.want || errors.Is(err, ErrInvalidCredential) != tt.invalid {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	if _, err := CookieCredential("bad name"); err == nil {
		t.Fatal("accepted invalid name")
	}
}

func TestGatewayCredentialExtraction(t *testing.T) {
	for _, tt := range []struct {
		name    string
		extract CredentialExtractor
		public  bool
		status  int
		code    string
		calls   int
	}{
		{"custom", func(*http.Request) (string, error) { return "custom", nil }, false, 204, "", 1},
		{"missing", func(*http.Request) (string, error) { return "", nil }, false, 401, "missing_credential", 0},
		{"invalid", func(*http.Request) (string, error) { return "", ErrInvalidCredential }, false, 401, "invalid_credential", 0},
		{"failure", func(*http.Request) (string, error) { return "", errors.New("backend unavailable") }, false, 503, "credential_extraction_failed", 0},
		{"public", func(*http.Request) (string, error) { t.Fatal("public route extracted credentials"); return "", nil }, true, 204, "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			route := publicRoute()
			if !tt.public {
				route.Access = AccessProtected
			}
			calls := 0
			code := ""
			g, err := New(WithRoutes(route), WithoutAccessLog(), WithUpstreamResolver(UpstreamResolverFunc(func(Route) (http.Handler, error) {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), nil
			})), WithAuthenticator(AuthenticatorFunc(func(_ context.Context, v string) (AuthenticationDecision, error) {
				calls++
				if v != "custom" {
					t.Fatal(v)
				}
				return AuthenticationDecision{Authenticated: true, Identity: Identity{UserID: "u"}}, nil
			}), tt.extract), WithErrorResponder(ErrorResponderFunc(func(w http.ResponseWriter, _ *http.Request, e GatewayError) { code = e.Code; w.WriteHeader(e.Status) })))
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, httptest.NewRequest("GET", "/public", nil))
			if w.Code != tt.status || code != tt.code || calls != tt.calls {
				t.Fatalf("status=%d code=%s calls=%d", w.Code, code, calls)
			}
		})
	}
}

func TestGatewayRejectsInvalidCredentialConfiguration(t *testing.T) {
	auth := AuthenticatorFunc(func(context.Context, string) (AuthenticationDecision, error) { return AuthenticationDecision{}, nil })
	for _, option := range []Option{WithAuthenticator(auth, nil), WithAuthenticator(auth, BearerCredential(), BearerCredential())} {
		if _, err := New(WithRoutes(publicRoute()), option); err == nil {
			t.Fatal("accepted invalid extractor configuration")
		}
	}
}
