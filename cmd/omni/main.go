package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tombuente/omni/internal/discord"
)

var (
	botToken       = os.Getenv("BOT_TOKEN")
	guild          = os.Getenv("GUILD")
	deleteCommands = os.Getenv("DELETE_COMMANDS")

	postgresHost     = os.Getenv("POSTGRES_HOST")
	postgresPort     = os.Getenv("POSTGRES_PORT")
	postgresUser     = os.Getenv("POSTGRES_USER")
	postgresPassword = os.Getenv("POSTGRES_PASSWORD")
	postgresDB       = os.Getenv("POSTGRES_DB")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var wg sync.WaitGroup

	// postgres://username:password@host:port/database_name
	connString := fmt.Sprintf("postgres://%v:%v@%v:%v/%v", postgresUser, postgresPassword, postgresHost, postgresPort, postgresDB)
	pool, err := pgxpool.New(context.Background(), connString)
	if err != nil {
		slog.Error("Unable to connect to database", "error", err)
		os.Exit(1)
	}

	var delCmds bool
	if deleteCommands != "" {
		delCmds, err = strconv.ParseBool(deleteCommands)
		if err != nil {
			slog.Error("Unable to parse remove commands env var", "error", err)
			os.Exit(1)
		}
	}

	db := discord.MakeDatabase(pool)
	config := discord.Config{
		Token:          botToken,
		Guild:          guild,
		DeleteCommands: delCmds,
	}
	b, err := discord.Make(config, db)
	if err != nil {
		slog.Error("Unable to make bot instance", "error", err)
		os.Exit(1)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := b.Run(ctx); err != nil {
			slog.Error("Encountered an error while running the bot", "error", err)
		}
	}()

	slog.Info("Press Ctrl+C to exit")

	<-ctx.Done()
	slog.Info("Gracefully shutting down...")
	stop()

	wg.Wait()
}
