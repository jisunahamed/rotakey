package app

import (
	"sync"

	tiktoken "github.com/tiktoken-go/tokenizer"
)

var (
	cl100kOnce  sync.Once
	cl100kCodec tiktoken.Codec
	cl100kErr   error

	o200kOnce  sync.Once
	o200kCodec tiktoken.Codec
	o200kErr   error
)

// estimateInputTokens uses the configured model tokenizer when one is known.
// The JSON request itself is tokenized, which conservatively includes payload
// structure, and a small allowance covers provider-side message framing.
func estimateInputTokens(requestBody []byte, profile string) int64 {
	fallback := int64(len(requestBody)/3 + 16)

	var (
		codec tiktoken.Codec
		err   error
	)
	switch profile {
	case "cl100k_base":
		cl100kOnce.Do(func() {
			cl100kCodec, cl100kErr = tiktoken.Get(tiktoken.Cl100kBase)
		})
		codec, err = cl100kCodec, cl100kErr
	case "o200k_base":
		o200kOnce.Do(func() {
			o200kCodec, o200kErr = tiktoken.Get(tiktoken.O200kBase)
		})
		codec, err = o200kCodec, o200kErr
	default:
		return fallback
	}
	if err != nil || codec == nil {
		return fallback
	}

	count, err := codec.Count(string(requestBody))
	if err != nil {
		return fallback
	}
	estimate := int64(count + 16)
	if estimate < 1 {
		return 1
	}
	return estimate
}
