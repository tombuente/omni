package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	dgo "github.com/bwmarrin/discordgo"
)

type Bot struct {
	session *dgo.Session

	db Database
}

var commands = []*dgo.ApplicationCommand{
	{
		Name:        "creator-channel",
		Description: "Creates a reactor channel",
		Options: []*dgo.ApplicationCommandOption{
			{
				Type:        dgo.ApplicationCommandOptionInteger,
				Name:        "user-limit",
				Description: "User limit",
				MinValue:    newIntOption(1),
				Required:    false,
			},
		},
	},
}

func Make(token string, db Database) (Bot, error) {
	session, err := dgo.New(fmt.Sprintf("Bot %v", token))
	if err != nil {
		return Bot{}, fmt.Errorf("unable to create session: %w", err)
	}

	session.Identify.Intents = dgo.IntentGuilds | dgo.IntentGuildVoiceStates

	return Bot{
		session: session,
		db:      db,
	}, nil
}

func (b Bot) Run(stop chan os.Signal) error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("unable to open session: %w", err)
	}
	defer b.session.Close()

	appID := b.session.State.User.ID
	for _, cmd := range commands {
		_, err := b.session.ApplicationCommandCreate(appID, "1242245611298885683", cmd)
		if err != nil {
			slog.Error("Unable to create application command", "command", cmd.Name, "error", err)
		}
	}

	b.session.AddHandler(voiceStateUpdate(b.voiceStateUpdateCreatorChannel))
	b.session.AddHandler(voiceStateUpdate(b.voiceStateUpdateTemporaryVoiceChannel))

	cmdHandlers := make(map[string]func(s *dgo.Session, i *dgo.InteractionCreate), len(commands))
	cmdHandlers["creator-channel"] = cmd(b.cmdCreatorChannel)
	b.session.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if h, ok := cmdHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	<-stop
	return nil
}

func (b Bot) voiceStateUpdateCreatorChannel(e *dgo.VoiceStateUpdate) error {
	if e.ChannelID == "" {
		// User left the voice channel
		return nil
	}

	if _, err := b.db.creatorChannel(context.Background(), e.ChannelID); err != nil {
		return fmt.Errorf("unable to query creator channel (id=%v) from database: %w", e.ChannelID, err)
	}

	creatorChannel, err := b.session.Channel(e.ChannelID)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)

	}

	tempVoiceChannel, err := b.session.GuildChannelCreateComplex(
		creatorChannel.GuildID,
		dgo.GuildChannelCreateData{
			Name:      e.Member.User.Username,
			Type:      dgo.ChannelTypeGuildVoice,
			UserLimit: creatorChannel.UserLimit,
			Position:  creatorChannel.Position + 1,
			ParentID:  creatorChannel.ParentID,
		})
	if err != nil {
		return fmt.Errorf("unable to create temporary voice channel: %w", err)
	}

	if _, err := b.db.createTemporaryVoiceChannel(context.Background(), TemporaryVoiceChannel{ID: tempVoiceChannel.ID}); err != nil {
		// In an effort to keep Discord and the database in sync, try to delete the temporary voice channel.
		if _, err := b.session.ChannelDelete(tempVoiceChannel.ID); err != nil {
			return fmt.Errorf("unable to delete temporary voice channel, it is not tracked in the database")
		}

		return fmt.Errorf("unable to create temporary voice channel in database: %w", err)
	}

	if err := b.session.GuildMemberMove(e.GuildID, e.UserID, &tempVoiceChannel.ID); err != nil {
		// Remove orphan
		if _, err := b.session.ChannelDelete(tempVoiceChannel.ID); err != nil {
			return fmt.Errorf("unable to delete temporary voice channel, it's now an orphan: %w", err)
		}

		return fmt.Errorf("unable to move user to temporary voice channel: %w", err)
	}

	return nil
}

func (b Bot) voiceStateUpdateTemporaryVoiceChannel(e *dgo.VoiceStateUpdate) error {
	guild, err := b.session.State.Guild(e.GuildID)
	if err != nil {
		return fmt.Errorf("unable to get guild from state cache: %w", err)

	}

	if e.BeforeUpdate == nil {
		// User joined a channel without having been in a channel previously
		return nil
	}

	channelID := e.BeforeUpdate.ChannelID
	channelHasUsers := channelHasUsers(guild, channelID)
	if channelHasUsers {
		// Must not delete a temporary voice channel if users are still in it
		return nil
	}

	_, err = b.db.temporaryVoiceChannel(context.Background(), channelID)
	if err != nil {
		return fmt.Errorf("unable to query temporary voice channel (id=%v) from database: %w", channelID, err)
	}

	if _, err := b.session.ChannelDelete(channelID); err != nil {
		return fmt.Errorf("unable to delete voice channel: %w", err)

	}

	return nil
}

func (b Bot) cmdCreatorChannel(i *dgo.InteractionCreate) error {
	data := dgo.GuildChannelCreateData{
		Name: "Creator Channel",
		Type: dgo.ChannelTypeGuildVoice,
	}

	channel, err := b.session.GuildChannelCreateComplex(i.GuildID, data)
	if err != nil {
		return fmt.Errorf("unable to create creator channel: %w", err)
	}

	if _, err := b.db.createCreatorChannel(context.Background(), CreatorChannel{ID: channel.ID}); err != nil {
		// TODO remove out of sync channel
		return fmt.Errorf("unable to create creator channel in database: %w", err)
	}

	b.session.InteractionRespond(i.Interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionResponseChannelMessageWithSource,
		Data: &dgo.InteractionResponseData{
			Content: "Created creator channel",
		},
	})

	return nil
}

func voiceStateUpdate(handleFunc func(e *dgo.VoiceStateUpdate) error) func(_ *dgo.Session, e *dgo.VoiceStateUpdate) {
	return func(_ *dgo.Session, e *dgo.VoiceStateUpdate) {
		if err := handleFunc(e); err != nil {
			slog.Error(err.Error())
		}
	}
}

func cmd(handleFunc func(i *dgo.InteractionCreate) error) func(_ *dgo.Session, i *dgo.InteractionCreate) {
	return func(_ *dgo.Session, i *dgo.InteractionCreate) {
		if err := handleFunc(i); err != nil {
			slog.Error(err.Error())
		}
	}
}

func channelHasUsers(guild *dgo.Guild, channelID string) bool {
	hasUsers := false
	for _, voiceState := range guild.VoiceStates {
		if voiceState.ChannelID == channelID {
			hasUsers = true
			break
		}
	}

	return hasUsers
}

func newIntOption(value int) *float64 {
	v := float64(value)
	return &v
}
