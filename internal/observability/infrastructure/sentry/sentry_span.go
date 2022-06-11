package sentry

import (
	"context"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"

	"github.com/snyk/snyk-ls/config"
	"github.com/snyk/snyk-ls/internal/observability/performance"
)

type span struct {
	span      *sentry.Span
	txName    string
	operation string
	ctx       context.Context
}

func (s *span) GetTxName() string {
	return s.txName
}

func (s *span) GetOperation() string {
	return s.operation
}

func (s *span) GetTraceId() string {
	return s.ctx.Value(performance.TraceIdContextKey("trace_id")).(string)
}

func (s *span) Context() context.Context {
	return s.ctx
}

func (s *span) StartSpan(ctx context.Context) {
	var options []sentry.SpanOption
	if s.txName != "" {
		options = append(options, sentry.TransactionName(s.txName))
	}
	s.span = sentry.StartSpan(ctx, s.operation, options...)
	s.span.SetTag("organization", config.CurrentConfig().GetOrganization())
	s.ctx = performance.GetContextWithTraceId(s.span.Context(), s.span.TraceID.String())

	log.Debug().
		Str("method", "sentrySpan.StartSpan").
		Str("operation", s.operation).
		Str("txName", s.txName).
		Msg("starting span")
}

func (s *span) Finish() {
	log.Debug().
		Str("method", "span.Finish").
		Str("operation", s.span.Op).
		Msg("finishing span")
	s.span.Finish()
}

func (s *span) SetTransactionName(name string) {
	if name != "" && s.txName == "" {
		s.txName = name
	}
}
