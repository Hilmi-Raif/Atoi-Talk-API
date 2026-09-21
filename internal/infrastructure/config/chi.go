package config

import (
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	slogchi "github.com/samber/slog-chi"
)

func NewChi(cfg *AppConfig) *chi.Mux {
	r := chi.NewRouter()

	tokenRegex := regexp.MustCompile(`(token=)([^&]+)`)

	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == "query" || a.Key == "url" || a.Key == "request.query" {
			if a.Value.Kind() == slog.KindString {
				strVal := a.Value.String()
				if tokenRegex.MatchString(strVal) {
					cleanValue := tokenRegex.ReplaceAllString(strVal, "$1[REDACTED]")
					return slog.Attr{Key: a.Key, Value: slog.StringValue(cleanValue)}
				}
			}
		}
		return a
	}

	var handler slog.Handler
	handlerOpts := &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: replaceAttr,
	}

	if cfg.AppEnv == "production" {
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, handlerOpts)
	}

	handler = observability.NewTraceContextHandler(handler)
	requestLogger := slog.New(handler)

	r.Use(slogchi.New(requestLogger))

	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AppCorsAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, helper.NewNotFoundError(""))
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, helper.NewMethodNotAllowedError(""))
	})

	return r
}
