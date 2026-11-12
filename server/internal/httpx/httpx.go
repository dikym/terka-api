// Package httpx holds the handler shape and the JSON error envelope every endpoint shares.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Handler is an http handler that reports failure by returning an error.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Error is a failure the client is meant to see, sent as {"code","message"}.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type envelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JSON answers are always dynamic, and Cloudflare caches responses that carry no Cache-Control.
func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func writeInternal(w http.ResponseWriter) {
	_ = JSON(w, http.StatusInternalServerError, envelope{Code: "internal", Message: "internal error"})
}

// Recover turns a panic in next into the internal error envelope instead of a dropped connection.
func Recover(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if cause := recover(); cause != nil {
				log.ErrorContext(r.Context(), "panic", "cause", cause, "path", r.URL.Path)
				writeInternal(w)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Handle adapts h. Only an *Error reaches the client as written; anything else is logged and hidden.
func Handle(h Handler, log *slog.Logger) http.Handler {
	return Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var shown *Error
		if errors.As(err, &shown) {
			_ = JSON(w, shown.Status, envelope{Code: shown.Code, Message: shown.Message})
			return
		}
		log.ErrorContext(r.Context(), "handler failed", "error", err, "path", r.URL.Path)
		writeInternal(w)
	}), log)
}
