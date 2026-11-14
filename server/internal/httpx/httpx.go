// Package httpx holds the handler shape and the JSON error envelope every endpoint shares.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
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

// Every request body the API takes is a small JSON object; 8 KiB is far more than any of them needs.
const maxBody = 8 << 10

func badRequest(message string) error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: message}
}

// DecodeJSON reads exactly one JSON object into v. Anything else (an oversized body, an unknown
// field, a second value, null or a bare string) is the client's mistake and comes back as a 400.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return badRequest("request body is too large or unreadable")
	}
	body = bytes.TrimSpace(body)
	// Decoding null into a struct succeeds and leaves it empty, so the shape is checked first.
	if len(body) == 0 || body[0] != '{' {
		return badRequest("request body must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("request body is not valid for this endpoint")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return badRequest("request body must hold a single JSON object")
	}
	return nil
}

// ClientIP is the address rate limits count against. CF-Connecting-IP is believed only when the
// server is known to sit behind Cloudflare, because anyone else can send that header.
func ClientIP(r *http.Request, trustCloudflare bool) string {
	if trustCloudflare {
		if addr, err := netip.ParseAddr(r.Header.Get("CF-Connecting-IP")); err == nil {
			return addr.WithZone("").Unmap().String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
