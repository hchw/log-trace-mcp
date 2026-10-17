// Command log-trace-mcp runs the unified log query MCP server and admin page.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/hchw/log-trace-mcp/internal/admin"
	"github.com/hchw/log-trace-mcp/internal/config"
	"github.com/hchw/log-trace-mcp/internal/crypto"
	"github.com/hchw/log-trace-mcp/internal/manager"
	logmcp "github.com/hchw/log-trace-mcp/internal/mcp"
	"github.com/hchw/log-trace-mcp/internal/sdk"
	"github.com/hchw/log-trace-mcp/internal/store"
)

const (
	appName = "log-trace-mcp"
)

// appVersion is overridable at build time via -ldflags "-X main.appVersion=...".
var appVersion = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "version":
		fmt.Printf("%s %s\n", appName, appVersion)
		return nil
	case "tools":
		return runTools()
	case "rekey":
		return runRekey(args)
	case "serve":
		return runServe(args)
	default:
		return fmt.Errorf("unknown command %q (want: serve, tools, rekey, version)", cmd)
	}
}

type app struct {
	cfg    config.Config
	store  *store.Store
	mgr    *manager.Manager
	svc    *sdk.Service
	mcp    *logmcp.Server
	logger *slog.Logger
}

func newApp() (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		st.Close()
		return nil, err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	reg := sdk.NewRegistry()
	mgr := manager.New(st, cipher, reg, logger)
	if err := mgr.Reload(); err != nil {
		logger.Warn("initial reload had errors", "err", err)
	}
	mgr.MarkDataVersion()
	svc := sdk.NewService(reg)
	mcpSrv := logmcp.New(svc, reg, appName, appVersion)
	mcpSrv.Register()
	mcpSrv.Refresh()
	return &app{cfg: cfg, store: st, mgr: mgr, svc: svc, mcp: mcpSrv, logger: logger}, nil
}

func (a *app) close() {
	if a.mgr != nil {
		_ = a.mgr.Registry().Close()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}

func runTools() error {
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()
	for _, name := range a.mcp.RegisteredTools() {
		fmt.Println(name)
	}
	return nil
}

func runRekey(args []string) error {
	fs := flag.NewFlagSet("rekey", flag.ContinueOnError)
	key := fs.String("key", "", "new master key (base64, 32 bytes); or set LOG_TRACE_NEW_MASTER_KEY")
	if err := fs.Parse(args); err != nil {
		return err
	}
	newKey := *key
	if newKey == "" {
		newKey = os.Getenv("LOG_TRACE_NEW_MASTER_KEY")
	}
	if newKey == "" {
		return errors.New("new master key is required (--key or LOG_TRACE_NEW_MASTER_KEY)")
	}
	raw, err := base64.StdEncoding.DecodeString(newKey)
	if err != nil {
		if len(newKey) == 32 {
			raw = []byte(newKey)
		} else {
			return fmt.Errorf("new key must be base64-encoded 32 bytes: %w", err)
		}
	}
	if len(raw) != 32 {
		return fmt.Errorf("new key must be 32 bytes, got %d", len(raw))
	}
	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()
	if err := a.mgr.Rekey(raw); err != nil {
		return err
	}
	fmt.Println("rekey complete")
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	stdio := fs.Bool("stdio", false, "serve MCP over stdio (in addition to the admin and HTTP endpoints)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	a, err := newApp()
	if err != nil {
		return err
	}
	defer a.close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go a.mgr.Watch(ctx, 2*time.Second)

	// Admin page.
	adminSrv := admin.New(a.mgr, a.cfg.AdminToken, a.mcp.RegisteredTools, a.mcp.Refresh)
	adminHTTP := &http.Server{Addr: a.cfg.AdminAddr, Handler: adminSrv.Handler()}
	go func() {
		a.logger.Info("admin listening", "addr", a.cfg.AdminAddr)
		if err := adminHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("admin server", "err", err)
		}
	}()

	// MCP streamable HTTP.
	httpMCP := mcpserver.NewStreamableHTTPServer(a.mcp.MCPServer())
	go func() {
		a.logger.Info("mcp http listening", "addr", a.cfg.MCPHTTPAddr)
		if err := httpMCP.Start(a.cfg.MCPHTTPAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("mcp http server", "err", err)
		}
	}()

	if *stdio {
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = adminHTTP.Shutdown(shutdownCtx)
			_ = httpMCP.Shutdown(shutdownCtx)
		}()
		a.logger.Info("serving mcp over stdio")
		return mcpserver.ServeStdio(a.mcp.MCPServer())
	}

	<-ctx.Done()
	a.logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = adminHTTP.Shutdown(shutdownCtx)
	_ = httpMCP.Shutdown(shutdownCtx)
	return nil
}
