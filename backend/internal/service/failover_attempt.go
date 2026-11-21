package service

import (
	"context"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// FailoverHTTPAttempt attaches a request-local observer to one actual send.
// Headers is called when HTTPUpstream returns a response. Close owns the
// attempt's lifetime, including response-body reads.
type FailoverHTTPAttempt struct {
	Context context.Context
	Headers func(int)
	Result  func(*http.Response, error)
	BodyEnd func()
	Close   func()
}

type failoverAttemptObserverKey struct{}

type FailoverAttemptObserver func(context.Context, int64) (FailoverHTTPAttempt, error)

func WithFailoverAttemptObserver(ctx context.Context, observer FailoverAttemptObserver) context.Context {
	return context.WithValue(ctx, failoverAttemptObserverKey{}, observer)
}

type failoverAttemptUpstream struct{ HTTPUpstream }

// Some forwarders already wrap their individual calls. Keep one boundary when
// compatibility forwarders share a service-wide wrapper.
func (u failoverAttemptUpstream) transport() HTTPUpstream {
	if inner, ok := u.HTTPUpstream.(failoverAttemptUpstream); ok {
		return inner.transport()
	}
	return u.HTTPUpstream
}

func (u failoverAttemptUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return observeFailoverAttempt(req, accountID, func(req *http.Request) (*http.Response, error) {
		return u.transport().Do(req, proxyURL, accountID, concurrency)
	})
}

func (u failoverAttemptUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return observeFailoverAttempt(req, accountID, func(req *http.Request) (*http.Response, error) {
		return u.transport().DoWithTLS(req, proxyURL, accountID, concurrency, profile)
	})
}

func observeFailoverAttempt(req *http.Request, accountID int64, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	observe, _ := req.Context().Value(failoverAttemptObserverKey{}).(FailoverAttemptObserver)
	if observe == nil {
		return send(req)
	}
	attempt, err := observe(req.Context(), accountID)
	if err != nil {
		return nil, err
	}
	if attempt.Context != nil {
		req = req.WithContext(attempt.Context)
	}
	resp, err := send(req)
	if resp != nil && attempt.Headers != nil {
		attempt.Headers(resp.StatusCode)
	}
	if attempt.Result != nil {
		attempt.Result(resp, err)
	}
	if resp != nil && resp.Body != nil && (attempt.Close != nil || attempt.BodyEnd != nil) {
		resp.Body = &failoverAttemptBody{ReadCloser: resp.Body, bodyEnd: attempt.BodyEnd, closeAttempt: attempt.Close}
	} else {
		if attempt.BodyEnd != nil {
			attempt.BodyEnd()
		}
		if attempt.Close != nil {
			attempt.Close()
		}
	}
	return resp, err
}

type failoverAttemptBody struct {
	io.ReadCloser
	bodyEnd      func()
	closeAttempt func()
}

func (b *failoverAttemptBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.bodyEnd != nil {
		b.bodyEnd()
	}
	return n, err
}

func (b *failoverAttemptBody) Close() error {
	if b.closeAttempt != nil {
		defer b.closeAttempt()
	}
	if b.bodyEnd != nil {
		defer b.bodyEnd()
	}
	return b.ReadCloser.Close()
}

func observeHTTPUpstream(upstream HTTPUpstream) HTTPUpstream {
	if upstream == nil {
		return nil
	}
	if _, installed := upstream.(failoverAttemptUpstream); installed {
		return upstream
	}
	return failoverAttemptUpstream{HTTPUpstream: upstream}
}

// EnableHTTPFailoverObservation installs the observation boundary at startup.
func (s *OpenAIGatewayService) EnableHTTPFailoverObservation() {
	if s != nil {
		s.httpUpstream = observeHTTPUpstream(s.httpUpstream)
	}
}

// EnableHTTPFailoverObservation covers the Chat/Responses conversion forwards.
func (s *GatewayService) EnableHTTPFailoverObservation() {
	if s != nil {
		s.httpUpstream = observeHTTPUpstream(s.httpUpstream)
	}
}

// EnableHTTPFailoverObservation covers the Gemini Chat conversion forward.
func (s *GeminiMessagesCompatService) EnableHTTPFailoverObservation() {
	if s != nil {
		s.httpUpstream = observeHTTPUpstream(s.httpUpstream)
	}
}
