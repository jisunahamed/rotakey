package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jisunahamed/rotakey/internal/app"
)

func main() {
	cfg, e := app.LoadConfig()
	if e == nil {
		e = app.ReplaceDriveTask(context.Background(), cfg, os.Getenv("DRIVE_REPLACEMENT_RUN_ID"), "/work/body-only-replacement.json")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
