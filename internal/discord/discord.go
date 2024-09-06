package discord

import (
	"context"
	"database/sql"
	"fmt"

	dgo "github.com/bwmarrin/discordgo"
)

type Discord struct {
	session *dgo.Session
	db      Database
	config  runtimeConfig
}

type Config struct {
	Token          string
	Guild          string
	DeleteCommands bool
}

type runtimeConfig struct {
	guild          string
	deleteCommands bool
}

type CreatorChannel struct {
	ID      string `db:"id"`
	GuildID string `db:"guild_id"`
}

type creatorChannelFilter struct {
	guildID string
}

type TemporaryChannel struct {
	ID      string `db:"id"`
	GuildID string `db:"guild_id"`
}

type Group struct {
	ID      int64  `db:"id"`
	Name    string `db:"name"`
	GuildID string `db:"guild_id"`
}

type GroupParams struct {
	name    string
	guildID string
}

type GroupFilter struct {
	guildID sql.NullString
}

func Make(config Config, db Database) (Discord, error) {
	session, err := dgo.New(fmt.Sprintf("Bot %v", config.Token))
	if err != nil {
		return Discord{}, fmt.Errorf("unable to create session: %w", err)
	}

	session.Identify.Intents = dgo.IntentGuilds | dgo.IntentGuildVoiceStates

	return Discord{
		session: session,
		db:      db,
		config: runtimeConfig{
			guild:          config.Guild,
			deleteCommands: config.DeleteCommands,
		},
	}, nil
}

func (d Discord) Run(ctx context.Context) error {
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("unable to open session: %w", err)
	}
	defer d.session.Close()

	if d.config.deleteCommands {
		defer d.deleteCommands()
	}

	d.commands()
	d.voiceStates()

	<-ctx.Done()

	return nil
}
