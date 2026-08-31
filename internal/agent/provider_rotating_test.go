package agent

import (
	"context"
	"errors"
	"testing"
)

type providerFunc func(context.Context, Turn) (ProviderResult, error)

func (f providerFunc) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return f(ctx, turn)
}

type statusFailure struct{ code int }

func (e statusFailure) Error() string   { return "provider failure" }
func (e statusFailure) StatusCode() int { return e.code }

func TestRotatingProviderRetriesOnlyClassifiedRateLimit(t *testing.T) {
	ring := NewKeyRing([]string{"key-a", "key-b"})
	var used []string
	rotating, err := NewRotatingProvider(ring, func(key string) (Provider, error) {
		used = append(used, key)
		return providerFunc(func(context.Context, Turn) (ProviderResult, error) {
			if key == "key-a" {
				return ProviderResult{}, statusFailure{code: 429}
			}
			return ProviderResult{Text: "ok"}, nil
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotating.Generate(context.Background(), Turn{})
	if err != nil || got.Text != "ok" || len(used) != 2 || used[0] == used[1] {
		t.Fatalf("result=%+v err=%v used=%v", got, err, used)
	}
}

func TestRotatingProviderDoesNotRotateAuthOrArbitraryQuotaText(t *testing.T) {
	for _, failure := range []error{statusFailure{code: 401}, errors.New("please review quota settings")} {
		ring := NewKeyRing([]string{"key-a", "key-b"})
		calls := 0
		rotating, err := NewRotatingProvider(ring, func(string) (Provider, error) {
			return providerFunc(func(context.Context, Turn) (ProviderResult, error) {
				calls++
				return ProviderResult{}, failure
			}), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rotating.Generate(context.Background(), Turn{}); err == nil || calls != 1 {
			t.Fatalf("failure=%v err=%v calls=%d", failure, err, calls)
		}
	}
}

func TestClassifyCredentialFailureStrict(t *testing.T) {
	cases := []struct {
		err  error
		want CredentialFailureClass
	}{
		{errors.New("status 429: slow down"), CredentialFailureRateLimit},
		{errors.New("code=insufficient_quota"), CredentialFailureQuota},
		{errors.New("status 403 quota_exceeded"), CredentialFailureNone},
		{errors.New("quota may be low"), CredentialFailureNone},
	}
	for _, tc := range cases {
		if got := ClassifyCredentialFailure(tc.err); got != tc.want {
			t.Errorf("ClassifyCredentialFailure(%q)=%q want %q", tc.err, got, tc.want)
		}
	}
}
