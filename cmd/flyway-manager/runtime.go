package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"flyway-manager/internal/config"
	"flyway-manager/internal/flyway"
	"os"
	"time"
)

// Installs only operator-supplied, approved upstream bytes. No download or
// redistribution permission is inferred from an installation request.
func installRuntime(args []string) error {
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	flags := flag.NewFlagSet("runtime-install", flag.ContinueOnError)
	source := flags.String("source", "", "authorized unpacked Flyway distribution directory")
	executable := flags.String("executable", "flyway", "executable relative to the distribution root")
	version := flags.String("version", "", "approved exact Flyway version")
	checksum := flags.String("sha256", "", "approved SHA-256 of flyway-manifest.sha256")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 || *source == "" || *version == "" || *checksum == "" {
		return errors.New("runtime-install requires --source, --version and --sha256")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	installed, e := flyway.Install(ctx, *source, *executable, *version, *checksum, cfg.DataDir)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"flyway": installed})
}
