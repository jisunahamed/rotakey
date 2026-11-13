package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jisunahamed/rotakey/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	key, err := base64.StdEncoding.DecodeString(os.Getenv("APP_MASTER_KEY"))
	if err != nil || len(key) != 32 {
		fmt.Fprintln(os.Stderr, "archive master key is invalid")
		os.Exit(1)
	}
	result, err := app.ArchiveRequests(ctx, os.Getenv("DATABASE_URL"), os.Getenv("ARCHIVE_DATABASE_URL"), os.Getenv("ARCHIVE_INSTANCE_ID"), key, func(r app.ArchiveResult) { _ = json.NewEncoder(os.Stdout).Encode(r) })
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "completed", "copied": result.Copied, "deleted": result.Deleted})
}
