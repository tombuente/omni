package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/tombuente/omni/internal/apperrors"
)

type Bot struct {
	session *dgo.Session
	db      Database
}

var commands = []*dgo.ApplicationCommand{
	{
		Name:        "creator-channel",
		Description: "Creates a creactor channel",
	},
	{
		Name:        "channel-position",
		Description: "Update channel position",
		Options: []*dgo.ApplicationCommandOption{
			{
				Type:        dgo.ApplicationCommandOptionChannel,
				Name:        "channel",
				Description: "Channel",
				Required:    true,
			},
			{
				Type:        dgo.ApplicationCommandOptionInteger,
				Name:        "position",
				Description: "New Position",
				Required:    true,
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

func (b Bot) Run(ctx context.Context) error {
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

	b.session.AddHandler(wrapVoiceStateUpdate(b.voiceStateUpdateCreatorChannel))
	b.session.AddHandler(wrapVoiceStateUpdate(b.voiceStateUpdateTemporaryVoiceChannel))

	cmdHandlers := make(map[string]func(s *dgo.Session, i *dgo.InteractionCreate), len(commands))
	cmdHandlers["creator-channel"] = wrapCommand(b.commandCreatorChannel)
	cmdHandlers["channel-position"] = wrapCommand(b.cmdUpdateChannelPos)
	b.session.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if h, ok := cmdHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	<-ctx.Done()
	return nil
}

// voiceStateUpdateCreatorChannel handles VoiceStateUpdate in creator channels.
func (b Bot) voiceStateUpdateCreatorChannel(e *dgo.VoiceStateUpdate) error {
	if e.ChannelID == "" {
		// User left the voice channel
		return nil
	}

	if _, err := b.db.creatorChannel(context.Background(), e.ChannelID); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("unable to query creator channel (id=%v) from database: %w", e.ChannelID, err)
	}

	creatorCh, err := b.session.Channel(e.ChannelID)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)
	}

	voiceCh, err := b.createTemporaryVoiceChannel(
		creatorCh.GuildID,
		e.Member.User.Username,
		dgo.ChannelTypeGuildVoice,
		creatorCh.UserLimit,
		creatorCh.Position+1,
		creatorCh.ParentID)
	if err != nil {
		return fmt.Errorf("unable to create temporary voice channel: %w", err)
	}

	// Try to move the user into the newly created temporary voice channel.
	// If not possible, try deleting the now empty temporary voice channel.
	if err := b.session.GuildMemberMove(e.GuildID, e.UserID, &voiceCh.ID); err != nil {
		if _, err := b.session.ChannelDelete(voiceCh.ID); err != nil {
			slog.Warn("Unable to delete orphaned temporary voice channel")
		}

		return fmt.Errorf("unable to move user to temporary voice channel: %w", err)
	}

	return nil
}

// voiceStateUpdateTemporaryVoiceChannel handles VoiceStateUpates in temporary voice channels.
func (b Bot) voiceStateUpdateTemporaryVoiceChannel(e *dgo.VoiceStateUpdate) error {
	guild, err := b.session.State.Guild(e.GuildID)
	if err != nil {
		return fmt.Errorf("unable to get guild from state cache: %w", err)
	}

	if e.BeforeUpdate == nil {
		// User joined a channel without having been in a channel previously.
		return nil
	}

	chID := e.BeforeUpdate.ChannelID

	hasUsers := channelHasUsers(guild, chID)
	if hasUsers {
		// Must not delete a temporary voice channel if users are still in it.
		return nil
	}

	_, err = b.db.temporaryVoiceChannel(context.Background(), chID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("unable to query temporary voice channel (id=%v) from database: %w", chID, err)
	}

	if _, err := b.session.ChannelDelete(chID); err != nil {
		return fmt.Errorf("unable to delete voice channel: %w", err)
	}

	return nil
}

// commandCreatorChannel creates a new creator channel.
func (b Bot) commandCreatorChannel(i *dgo.InteractionCreate) error {
	data := dgo.GuildChannelCreateData{
		Name: "Creator Channel",
		Type: dgo.ChannelTypeGuildVoice,
	}

	ch, err := b.session.GuildChannelCreateComplex(i.GuildID, data)
	if err != nil {
		return fmt.Errorf("unable to create creator channel: %w", err)
	}

	if _, err := b.db.createCreatorChannel(context.Background(), CreatorChannel{ID: ch.ID}); err != nil {
		if _, err := b.session.ChannelDelete(ch.ID); err != nil {
			slog.Warn("Unable to delete creator channel that is not tracked in the database")
		}

		return fmt.Errorf("unable to create creator channel in database: %w", err)
	}

	return b.interactionRespond(i.Interaction, "Created creator channel")
}

func (b Bot) cmdUpdateChannelPos(i *dgo.InteractionCreate) error {
	options := interactionOptions(i)
	ch := options["channel"]   // required
	pos := options["position"] // required

	data := &dgo.ChannelEdit{
		Position: newInt(int(pos.IntValue())),
	}
	if _, err := b.session.ChannelEdit(ch.ChannelValue(nil).ID, data); err != nil {
		_ = b.interactionRespond(i.Interaction, "Unable to update channel")
		return fmt.Errorf("unable to update channel position: %w", err)
	}

	return b.interactionRespond(i.Interaction, "Updated channel")
}

// createTemporaryVoiceChannel handles the creation of a new voice channel in a specified
// Discord guild and stores its reference in the database. It ensures that
// the operations are kept in sync by attempting to delete both the channel
// and its database entry if either operation fails.
func (b Bot) createTemporaryVoiceChannel(guildID string, name string, channelType dgo.ChannelType, userLimit int, position int, parentID string) (*dgo.Channel, error) {
	data := dgo.GuildChannelCreateData{
		Name:      name,
		Type:      channelType,
		UserLimit: userLimit,
		Position:  position,
		ParentID:  parentID,
	}

	ch, err := b.session.GuildChannelCreateComplex(guildID, data)
	if err != nil {
		return &dgo.Channel{}, err
	}

	if _, err := b.db.createTemporaryVoiceChannel(context.Background(), TemporaryVoiceChannel{ID: ch.ID}); err != nil {
		if _, err := b.session.ChannelDelete(ch.ID); err != nil {
			slog.Warn("A temporary voice channel was created but is not tracked in the database")
		}

		return &dgo.Channel{}, err
	}

	return ch, nil
}

// interactionOptions returns a map where each key is an option name
// and each value is the corresponding value for that option.
func interactionOptions(i *dgo.InteractionCreate) map[string]*dgo.ApplicationCommandInteractionDataOption {
	options := make(map[string]*dgo.ApplicationCommandInteractionDataOption, len(i.ApplicationCommandData().Options))
	for _, option := range i.ApplicationCommandData().Options {
		options[option.Name] = option
	}

	return options
}

func (b Bot) interactionRespond(interaction *dgo.Interaction, msg string) error {
	return b.session.InteractionRespond(interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionResponseChannelMessageWithSource,
		Data: &dgo.InteractionResponseData{
			Content: msg,
		},
	})
}

func wrapVoiceStateUpdate(handleFunc func(e *dgo.VoiceStateUpdate) error) func(_ *dgo.Session, e *dgo.VoiceStateUpdate) {
	return func(_ *dgo.Session, e *dgo.VoiceStateUpdate) {
		if err := handleFunc(e); err != nil {
			slog.Error(err.Error())
		}
	}
}

func wrapCommand(handleFunc func(i *dgo.InteractionCreate) error) func(_ *dgo.Session, i *dgo.InteractionCreate) {
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

func newInt(value int) *int {
	return &value
}
