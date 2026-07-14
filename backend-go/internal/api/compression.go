package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		writer, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		if err != nil {
			panic(err)
		}
		return writer
	},
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	return w.writer.Write(data)
}

func (w *gzipResponseWriter) WriteString(data string) (int, error) {
	w.Header().Del("Content-Length")
	return w.writer.Write([]byte(data))
}

func (w *gzipResponseWriter) Flush() {
	_ = w.writer.Flush()
	w.ResponseWriter.Flush()
}

func ResponseCompression() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.Method == http.MethodHead ||
			!strings.HasPrefix(ctx.Request.URL.Path, "/api/") ||
			!acceptsGzip(ctx.GetHeader("Accept-Encoding")) {
			ctx.Next()
			return
		}

		writer := gzipWriterPool.Get().(*gzip.Writer)
		writer.Reset(ctx.Writer)
		defer func() {
			_ = writer.Close()
			writer.Reset(io.Discard)
			gzipWriterPool.Put(writer)
		}()
		ctx.Header("Content-Encoding", "gzip")
		ctx.Header("Vary", "Accept-Encoding")
		ctx.Writer = &gzipResponseWriter{ResponseWriter: ctx.Writer, writer: writer}
		ctx.Next()
	}
}

func acceptsGzip(header string) bool {
	for _, value := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(value), ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}

		quality := 1.0
		for _, parameter := range parts[1:] {
			keyValue := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(keyValue) != 2 || !strings.EqualFold(keyValue[0], "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(keyValue[1], 64)
			if err != nil {
				quality = 0
			} else {
				quality = parsed
			}
		}
		return quality > 0
	}
	return false
}
