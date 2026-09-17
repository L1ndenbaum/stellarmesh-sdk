package sessionauth

import "testing"

func TestKeyBuilder(t *testing.T) {
	for _, tt := range []struct{ separator, scope, id, want string }{
		{"", "kgraph", "abc", "kgraph:session:abc"},
		{"|", "kgraph", "a|b", "kgraph|session|a%7Cb"},
		{"::", "a:b", "{%}\n", "a%3Ab::session::%7B%25%7D%0A"},
		{"-", "scope", "abc-def", "scope-session-abc%2Ddef"},
		{"", " a", "a%3Ab", "%20a:session:a%253Ab"},
	} {
		keys, err := NewKeyBuilder(KeyConfig{Separator: tt.separator})
		if err != nil {
			t.Fatal(err)
		}
		got, err := keys.SessionKey(tt.scope, tt.id)
		if err != nil || got != tt.want {
			t.Fatalf("key=%q err=%v", got, err)
		}
	}
	var keys KeyBuilder
	if got, _ := keys.UserSessionsKey("kgraph", "user-1"); got != "kgraph:user_sessions:user-1" {
		t.Fatal(got)
	}
	for _, bad := range []string{"a", "%", "{", "}", " ", "\n", "中"} {
		if _, err := NewKeyBuilder(KeyConfig{Separator: bad}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := keys.SessionKey("", "a"); err == nil {
		t.Fatal("empty scope")
	}
	if _, err := keys.UserSessionsKey("p", ""); err == nil {
		t.Fatal("empty ID")
	}
}
