package app

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decompressBody(encoding string, raw []byte, limit int64) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch encoding {
	case "gzip", "x-gzip":
		reader, err = gzip.NewReader(bytes.NewReader(raw))
	case "deflate":
		reader = flate.NewReader(bytes.NewReader(raw))
	case "br", "zstd":
		// These encodings are recognized so callers get a clear protocol error;
		// the gateway's dependency-light build currently decodes gzip/deflate.
		return nil, fmt.Errorf("unsupported content encoding %q", encoding)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	buf := bytes.NewBuffer(nil)
	written, copyErr := io.CopyN(buf, reader, limit+1)
	if copyErr != nil && copyErr != io.EOF {
		return nil, copyErr
	}
	if written > limit {
		return raw, nil
	}
	return buf.Bytes(), nil
}

func isCompressed(r *http.Request) bool {
	enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	return enc != "" && enc != "identity"
}
