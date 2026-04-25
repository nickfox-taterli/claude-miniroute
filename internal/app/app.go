package app

import (
	"context"
	"net/http"
	"time"

	"miniroute/internal/adminapi"
	"miniroute/internal/config"
	"miniroute/internal/cooldown"
	"miniroute/internal/proxy"
	"miniroute/internal/query"
	"miniroute/internal/store/sqlite"
)

type App struct {
	proxyServer *http.Server
	adminServer *http.Server
	store       *sqlite.Store
	reloader    *config.Reloader
}

func New(cfg *config.Config, cfgPath string) (*App, error) {
	store, err := sqlite.Open(cfg.Storage.SQLitePath)
	if err != nil {
		return nil, err
	}

	ct := cooldown.NewTracker()
	ct.RegisterProvider("MiniMax", cooldown.NewMiniMaxCooldown())
	ct.RegisterProvider("GLM", cooldown.NewGLMCooldown())

	reloader := config.NewReloader(cfg, cfgPath)

	started := time.Now()
	proxyHandler := proxy.New(reloader, store, ct)
	q := query.New(store, started, proxyHandler.Inflight)
	adminHandler := adminapi.New(q, reloader, ct)

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/", proxyHandler.Routes())

	adminMux := http.NewServeMux()
	adminHandler.RegisterRoutes(adminMux)
	adminapi.RegisterFrontend(adminMux)

	proxySrv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      accessLogMiddleware("proxy", proxy.MiddlewareInjectWriter(proxyMux, proxyHandler)),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutMS) * time.Millisecond,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeoutMS) * time.Millisecond,
		WriteTimeout: 0,
	}
	adminSrv := &http.Server{
		Addr:         cfg.Server.AdminListen,
		Handler:      accessLogMiddleware("admin", adminMux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return &App{proxyServer: proxySrv, adminServer: adminSrv, store: store, reloader: reloader}, nil
}

func (a *App) Run(ctx context.Context) error {
	go a.reloader.StartWatching(5 * time.Second)

	errCh := make(chan error, 2)
	go func() {
		if err := a.proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		if err := a.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return a.Shutdown(context.Background())
	case err := <-errCh:
		_ = a.Shutdown(context.Background())
		return err
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	a.reloader.Stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_ = a.adminServer.Shutdown(ctx)
	_ = a.proxyServer.Shutdown(ctx)
	return a.store.Close()
}
