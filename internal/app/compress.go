package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func decompressBody(encoding string, raw []byte, limit int64) ([]byte, error) {
	var reader io.Reader
	closeReader := func() {}
	var err error
	switch encoding {
	case "gzip", "x-gzip":
		gzipReader, gzipErr := gzip.NewReader(bytes.NewReader(raw))
		reader, err = gzipReader, gzipErr
		if gzipReader != nil {
			closeReader = func() { _ = gzipReader.Close() }
		}
	case "deflate":
		flateReader := flate.NewReader(bytes.NewReader(raw))
		reader = flateReader
		closeReader = func() { _ = flateReader.Close() }
	case "br":
		reader = brotli.NewReader(bytes.NewReader(raw))
	case "zstd":
		zstdReader, zstdErr := zstd.NewReader(bytes.NewReader(raw), zstd.WithDecoderMaxMemory(uint64(limit+1)))
		reader, err = zstdReader, zstdErr
		if zstdReader != nil {
			closeReader = zstdReader.Close
		}
	default:
		return nil, fmt.Errorf("unsupported content encoding %q", encoding)
	}
	if err != nil {
		return nil, err
	}
	defer closeReader()

	buf := bytes.NewBuffer(nil)
	written, copyErr := io.CopyN(buf, reader, limit+1)
	if copyErr != nil && copyErr != io.EOF {
		return nil, copyErr
	}
	if written > limit {
		return nil, fmt.Errorf("decoded request body exceeds %d bytes", limit)
	}
	return buf.Bytes(), nil
}

func isCompressed(r *http.Request) bool {
	enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	return enc != "" && enc != "identity"
}
