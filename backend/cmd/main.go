package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lyrics/internal/auth"
	"lyrics/internal/cache"
	"lyrics/internal/cors"
	"lyrics/internal/domain"
	"lyrics/internal/httpapi"
	"lyrics/internal/logging"
	"lyrics/internal/metrics"
	"lyrics/internal/ports"
	"lyrics/internal/postgres"
	"lyrics/internal/provider"
	"lyrics/internal/render"
	"lyrics/internal/service"
	"lyrics/internal/storage"
	"lyrics/internal/youtube"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application_stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dotEnvFile, err := loadLocalDotEnv()
	if err != nil {
		return fmt.Errorf("load local environment: %w", err)
	}

	logger, logFile, err := newProductionLogger()
	if err != nil {
		return err
	}
	if logFile != nil {
		defer logFile.Close()
	}
	logging.ConfigureStandardLogger(logger)
	if dotEnvFile != "" {
		logger.Info("local_environment_loaded", "file", dotEnvFile)
	}

	persistence, closePersistence, err := openPersistence(context.Background())
	if err != nil {
		return err
	}
	defer closePersistence()

	apiServer, metricsServer := newServers(logger, persistence)
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	apiErrors := make(chan error, 1)
	go func() {
		logger.Info("metrics_server_starting", "addr", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics_server_failed", "addr", metricsServer.Addr, "error", err)
		}
	}()
	go func() {
		logger.Info("api_server_starting", "addr", apiServer.Addr)
		if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiErrors <- err
		}
	}()

	configuration := []any{
		"api_addr", apiServer.Addr,
		"metrics_addr", metricsServer.Addr,
		"cors_origins_count", len(configuredCORSOrigins()),
		"youtube_api_key_configured", strings.TrimSpace(os.Getenv("YOUTUBE_API_KEY")) != "",
		"log_level", strings.ToLower(strings.TrimSpace(envOr("LYRICS_LOG_LEVEL", "info"))),
		"log_file_enabled", strings.TrimSpace(os.Getenv("LYRICS_LOG_FILE")) != "",
		"ffmpeg_dir", envOr("LYRICS_FFMPEG_DIR", filepath.Join("..", "tools")),
		"assets_dir", envOr("LYRICS_ASSETS_DIR", "assets"),
		"render_dir", envOr("LYRICS_RENDER_DIR", filepath.Join("data", "renders")),
	}
	configuration = append(configuration, persistenceLogAttrs()...)
	logger.Info("application_config", configuration...)
	select {
	case <-shutdownContext.Done():
		logger.Info("shutdown_requested")
	case err := <-apiErrors:
		return fmt.Errorf("API listener: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := apiServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown API: %w", err)
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown metrics: %w", err)
	}
	return nil
}

func loadLocalDotEnv() (string, error) {
	for _, filename := range []string{".env", filepath.Join("backend", ".env")} {
		info, err := os.Stat(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			continue
		}
		if err := loadEnvironmentFile(filename); err != nil {
			return "", err
		}
		return filename, nil
	}
	return "", nil
}

func loadEnvironmentFile(filename string) error {
	if _, err := os.Stat(filename); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := godotenv.Load(filename); err != nil {
		return fmt.Errorf("load %s: %w", filename, err)
	}
	return nil
}

func openPersistence(ctx context.Context) (ports.Persistence, func() error, error) {
	driver := strings.ToLower(strings.TrimSpace(envOr("LYRICS_STORAGE_DRIVER", "sqlite")))
	switch driver {
	case "sqlite":
		databasePath := envOr("LYRICS_DB_PATH", filepath.Join("data", "lyrics.db"))
		store, err := storage.Open(databasePath)
		if err != nil {
			return nil, nil, fmt.Errorf("open SQLite database: %w", err)
		}
		return store, store.Close, nil
	case "postgres", "postgresql":
		databaseURL := strings.TrimSpace(os.Getenv("LYRICS_DATABASE_URL"))
		if databaseURL == "" {
			databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
		}
		if databaseURL == "" {
			return nil, nil, errors.New("LYRICS_DATABASE_URL or DATABASE_URL is required when LYRICS_STORAGE_DRIVER=postgres")
		}
		store, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			return nil, nil, fmt.Errorf("open PostgreSQL database: %w", err)
		}
		return store, store.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported LYRICS_STORAGE_DRIVER %q (expected sqlite or postgres)", driver)
	}
}

func persistenceLogAttrs() []any {
	driver := strings.ToLower(strings.TrimSpace(envOr("LYRICS_STORAGE_DRIVER", "sqlite")))
	if driver == "sqlite" {
		return []any{
			"storage_driver", driver,
			"database_path", envOr("LYRICS_DB_PATH", filepath.Join("data", "lyrics.db")),
		}
	}
	if driver != "postgres" && driver != "postgresql" {
		return []any{"storage_driver", driver}
	}

	databaseURL := strings.TrimSpace(os.Getenv("LYRICS_DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	connection, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return []any{"storage_driver", "postgres", "database_target_parsed", false}
	}
	return []any{
		"storage_driver", "postgres",
		"database_host", connection.Host,
		"database_port", connection.Port,
		"database_name", connection.Database,
		"database_tls_enabled", connection.TLSConfig != nil,
	}
}

func newServer(stores ...ports.Persistence) *http.Server {
	apiServer, _ := newServers(slog.Default(), stores...)
	return apiServer
}

func newServers(logger *slog.Logger, stores ...ports.Persistence) (*http.Server, *http.Server) {
	var lyricsProvider domain.LyricsProvider = provider.NewLRCLIB(http.DefaultClient)
	var videoSearcher domain.VideoSearcher = youtube.New(http.DefaultClient, os.Getenv("YOUTUBE_API_KEY"))
	if len(stores) > 0 && stores[0] != nil {
		lyricsProvider = cache.NewLyricsProvider(lyricsProvider, stores[0])
		videoSearcher = cache.NewVideoSearcher(videoSearcher, stores[0])
	}
	lyricsService := service.NewLyricsService(lyricsProvider, videoSearcher)
	lyricsHandler := httpapi.NewLyricsHandler(lyricsService)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/lyrics", lyricsHandler.Get)
	mux.HandleFunc("/api/search", lyricsHandler.Search)
	mux.HandleFunc("/api/video", lyricsHandler.Video)
	if len(stores) > 0 && stores[0] != nil {
		authenticated := auth.NewService(stores[0])
		library := service.NewLibraryService(stores[0])
		accountHandler := httpapi.NewAccountHandler(authenticated, library)
		mux.HandleFunc("POST /api/auth/register", accountHandler.Register)
		mux.HandleFunc("POST /api/auth/login", accountHandler.Login)
		mux.Handle("POST /api/auth/logout", authenticated.Middleware(http.HandlerFunc(accountHandler.Logout)))
		mux.Handle("GET /api/me", authenticated.Middleware(http.HandlerFunc(accountHandler.Me)))
		mux.Handle("GET /api/playlists", authenticated.Middleware(http.HandlerFunc(accountHandler.Playlists)))
		mux.Handle("GET /api/library", authenticated.Middleware(http.HandlerFunc(accountHandler.Library)))
		mux.Handle("POST /api/playlists", authenticated.Middleware(http.HandlerFunc(accountHandler.CreatePlaylist)))
		mux.Handle("POST /api/library/tracks", authenticated.Middleware(http.HandlerFunc(accountHandler.RegisterTrack)))
		mux.Handle("PUT /api/tracks/{trackId}/save", authenticated.Middleware(http.HandlerFunc(accountHandler.SaveTrack)))
		mux.Handle("PUT /api/tracks/{trackId}/reaction", authenticated.Middleware(http.HandlerFunc(accountHandler.Reaction)))
		mux.Handle("POST /api/playlists/{playlistId}/tracks", authenticated.Middleware(http.HandlerFunc(accountHandler.AddToPlaylist)))
	}

	var renderHandler *httpapi.RenderHandler
	if len(stores) > 0 {
		renderHandler = newRenderHandler(lyricsProvider, stores[0])
	}
	if renderHandler != nil {
		mux.HandleFunc("POST /api/renders", renderHandler.Create)
		mux.HandleFunc("POST /api/renders/preview", renderHandler.Preview)
		mux.HandleFunc("GET /api/renders/source", renderHandler.Source)
		mux.HandleFunc("GET /api/renders/{id}", renderHandler.Status)
		mux.HandleFunc("GET /api/renders/{id}/file", renderHandler.File)
	} else {
		log.Printf("render=disabled reason=ffmpeg-no-disponible")
	}

	httpMetrics := metrics.NewHTTP()
	handler := logging.Middleware(httpMetrics.Middleware(cors.MiddlewareWithOrigins(mux, configuredCORSOrigins())), logger)

	apiServer := &http.Server{Addr: configuredHTTPAddress(), Handler: handler}
	metricsServer := &http.Server{
		Addr:    envOr("LYRICS_METRICS_ADDR", "127.0.0.1:9090"),
		Handler: httpMetrics.Handler(),
	}
	return apiServer, metricsServer
}

func configuredHTTPAddress() string {
	if address := strings.TrimSpace(os.Getenv("LYRICS_HTTP_ADDR")); address != "" {
		return address
	}
	return net.JoinHostPort("0.0.0.0", envOr("PORT", "8080"))
}

func configuredCORSOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("LYRICS_CORS_ORIGINS"))
	if raw == "" {
		return []string{"*"}
	}

	origins := make([]string, 0)
	for _, origin := range strings.Split(raw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func newProductionLogger() (*slog.Logger, *lumberjack.Logger, error) {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LYRICS_LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	writers := []io.Writer{os.Stdout}
	var logFile *lumberjack.Logger
	if filename := strings.TrimSpace(os.Getenv("LYRICS_LOG_FILE")); filename != "" {
		if directory := filepath.Dir(filename); directory != "." {
			if err := os.MkdirAll(directory, 0o750); err != nil {
				return nil, nil, fmt.Errorf("create log directory: %w", err)
			}
		}
		logFile = &lumberjack.Logger{
			Filename:   filename,
			MaxSize:    100,
			MaxBackups: 10,
			MaxAge:     30,
			Compress:   false,
		}
		writers = append(writers, logFile)
	}

	return logging.NewJSONLogger(io.MultiWriter(writers...), level), logFile, nil
}

func newRenderHandler(lyricsProvider domain.LyricsProvider, store ports.RenderRepository) *httpapi.RenderHandler {
	if store == nil {
		return nil
	}

	toolsDir := envOr("LYRICS_FFMPEG_DIR", filepath.Join("..", "tools"))
	assetsDir := envOr("LYRICS_ASSETS_DIR", "assets")
	renderDir := envOr("LYRICS_RENDER_DIR", filepath.Join("data", "renders"))

	engine := render.NewEngine(toolsDir, assetsDir, renderDir)
	if err := engine.Available(); err != nil {
		log.Printf("render=disabled error=%q dir=%q", err, toolsDir)
		return nil
	}

	// El audio de un link de YouTube es opcional: si no está el binario, el
	// render sigue funcionando con archivo o solo letra, y lo dice al pedir el
	// link en vez de apagar todo el módulo.
	var audioSource domain.AudioSource
	downloader := youtube.NewDownloader(toolsDir, filepath.Join(renderDir, "youtube"))
	if err := downloader.Available(); err != nil {
		log.Printf("render=source-disabled error=%q", err)
	} else {
		audioSource = downloader
	}

	renderService, err := service.NewRenderService(lyricsProvider, engine, engine, audioSource, store, renderDir)
	if err != nil {
		log.Printf("render=disabled error=%q", err)
		return nil
	}

	log.Printf("render=ready dir=%q output=%q source=%t", assetsDir, renderDir, audioSource != nil)
	return httpapi.NewRenderHandler(renderService)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
