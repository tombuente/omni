package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/tombuente/omni/internal/apperrors"
)

type Discord struct {
	session *dgo.Session
	db      Database
}

type CreatorChannel struct {
	ID string `db:"id"`
}

type TemporaryChannel struct {
	ID string `db:"id"`
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

func Make(token string, db Database) (Discord, error) {
	session, err := dgo.New(fmt.Sprintf("Bot %v", token))
	if err != nil {
		return Discord{}, fmt.Errorf("unable to create session: %w", err)
	}

	session.Identify.Intents = dgo.IntentGuilds | dgo.IntentGuildVoiceStates

	return Discord{
		session: session,
		db:      db,
	}, nil
}

func (d Discord) Run(ctx context.Context) error {
	if err := d.session.Open(); err != nil {
		return fmt.Errorf("unable to open session: %w", err)
	}
	defer d.session.Close()

	appID := d.session.State.User.ID
	for _, cmd := range commands {
		_, err := d.session.ApplicationCommandCreate(appID, "1242245611298885683", cmd)
		if err != nil {
			slog.Error("Unable to create application command", "command", cmd.Name, "error", err)
		}
	}

	d.session.AddHandler(wrapVoiceStateUpdate(d.voiceStateUpdate))

	cmdHandlers := make(map[string]func(s *dgo.Session, i *dgo.InteractionCreate), len(commands))
	cmdHandlers["creator-channel"] = wrapCommand(d.commandCreatorChannel)
	cmdHandlers["channel-position"] = wrapCommand(d.cmdUpdateChannelPos)
	d.session.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if h, ok := cmdHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	<-ctx.Done()
	return nil
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

func (d Discord) voiceStateUpdate(e *dgo.VoiceStateUpdate) error {
	if e.ChannelID != "" {
		if err := d.joinedChannel(e); err != nil {
			return err
		}
	}

	if e.BeforeUpdate != nil {
		return d.leftChannel(e)
	}

	return nil
}

func (d Discord) joinedChannel(e *dgo.VoiceStateUpdate) error {
	ok, err := d.isCreatorChannel(e.ChannelID)
	if err != nil {
		return err
	}
	if ok {
		d.joinedCreatorChannel(e)
	}

	return nil
}

func (d Discord) leftChannel(e *dgo.VoiceStateUpdate) error {
	ok, err := d.isTemporaryChannel(e.BeforeUpdate.ChannelID)
	if err != nil {
		return err
	}
	if ok {
		d.leftTemporaryChannel(e)
	}

	return nil
}

func (d Discord) isCreatorChannel(id string) (bool, error) {
	_, err := d.db.creatorChannel(context.Background(), id)
	if err == nil {
		return true, nil
	} else if !errors.Is(err, apperrors.ErrNotFound) {
		return false, err
	}

	return false, nil
}

func (d Discord) isTemporaryChannel(id string) (bool, error) {
	_, err := d.db.temporaryChannel(context.Background(), id)
	if err == nil {
		return true, nil
	} else if !errors.Is(err, apperrors.ErrNotFound) {
		return false, err
	}

	return false, nil
}

func (d Discord) joinedCreatorChannel(e *dgo.VoiceStateUpdate) error {
	channel, err := d.session.Channel(e.ChannelID)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)
	}

	voiceCh, err := d.createTemporaryChannel(
		channel.GuildID,
		e.Member.User.Username,
		dgo.ChannelTypeGuildVoice,
		channel.UserLimit,
		channel.Position+1,
		channel.ParentID)
	if err != nil {
		return fmt.Errorf("unable to create temporary channel: %w", err)
	}

	// Try to move the user into the newly created temporary channel.
	// If not possible, try deleting the now empty temporary channel.
	if err := d.session.GuildMemberMove(e.GuildID, e.UserID, &voiceCh.ID); err != nil {
		if _, err := d.session.ChannelDelete(voiceCh.ID); err != nil {
			slog.Warn("Unable to delete orphaned temporary channel")
		}
		return fmt.Errorf("unable to move user to temporary channel: %w", err)
	}

	return nil
}

func (d Discord) leftTemporaryChannel(e *dgo.VoiceStateUpdate) error {
	guild, err := d.session.State.Guild(e.GuildID)
	if err != nil {
		return fmt.Errorf("unable to get guild from state cache: %w", err)
	}

	channelID := e.BeforeUpdate.ChannelID
	if channelHasUsers(guild, channelID) {
		// Must not delete a temporary channel if users are still in it.
		return nil
	}

	_, err = d.db.temporaryChannel(context.Background(), channelID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("unable to query temporary channel (id=%v) from database: %w", channelID, err)
	}

	if _, err := d.session.ChannelDelete(channelID); err != nil {
		return fmt.Errorf("unable to delete channel: %w", err)
	}

	return nil
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

func (d Discord) commandCreatorChannel(i *dgo.InteractionCreate) error {
	data := dgo.GuildChannelCreateData{
		Name: "Creator Channel",
		Type: dgo.ChannelTypeGuildVoice,
	}

	ch, err := d.session.GuildChannelCreateComplex(i.GuildID, data)
	if err != nil {
		return fmt.Errorf("unable to create creator channel: %w", err)
	}

	if _, err := d.db.createCreatorChannel(context.Background(), CreatorChannel{ID: ch.ID}); err != nil {
		if _, err := d.session.ChannelDelete(ch.ID); err != nil {
			slog.Warn("Unable to delete creator channel that is not tracked in the database")
		}

		return fmt.Errorf("unable to create creator channel in database: %w", err)
	}

	return d.interactionRespond(i.Interaction, "Created creator channel")
}

func (d Discord) cmdUpdateChannelPos(i *dgo.InteractionCreate) error {
	options := interactionOptions(i)
	ch := options["channel"]   // required
	pos := options["position"] // required

	data := &dgo.ChannelEdit{
		Position: newInt(int(pos.IntValue())),
	}
	if _, err := d.session.ChannelEdit(ch.ChannelValue(nil).ID, data); err != nil {
		_ = d.interactionRespond(i.Interaction, "Unable to update channel")
		return fmt.Errorf("unable to update channel position: %w", err)
	}

	return d.interactionRespond(i.Interaction, "Updated channel")
}

// createTemporaryChannel handles the creation of a new channel.
// It tries to keep Discord and the database in sync by attempting to delete
// both the channel and its database entry if either operation fails.
func (d Discord) createTemporaryChannel(guildID string, name string, channelType dgo.ChannelType, userLimit int, position int, parentID string) (*dgo.Channel, error) {
	data := dgo.GuildChannelCreateData{
		Name:      name,
		Type:      channelType,
		UserLimit: userLimit,
		Position:  position,
		ParentID:  parentID,
	}

	channel, err := d.session.GuildChannelCreateComplex(guildID, data)
	if err != nil {
		return &dgo.Channel{}, err
	}

	if _, err := d.db.createTemporaryChannel(context.Background(), TemporaryChannel{ID: channel.ID}); err != nil {
		if _, err := d.session.ChannelDelete(channel.ID); err != nil {
			slog.Warn("A temporary channel was created but is not tracked in the database")
		}

		return &dgo.Channel{}, err
	}

	return channel, nil
}

// interactionOptions returns a map with option name and value pairs.
func interactionOptions(i *dgo.InteractionCreate) map[string]*dgo.ApplicationCommandInteractionDataOption {
	options := make(map[string]*dgo.ApplicationCommandInteractionDataOption, len(i.ApplicationCommandData().Options))
	for _, option := range i.ApplicationCommandData().Options {
		options[option.Name] = option
	}

	return options
}

func (d Discord) interactionRespond(interaction *dgo.Interaction, msg string) error {
	return d.session.InteractionRespond(interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionResponseChannelMessageWithSource,
		Data: &dgo.InteractionResponseData{
			Content: msg,
		},
	})
}

func newInt(value int) *int {
	return &value
}
