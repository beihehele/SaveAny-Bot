package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/admin"
	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/client/bot"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/spf13/cobra"
)

func Run(cmd *cobra.Command, _ []string) {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.InfoLevel,
		ReportTimestamp: true,
		TimeFormat:      time.TimeOnly,
		ReportCaller:    true,
	})
	log.SetDefault(logger)
	ctx = log.WithContext(ctx, logger)

	configFile := config.GetConfigFile(cmd)
	if err := config.Init(ctx, configFile); err != nil {
		if ctx.Err() != nil {
			logger.Info("Startup cancelled")
			return
		}
		logger.Fatal("Init failed", "error", err)
	}

	level, err := log.ParseLevel(strings.TrimSpace(config.C().Log.Level))
	if err != nil {
		logger.Warn("Invalid log level, fallback to debug", "level", config.C().Log.Level, "error", err)
		level = log.DebugLevel
	}
	logger.SetLevel(level)

	exitChan, err := initAll(ctx)
	if err != nil {
		if ctx.Err() != nil {
			logger.Info("Startup cancelled")
			return
		}
		logger.Fatal("Init failed", "error", err)
	}
	go func() {
		select {
		case <-exitChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	workersDone := core.Run(ctx)

	<-ctx.Done()
	logger.Info("Exiting...")
	defer logger.Info("Exit complete")
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-workersDone:
	case <-timer.C:
		logger.Warn("Task shutdown deadline exceeded; temporary files are preserved")
	}
	// Tasks remove their own temporary files. Never recursively clear a configured
	// directory here: it may contain unrelated files or tasks still shutting down.
}

func initAll(ctx context.Context) (<-chan struct{}, error) {
	cache.Init()
	logger := log.FromContext(ctx)
	i18n.Init(config.C().Lang)
	logger.Info("Initializing...")
	database.Init(ctx)
	if err := database.ValidateStorageReferences(ctx); err != nil {
		return nil, err
	}
	storage.LoadStorages(ctx)
	core.Prepare()
	if config.C().Telegram.Userbot.Enable {
		_, err := userclient.Login(ctx)
		if err != nil {
			return nil, fmt.Errorf("user login failed: %w", err)
		}
	}
	exitChan := bot.Init(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err := api.Start(ctx); err != nil {
		logger.Error("Failed to start API server", "error", err)
	}
	if err := admin.Start(ctx); err != nil {
		logger.Error("Failed to start admin console", "error", err)
	}
	return exitChan, nil
}
