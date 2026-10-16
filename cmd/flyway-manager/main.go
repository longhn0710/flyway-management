package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flyway-manager/internal/flyway"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flyway-manager/internal/config"
	"flyway-manager/internal/core"
	"flyway-manager/internal/database"
	"flyway-manager/internal/httpapi"
	"flyway-manager/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "runtime-install" {
		return installRuntime(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		return backupData(os.Args[2:])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level := new(slog.LevelVar)
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	service, err := core.NewWithMasterKeyFile(ctx, db, cfg.DataDir, cfg.PublicURL, cfg.MasterKeyFile)
	if err != nil {
		return err
	}
	runtimePin := fmt.Sprintf("%x", sha256.Sum256([]byte(cfg.Flyway.Version+":"+cfg.Flyway.SHA256)))
	if err = service.StartExecution(ctx, flyway.New(cfg.Flyway, cfg.DataDir), runtimePin, cfg.DataDir, cfg.ArtifactLimits); err != nil {
		return err
	}
	defer service.StopExecution()
	if required, _ := service.BootstrapRequired(ctx); required {
		logger.Info("first-run setup required; read the private setup token file", "bootstrap_token_file", service.SetupTokenFile)
	}
	assets, err := web.Dist()
	if err != nil {
		return err
	}
	handler := httpapi.NewRouter(httpapi.RouterOptions{Database: db, Service: service, Assets: assets, Logger: logger})
	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return err
	}
	if addr, ok := listener.Addr().(*net.TCPAddr); ok && !addr.IP.IsLoopback() {
		logger.Warn("server is listening on a non-loopback interface; configure public_url and a trusted HTTPS reverse proxy, and deny direct backend access", "address", listener.Addr().String())
	}
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", listener.Addr().String(), "installation_id", db.InstallationID)
		serveErr <- server.Serve(listener)
	}()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
