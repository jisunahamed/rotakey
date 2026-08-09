package app

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestDecompressBodySupportedEncodings(t *testing.T) {
	plain := []byte(`{"model":"demo","input":"hello"}`)
	encodings := map[string]func([]byte) []byte{
		"gzip": func(input []byte) []byte {
			var output bytes.Buffer
			writer := gzip.NewWriter(&output)
			_, _ = writer.Write(input)
			_ = writer.Close()
			return output.Bytes()
		},
		"br": func(input []byte) []byte {
			var output bytes.Buffer
			writer := brotli.NewWriter(&output)
			_, _ = writer.Write(input)
			_ = writer.Close()
			return output.Bytes()
		},
		"zstd": func(input []byte) []byte {
			var output bytes.Buffer
			writer, _ := zstd.NewWriter(&output)
			_, _ = writer.Write(input)
			writer.Close()
			return output.Bytes()
		},
	}
	for encoding, encode := range encodings {
		t.Run(encoding, func(t *testing.T) {
			got, err := decompressBody(encoding, encode(plain), 1024)
			if err != nil || !bytes.Equal(got, plain) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestDecompressBodyEnforcesDecodedLimit(t *testing.T) {
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	_, _ = writer.Write(bytes.Repeat([]byte("x"), 4096))
	_ = writer.Close()
	if _, err := decompressBody("gzip", output.Bytes(), 128); err == nil {
		t.Fatal("expected decoded size limit error")
	}
}
