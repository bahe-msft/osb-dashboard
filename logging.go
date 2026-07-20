package main

import (
	"log/slog"
	"net"
	"net/http"
	"time"
)

type requestLogResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (writer *requestLogResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *requestLogResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(body)
	writer.bytes += written
	return written, err
}

func (writer *requestLogResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			generatedID, err := createRequestID()
			if err != nil {
				requestID = "unavailable"
			} else {
				requestID = generatedID
			}
		}

		w.Header().Set("X-Request-ID", requestID)
		response := &requestLogResponseWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		if response.status == 0 {
			response.status = http.StatusOK
		}

		remoteAddress := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remoteAddress = host
		}
		logger.InfoContext(
			r.Context(),
			"http request",
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("query", r.URL.RawQuery),
			slog.Int("status", response.status),
			slog.Int("bytes", response.bytes),
			slog.Duration("duration", time.Since(startedAt)),
			slog.String("remote", remoteAddress),
			slog.String("user_agent", r.UserAgent()),
			slog.String("referer", r.Referer()),
		)
	})
}
