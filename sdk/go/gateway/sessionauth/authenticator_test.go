package sessionauth

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
)

type readerFunc func(context.Context, string) (Session, bool, error)

func (f readerFunc) Lookup(ctx context.Context, id string) (Session, bool, error) { return f(ctx, id) }
func TestAuthenticator(t *testing.T) {
	id := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	now := time.Now()
	failure := errors.New("redis failed")
	for _, tt := range []struct {
		name, credential string
		found            bool
		session          Session
		err              error
		accepted         bool
	}{
		{name: "valid", credential: id, found: true, session: Session{ID: id, Identity: gateway.Identity{UserID: "u"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, accepted: true},
		{name: "missing", credential: id},
		{name: "malformed", credential: "not-a-session"},
		{name: "failure", credential: id, err: failure},
		{name: "corrupt", credential: id, found: true, session: Session{ID: id}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			auth, err := NewAuthenticator(readerFunc(func(context.Context, string) (Session, bool, error) {
				called = true
				return tt.session, tt.found, tt.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			got, err := auth.Authenticate(context.Background(), tt.credential)
			if got.Authenticated != tt.accepted {
				t.Fatalf("decision=%+v", got)
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatal(err)
			}
			if tt.name == "corrupt" && !errors.Is(err, ErrCorruptSession) {
				t.Fatal(err)
			}
			if tt.name == "malformed" && called {
				t.Fatal("read malformed credential")
			}
		})
	}
	var nilReader readerFunc
	if _, err := NewAuthenticator(nilReader); err == nil {
		t.Fatal("accepted nil reader")
	}
}
