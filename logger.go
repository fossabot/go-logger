package logger

import (
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewCore will create handy Core with sensible defaults:
// - messages with error level and higher will go to stderr, everything else to stdout
// - use json encoder for production and console for development
func NewCore(debug bool) zapcore.Core {
	var encoder zapcore.Encoder
	if debug {
		encoder = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	} else {
		encoder = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	}

	return zapcore.NewTee(
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl >= zapcore.ErrorLevel
		})),
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl < zapcore.ErrorLevel
		})),
	)
}

// RequestLogger is a middleware for injecting sentry.Hub and zap.Logger into request context.
// If provided logger has sentryCoreWrapper as core injected logger will have core with same local core and
// sentry core based on an empty Hub for each request so breadcrumbs list will be empty each time.
// In other case logger.Core() will be used as a local core and sentry core will be created if sentry is initialized
func RequestLogger(logger *zap.Logger) func(next http.Handler) http.Handler {
	localCore := logger.Core()
	client := sentry.CurrentHub().Client()
	var options []SentryCoreOption
	if wrappedCore, ok := localCore.(sentryCoreWrapper); ok {
		localCore = wrappedCore.LocalCore()
		sentryCore := wrappedCore.SentryCore()
		client = sentryCore.hub.Client()

		if breadcrumbLevel := sentryCore.BreadcrumbLevel; breadcrumbLevel != defaultBreadcrumbLevel {
			options = append(options, BreadcrumbLevel(breadcrumbLevel))
		}
		if eventLevel := sentryCore.BreadcrumbLevel; eventLevel != defaultEventLevel {
			options = append(options, EventLevel(eventLevel))
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			core := localCore
			if client != nil {
				hub := sentry.NewHub(client, sentry.NewScope())
				core = NewSentryCoreWrapper(localCore, hub, options...)

				ctx = WithHub(ctx, hub)
			}

			logger := zap.New(core)
			ctx = WithLogger(ctx, logger)

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				logger.Debug("",
					zap.Duration("duration", time.Since(t1)),
					zap.Int("status", ww.Status()),
					zap.Int("size", ww.BytesWritten()),
					zap.String("method", r.Method),
					zap.String("url", r.URL.String()),
				)
			}()

			next.ServeHTTP(ww, r.WithContext(ctx))
		})
	}
}
