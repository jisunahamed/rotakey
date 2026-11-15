package app

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestThinkingStreamReachesClaudeBeforeProviderCompletion(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning"} {
		t.Run(field, func(t *testing.T) {
			source, provider := io.Pipe()
			defer source.Close()
			defer provider.Close()
			target, translated := io.Pipe()
			defer target.Close()
			defer translated.Close()
			done := make(chan error, 1)
			go func() {
				_, err := translateOpenAIStreamToAnthropic(source, translated, "test", nil)
				translated.CloseWithError(err)
				done <- err
			}()
			go func() { io.WriteString(provider, "data: {\"choices\":[{\"delta\":{\""+field+"\":\"step one\"}}]}\n\n") }()
			got := make(chan string, 1)
			go func() {
				var b strings.Builder
				buf := make([]byte, 2048)
				for {
					n, e := target.Read(buf)
					b.Write(buf[:n])
					if strings.Contains(b.String(), `"thinking":"step one"`) {
						got <- b.String()
						return
					}
					if e != nil {
						got <- b.String()
						return
					}
				}
			}()
			select {
			case text := <-got:
				if !strings.Contains(text, "thinking_delta") {
					t.Fatal(text)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("thinking buffered until upstream completion")
			}
			go func() {
				io.WriteString(provider, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n")
				provider.Close()
			}()
			tail, err := io.ReadAll(target)
			if err != nil {
				t.Fatal(err)
			}
			stop := strings.Index(string(tail), `"type":"content_block_stop"`)
			text := strings.Index(string(tail), `"type":"text"`)
			if stop < 0 || text < 0 || stop > text {
				t.Fatalf("thinking block not closed before answer: %s", tail)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
