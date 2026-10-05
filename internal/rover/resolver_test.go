package rover

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLocalResolverCacheExpiresAndInvalidates(t *testing.T) {
	calls := 0
	r := &localResolver{lookup: func(context.Context, string) (string, error) { calls++; return "192.168.1.30", nil }}
	for range 3 {
		if _, err := r.resolve(context.Background(), "cam-rover.local"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("lookups=%d", calls)
	}
	r.mu.Lock()
	r.expires = time.Now().Add(-time.Second)
	r.mu.Unlock()
	_, _ = r.resolve(context.Background(), "cam-rover.local")
	if calls != 2 {
		t.Fatal("cache did not expire")
	}
	r.invalidate()
	_, _ = r.resolve(context.Background(), "cam-rover.local")
	if calls != 3 {
		t.Fatal("cache was not invalidated")
	}
	_, _ = r.resolve(context.Background(), "192.168.1.30")
	if calls != 3 {
		t.Fatal("IP address invoked resolver")
	}
}

func TestFailedLookupDoesNotReturnStaleIP(t *testing.T) {
	r := &localResolver{host: "cam-rover.local", ip: "192.168.1.30", lookup: func(context.Context, string) (string, error) { return "", errors.New("offline") }}
	if ip, err := r.resolve(context.Background(), "cam-rover.local"); err == nil || ip != "" {
		t.Fatal("stale fallback used")
	}
}

func TestAddressChangeInvalidatesResolver(t *testing.T) {
	c := New("cam-rover.local", "synthetic-test-token")
	c.resolver.expires = time.Now().Add(time.Second)
	c.SetAddress("other-rover.local")
	if !c.resolver.expires.IsZero() {
		t.Fatal("old address resolution retained")
	}
}

func TestInvalidationDuringLookupCannotRestoreOldCache(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r := &localResolver{lookup: func(context.Context, string) (string, error) { close(entered); <-release; return "192.168.1.30", nil }}
	done := make(chan struct{})
	go func() { _, _ = r.resolve(context.Background(), "cam-rover.local"); close(done) }()
	<-entered
	r.invalidate()
	close(release)
	<-done
	if !r.expires.IsZero() {
		t.Fatal("old in-flight lookup repopulated cache")
	}
}
