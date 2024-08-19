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

	b.addHandlers()

	<-stop
	return nil
}

func (b Bot) addHandlers() {
	b.session.AddHandler(b.voiceStateUpdateCreatorChannel)
	b.session.AddHandler(b.voiceStateUpdateTemporaryVoiceChannel)
}

func (b Bot) voiceStateUpdateCreatorChannel(s *dgo.Session, event *dgo.VoiceStateUpdate) {
	channelID := event.ChannelID
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
			Name:      event.Member.User.Username,
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

	if err := s.GuildMemberMove(event.GuildID, event.UserID, &tempVoiceChannel.ID); err != nil {
		slog.Error("Unable to move user to temporary voice channel", "error", err)

		// Remove orphan
		if _, err := s.ChannelDelete(tempVoiceChannel.ID); err != nil {
			slog.Error("Unable to delete temporary voice channel, it's now an orphan")
		}

		return
	}
}

func (b Bot) voiceStateUpdateTemporaryVoiceChannel(s *dgo.Session, event *dgo.VoiceStateUpdate) {
	guild, err := b.session.State.Guild(event.GuildID)
	if err != nil {
		slog.Error("Unable to get guild from state cache", "error", err)
		return
	}

	if event.BeforeUpdate == nil {
		// User joined a channel without having been in a channel previously
		return
	}

	chID := event.BeforeUpdate.ChannelID
	chHasUsers := voiceChannelHasUsers(guild, chID)
	if chHasUsers {
		// Must not delete a temporary voice channel if users are still in it
		return
	}

	_, err = b.db.temporaryVoiceChannel(context.Background(), chID)
	if err != nil {
		slog.Error("Unable to query temporary voice channel from database", "id", chID, "error", err)
		return
	}

	if _, err := s.ChannelDelete(chID); err != nil {
		slog.Error("Unable to delete voice channel", "error", err)
		return
	}
}

func voiceChannelHasUsers(guild *dgo.Guild, channelID string) bool {
	hasUsers := false
	for _, voiceState := range guild.VoiceStates {
		if voiceState.ChannelID == channelID {
			hasUsers = true
			break
		}
	}

	return hasUsers
}
