package main

import (
	"context"
	"fmt"
	"gokit/internal/app"
	"gokit/internal/user"
	"gokit/pkg/config"
	"gokit/pkg/logger"
	"gokit/pkg/postgres"
	"gokit/pkg/recovery"
	"gokit/pkg/telemetry"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.opentelemetry.io/otel"

	_ "gokit/docs"

	router "gokit/pkg/http"

	pgdb "gokit/pkg/postgres/db"

	cv "gokit/pkg/validator"
)

// @title          Restful Boilerplate API
// @version        1.0
// @description    Go RESTful API boilerplate built on net/http + PostgreSQL.
// @host           localhost:4040
// @BasePath       /v1/api
// @schemes        http
func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cfg := config.Load(os.Getenv)

	// Unified telemetry: traces + metrics + logs (all via OTLP to SigNoz).
	stopTelemetry, err := telemetry.SetupAll(ctx, cfg.LogFormat)
	if err != nil {
		return fmt.Errorf("setup telemetry: %w", err)
	}
	defer stopTelemetry()

	pool, err := postgres.OpenDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	a := &app.App{
		Queries:   pgdb.New(pool),
		Validator: cv.New(),
		Tracer:    otel.GetTracerProvider(),
	}

	r := router.NewRouter(router.WithInstrumentation("http.server"))
	r.Use(logger.Middleware)
	r.Use(recovery.Middleware)

	if err != nil {
		return fmt.Errorf("recipe module: %w", err)
	}

	r.Group("/v1", func(g *router.Group) {
		g.Prefix("/api")
		g.ANY("/swagger/", httpSwagger.WrapHandler)
		g.Group("/users", user.NewModule(a).RegisterRoutes)
	})

	addr := net.JoinHostPort(cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      r.Handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	router.GracefulServe(ctx, httpServer, 10*time.Second)
	return nil
}
