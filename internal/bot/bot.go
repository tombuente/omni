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

var (
	commands = []*dgo.ApplicationCommand{
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
)

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

	b.session.AddHandler(b.voiceStateUpdateCreatorChannel)
	b.session.AddHandler(b.voiceStateUpdateTemporaryVoiceChannel)

	cmdHandlers := make(map[string]func(s *dgo.Session, i *dgo.InteractionCreate), len(commands))
	cmdHandlers["creator-channel"] = b.cmdCreatorChannel
	b.session.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if h, ok := cmdHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	<-stop
	return nil
}

func (b Bot) voiceStateUpdateCreatorChannel(s *dgo.Session, e *dgo.VoiceStateUpdate) {
	channelID := e.ChannelID
	if channelID == "" {
		// User left the voice channel
		return
	}

	if _, err := b.db.creatorChannel(context.Background(), channelID); err != nil {
		slog.Error("Unable to query creator channel from database", "id", channelID, "error", err)
		return
	}

	creatorChannel, err := s.Channel(channelID)
	if err != nil {
		slog.Error("Unable to get channel", "error", err)
		return
	}

	tempVoiceChannel, err := s.GuildChannelCreateComplex(
		creatorChannel.GuildID,
		dgo.GuildChannelCreateData{
			Name:      e.Member.User.Username,
			Type:      dgo.ChannelTypeGuildVoice,
			UserLimit: creatorChannel.UserLimit,
			Position:  creatorChannel.Position + 1,
			ParentID:  creatorChannel.ParentID,
		})
	if err != nil {
		slog.Error("Unable to create temporary voice channel", "error", err)
		return
	}

	if _, err := b.db.createTemporaryVoiceChannel(context.Background(), TemporaryVoiceChannel{ID: tempVoiceChannel.ID}); err != nil {
		slog.Error("Unable to create temporary voice channel in database", "error", err)

		// In an effort to keep Discord and the database in sync, try to delete the temporary voice channel.
		if _, err := s.ChannelDelete(tempVoiceChannel.ID); err != nil {
			slog.Error("Unable to delete temporary voice channel, it is also not tracked in the database. The bot will now lose track of the channel")
		}

		return
	}

	if err := s.GuildMemberMove(e.GuildID, e.UserID, &tempVoiceChannel.ID); err != nil {
		slog.Error("Unable to move user to temporary voice channel", "error", err)

		// Remove orphan
		if _, err := s.ChannelDelete(tempVoiceChannel.ID); err != nil {
			slog.Error("Unable to delete temporary voice channel, it's now an orphan")
		}

		return
	}
}

func (b Bot) voiceStateUpdateTemporaryVoiceChannel(s *dgo.Session, e *dgo.VoiceStateUpdate) {
	guild, err := b.session.State.Guild(e.GuildID)
	if err != nil {
		slog.Error("Unable to get guild from state cache", "error", err)
		return
	}

	if e.BeforeUpdate == nil {
		// User joined a channel without having been in a channel previously
		return
	}

	channelID := e.BeforeUpdate.ChannelID
	channelHasUsers := channelHasUsers(guild, channelID)
	if channelHasUsers {
		// Must not delete a temporary voice channel if users are still in it
		return
	}

	_, err = b.db.temporaryVoiceChannel(context.Background(), channelID)
	if err != nil {
		slog.Error("Unable to query temporary voice channel from database", "id", channelID, "error", err)
		return
	}

	if _, err := s.ChannelDelete(channelID); err != nil {
		slog.Error("Unable to delete voice channel", "error", err)
		return
	}
}

func (b Bot) cmdCreatorChannel(s *dgo.Session, i *dgo.InteractionCreate) {
	data := dgo.GuildChannelCreateData{
		Name: "Creator Channel",
		Type: dgo.ChannelTypeGuildVoice,
	}

	channel, err := s.GuildChannelCreateComplex(i.GuildID, data)
	if err != nil {
		slog.Error("Unable to create creator channel", "error", err)
		return
	}

	if _, err := b.db.createCreatorChannel(context.Background(), CreatorChannel{ID: channel.ID}); err != nil {
		slog.Error("Unable to create creator channel in database", "error", err)
		// TODO remove out of sync channel
		return
	}

	s.InteractionRespond(i.Interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionResponseChannelMessageWithSource,
		Data: &dgo.InteractionResponseData{
			Content: "Created creator channel",
		},
	})
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
