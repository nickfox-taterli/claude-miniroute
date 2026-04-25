package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"miniroute/internal/app"
	"miniroute/internal/config"
)

func main() {
	var cfgPath string
	flag.StringVar(&cfgPath, "config", "./config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	a, err := app.New(cfg, cfgPath)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("miniroute started proxy=%s admin=%s", cfg.Server.Listen, cfg.Server.AdminListen)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("run app: %v", err)
	}
	log.Printf("miniroute stopped")
}
