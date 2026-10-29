package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type admissionServerKey struct{}
type admissionHeldKey struct{}

// Auxiliary inference (probes/repair) shares the same provider FIFO and slots.
// Public inference already owns its atomic key+slot reservation and skips this.
type admissionTransport struct {
	base     http.RoundTripper
	provider Provider
}

func (t *admissionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s, _ := r.Context().Value(admissionServerKey{}).(*Server)
	held, _ := r.Context().Value(admissionHeldKey{}).(bool)
	inference := r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/messages") || strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses") || strings.HasSuffix(r.URL.Path, "/embeddings"))
	if s == nil || held || !inference || t.provider.ID == "" {
		return t.base.RoundTrip(r)
	}
	ctx, release, err := enterAuxiliaryAdmission(r.Context(), t.provider)
	if err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(r.Clone(ctx))
	if err != nil {
		release()
		return nil, err
	}
	response.Body = &admittedBody{ReadCloser: response.Body, release: release}
	return response, nil
}

func enterAuxiliaryAdmission(ctx context.Context, provider Provider) (context.Context, func(), error) {
	s, _ := ctx.Value(admissionServerKey{}).(*Server)
	held, _ := ctx.Value(admissionHeldKey{}).(bool)
	if s == nil || held || provider.ID == "" {
		return ctx, func() {}, nil
	}
	token, err := newID("probe_queue")
	if err != nil {
		return ctx, func() {}, err
	}
	var reserved reservation
	for {
		if ctx.Err() != nil {
			s.removeAdmission(provider.ID, token)
			return ctx, func() {}, ctx.Err()
		}
		var enabled bool
		if err = s.db.QueryRow(ctx, `SELECT enabled,max_concurrent_requests FROM providers WHERE id=$1`, provider.ID).Scan(&enabled, &provider.MaxConcurrentRequests); err != nil || !enabled {
			s.removeAdmission(provider.ID, token)
			return ctx, func() {}, errors.New("provider unavailable")
		}
		var reason string
		reserved, reason, _, _, err = s.tryProviderAdmission(ctx, provider, token, nil, 0)
		if err != nil {
			s.removeAdmission(provider.ID, token)
			return ctx, func() {}, err
		}
		if reason == "" {
			break
		}
		timer := time.NewTimer(admissionPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.removeAdmission(provider.ID, token)
			return ctx, func() {}, ctx.Err()
		case <-timer.C:
		}
	}
	return guardProviderLease(ctx, reserved.lease)
}

// Losing coordination cancels the call before its slot can be reassigned.
func guardProviderLease(ctx context.Context, lease *providerLease) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	watch := make(chan struct{})
	go func() {
		select {
		case <-lease.lost:
			cancel()
		case <-ctx.Done():
		case <-watch:
		}
	}()
	var once sync.Once
	release := func() { once.Do(func() { close(watch); cancel(); lease.release() }) }
	return context.WithValue(ctx, admissionHeldKey{}, true), release, nil
}

type admittedBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *admittedBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }

func (b *admittedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		_ = b.Close()
	}
	return n, err
}
