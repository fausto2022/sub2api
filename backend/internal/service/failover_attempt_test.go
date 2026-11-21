package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type budgetAttemptTestUpstream struct {
	do func(*http.Request) (*http.Response, error)
}

func (u budgetAttemptTestUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(r)
}

func (u budgetAttemptTestUpstream) DoWithTLS(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.do(r)
}

func TestFailoverBudgetHTTPAttemptObservesOnlySends(t *testing.T) {
	for _, tls := range []bool{false, true} {
		name := "Do"
		if tls {
			name = "DoWithTLS"
		}
		t.Run(name, func(t *testing.T) {
			var sends, observed, header, closed int
			var wantCtx context.Context
			u := failoverAttemptUpstream{budgetAttemptTestUpstream{do: func(r *http.Request) (*http.Response, error) {
				sends++
				if wantCtx != nil && r.Context() != wantCtx {
					t.Fatal("transport did not receive attempt context")
				}
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("upstream"))}, nil
			}}}
			send := func(r *http.Request) (*http.Response, error) {
				if tls {
					return u.DoWithTLS(r, "", 42, 7, nil)
				}
				return u.Do(r, "", 42, 7)
			}
			base, err := http.NewRequest(http.MethodPost, "https://example.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := send(base)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if sends != 1 || observed != 0 {
				t.Fatalf("legacy counts: sends=%d, observed=%d", sends, observed)
			}
			denied := errors.New("budget limit reached")
			ctx := WithFailoverAttemptObserver(base.Context(), func(parent context.Context, id int64) (FailoverHTTPAttempt, error) {
				if id != 42 {
					t.Fatalf("observed account = %d", id)
				}
				observed++
				if observed > 1 {
					return FailoverHTTPAttempt{}, denied
				}
				attemptCtx, cancel := context.WithCancel(parent)
				wantCtx = attemptCtx
				return FailoverHTTPAttempt{Context: attemptCtx, Headers: func(status int) { header = status }, Close: func() { closed++; cancel() }}, nil
			})
			withObserver := base.WithContext(ctx)
			resp, err = send(withObserver)
			if err != nil || header != 503 || closed != 0 {
				t.Fatalf("headers=%d, closed=%d, err=%v", header, closed, err)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "upstream" {
				t.Fatalf("body=%q, err=%v", body, err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if closed != 1 || wantCtx.Err() != context.Canceled {
				t.Fatalf("closed=%d, err=%v", closed, wantCtx.Err())
			}
			if response, err := send(withObserver); response != nil || !errors.Is(err, denied) || sends != 2 {
				t.Fatalf("refused retry: response=%v, sends=%d, error=%v", response, sends, err)
			}
		})
	}
}
