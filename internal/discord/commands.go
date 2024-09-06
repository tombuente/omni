package discord

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/tombuente/omni/internal/apperrors"
)

var (
	errNoHandler = errors.New("no handler")

	commands = []*dgo.ApplicationCommand{
		{
			Name:        "mod",
			Description: "-",
			Options: []*dgo.ApplicationCommandOption{
				{
					Name:        "group",
					Description: "-",
					Options: []*dgo.ApplicationCommandOption{
						{
							Name:        "create",
							Description: "Create a new group.",
							Options: []*dgo.ApplicationCommandOption{
								{
									Name:        "name",
									Description: "Name",
									Required:    true,
									Type:        dgo.ApplicationCommandOptionString,
								},
							},
							Type: dgo.ApplicationCommandOptionSubCommand,
						},
						{
							Name:        "list",
							Description: "List all groups this server is a member of.",
							Type:        dgo.ApplicationCommandOptionSubCommand,
						},
					},
					Type: dgo.ApplicationCommandOptionSubCommandGroup,
				},
			},
		},
		{
			Name:        "tempvoice",
			Description: "-",
			Options: []*dgo.ApplicationCommandOption{
				{
					Name:         "creator",
					Description:  "-",
					Type:         dgo.ApplicationCommandOptionSubCommandGroup,
					Autocomplete: true,
					Options: []*dgo.ApplicationCommandOption{
						{
							Name:        "create",
							Description: "Create a creator channel.",
							Type:        dgo.ApplicationCommandOptionSubCommand,
						},
						{
							Name:        "position",
							Description: "Change the position of the creator channel, helps if temporary channels appear in the wrong place.",
							Type:        dgo.ApplicationCommandOptionSubCommand,
							Options: []*dgo.ApplicationCommandOption{
								{
									Name:         "channel",
									Description:  "The creator channel.",
									Type:         dgo.ApplicationCommandOptionString,
									Required:     true,
									Autocomplete: true,
								},
								{
									Name:        "position",
									Description: "New position of the creator channel",
									Type:        dgo.ApplicationCommandOptionInteger,
									MinValue:    newIntOption(0),
									Required:    true,
								},
							},
						},
					},
				},
			},
		},
	}
)

type commandError struct {
	err           error
	publicMessage string
}

type commandHandleFunc func(c *interactionContext) error

type autocompleteHandleFunc func(c *interactionContext) error

type interactionContext struct {
	s *dgo.Session
	i *dgo.InteractionCreate

	// You can only respond to an interaction once, after which the response must be edited.
	deferred bool

	// If commands are nested, set to options of sub command before calling the handle func.
	options []*dgo.ApplicationCommandInteractionDataOption
}

// commands creates slash commands and registers handlers for them.
func (d Discord) commands() {
	slog.Info("Creating slash commands...")
	appID := d.session.State.User.ID
	for _, cmd := range commands {
		_, err := d.session.ApplicationCommandCreate(appID, d.config.guild, cmd)
		if err != nil {
			slog.Error("Unable to create slash command", "command", cmd.Name, "error", err)
		}
	}
	if d.config.guild == "" {
		slog.Info("Created slash commands globally.")
	} else {
		slog.Info("Created slash commands to guild", "guild_id", d.config.guild)
	}

	c := make(map[string]func(s *dgo.Session, i *dgo.InteractionCreate))
	c["mod"] = wrapInteraction(d.handleMod)
	c["tempvoice"] = wrapInteraction(d.handleTempVoice)

	d.session.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if handle, ok := c[i.ApplicationCommandData().Name]; ok {
			handle(s, i)
		}
	})
}

func (d Discord) deleteCommands() error {
	slog.Info("Deleting commands...")
	appID := d.session.State.User.ID
	commands, err := d.session.ApplicationCommands(appID, d.config.guild)
	if err != nil {
		return fmt.Errorf("unable to fetch slash commands: %w", err)
	}

	for _, command := range commands {
		if err := d.session.ApplicationCommandDelete(appID, d.config.guild, command.ID); err != nil {
			return fmt.Errorf("unable to delete slash command: %w", err)
		}
	}
	if d.config.guild == "" {
		slog.Info("Deleted slash commands globally.")
	} else {
		slog.Info("Deleted slash commands from guild", "guild_id", d.config.guild)
	}

	return nil
}

func wrapInteraction(handle commandHandleFunc) func(s *dgo.Session, i *dgo.InteractionCreate) {
	return func(s *dgo.Session, i *dgo.InteractionCreate) {
		c := newCommandContext(s, i)

		if err := handle(c); err != nil {
			switch i.Type {
			case dgo.InteractionApplicationCommand:
				slog.Error("command handler failed", "error", err)

				publicMessage := "Internal error"
				var cmdErr *commandError
				if errors.As(err, &cmdErr) {
					publicMessage = cmdErr.publicMessage
				}

				if err := c.text(publicMessage); err != nil {
					slog.Warn("Unable to respond to command", "error", err)
				}
			case dgo.InteractionApplicationCommandAutocomplete:
				slog.Error("autocomplete handler failed", "error", err)
			}
		}
	}
}

func withAutocomplete(c *interactionContext, handleCommand commandHandleFunc, handleAutocomplete autocompleteHandleFunc) error {
	switch c.i.Type {
	case dgo.InteractionApplicationCommand:
		return handleCommand(c)
	case dgo.InteractionApplicationCommandAutocomplete:
		return handleAutocomplete(c)
	}
	return errNoHandler
}

func (d Discord) handleMod(c *interactionContext) error {
	name := c.options[0].Name
	c.options = c.options[0].Options
	switch name {
	case "group":
		return d.handleModGroup(c)
	}
	return errNoHandler
}

func (d Discord) handleModGroup(c *interactionContext) error {
	name := c.options[0].Name
	c.options = c.options[0].Options
	switch name {
	case "create":
		return d.handleModGroupCreate(c)
	case "list":
		return d.handleModGroupList(c)
	}
	return errNoHandler
}

func (d Discord) handleTempVoice(c *interactionContext) error {
	name := c.options[0].Name
	c.options = c.options[0].Options
	switch name {
	case "creator":
		return d.handleTempVoiceCreator(c)
	}
	return errNoHandler
}

func (d Discord) handleTempVoiceCreator(c *interactionContext) error {
	name := c.options[0].Name
	c.options = c.options[0].Options
	switch name {
	case "create":
		return d.handleTempVoiceCreatorCreate(c)
	case "position":
		return withAutocomplete(c, d.handleTempVoiceCreatorPosition, d.handleTempVoiceCreatorPositionAutocomplete)
	}
	return errNoHandler
}

func (d Discord) handleModGroupList(c *interactionContext) error {
	if err := c.deferCmd(); err != nil {
		return err
	}

	filter := GroupFilter{
		guildID: sql.NullString{String: c.i.GuildID, Valid: true},
	}
	groups, err := d.db.groups(context.Background(), filter)
	if errors.Is(err, apperrors.ErrNotFound) {
		return c.text("No groups found.")
	} else if err != nil {
		return fmt.Errorf("unable to get groups: %w", err)
	}

	var message string
	nameCache := make(map[string]string)
	for i, g := range groups {
		name, ok := nameCache[g.GuildID]
		if !ok {
			group, err := d.session.Guild(g.GuildID)
			if err != nil {
				return err
			}
			nameCache[group.ID] = group.Name
			name = group.Name
		}

		message += fmt.Sprintf("%v. `%v` (%v)\n", i, g.Name, name)
	}

	return c.text(message)
}

func (d Discord) handleModGroupCreate(c *interactionContext) error {
	name := c.optionMap()["name"].StringValue() // required

	if ok, _ := regexp.MatchString("^[A-Za-z0-9 _-]+$", name); !ok {
		return newCommandError("Malformatted group name, only A-Z, a-z, 0-9, space, dash, and underscore are allowed.")
	}
	name = strings.TrimSpace(name)

	params := GroupParams{
		name:    name,
		guildID: c.i.GuildID,
	}
	_, err := d.db.createGroup(context.Background(), params)
	if err != nil {
		return err
	}

	return c.text(fmt.Sprintf("Created group `%v`", name))
}

func (d Discord) handleTempVoiceCreatorCreate(c *interactionContext) error {
	data := dgo.GuildChannelCreateData{
		Name: "Creator",
		Type: dgo.ChannelTypeGuildVoice,
	}
	channel, err := c.s.GuildChannelCreateComplex(c.i.GuildID, data)
	if err != nil {
		return newCommandError("Unable to create guild channel").WithErr(err)
	}

	if _, err := d.db.createCreatorChannel(context.Background(), CreatorChannel{ID: channel.ID, GuildID: channel.GuildID}); err != nil {
		if _, delErr := c.s.ChannelDelete(channel.ID); delErr != nil {
			slog.Warn("Unable to delete creator channel that is not tracked in the database", slog.Group("error", err.Error(), delErr))
		}
		return err
	}

	return c.text(fmt.Sprintf("Created creator channel `%v`, feel free to move it!", channel.ID))
}

func (d Discord) handleTempVoiceCreatorPosition(c *interactionContext) error {
	channelID := c.optionMap()["channel"].StringValue()   // required
	position := int(c.optionMap()["position"].IntValue()) // required

	data := &dgo.ChannelEdit{
		Position: newInt(position),
	}
	if _, err := d.session.ChannelEdit(channelID, data); err != nil {
		return newCommandError("Unable to edit channel").WithErr(err)
	}

	return c.text("Updated channel")
}

func (d Discord) handleTempVoiceCreatorPositionAutocomplete(c *interactionContext) error {
	channels, err := d.db.creatorChannels(context.Background(), creatorChannelFilter{guildID: c.i.GuildID})
	if errors.Is(err, apperrors.ErrNotFound) {
		return c.choices([]*dgo.ApplicationCommandOptionChoice{})
	}
	if err != nil {
		return fmt.Errorf("unable to query creator channels from database: %w", err)
	}

	guildChannels, err := d.session.GuildChannels(c.i.GuildID)
	if err != nil {
		return fmt.Errorf("unable to obtain guild channel data from Discord: %w", err)
	}
	names := make(map[string]string)
	for _, channel := range guildChannels {
		names[channel.ID] = channel.Name
	}

	var choices []*dgo.ApplicationCommandOptionChoice
	for _, channel := range channels {
		name, ok := names[channel.ID]
		if !ok {
			slog.Warn("Channel does not exist anymore", "channel", channel.ID)
			continue
		}
		choices = append(choices, &dgo.ApplicationCommandOptionChoice{
			Name:  name,
			Value: channel.ID,
		})
	}

	return c.choices(choices)
}

func newCommandError(publicMessage string) *commandError {
	return &commandError{
		publicMessage: publicMessage,
	}
}

func (e *commandError) WithErr(err error) *commandError {
	return &commandError{
		err:           err,
		publicMessage: e.publicMessage,
	}
}

func (e *commandError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%v: %v", e.publicMessage, e.err)
	}
	return e.publicMessage
}

func (e *commandError) Unwrap() error {
	return e.err
}

func newCommandContext(s *dgo.Session, i *dgo.InteractionCreate) *interactionContext {
	return &interactionContext{
		s:       s,
		i:       i,
		options: i.ApplicationCommandData().Options,
	}
}

func (c *interactionContext) optionMap() map[string]*dgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*dgo.ApplicationCommandInteractionDataOption, len(c.options))
	for _, option := range c.options {
		m[option.Name] = option
	}
	return m
}

func (c *interactionContext) text(msg string) error {
	if !c.deferred {
		if err := c.s.InteractionRespond(c.i.Interaction, &dgo.InteractionResponse{
			Type: dgo.InteractionResponseChannelMessageWithSource,
			Data: &dgo.InteractionResponseData{
				Content: msg,
			},
		}); err != nil {
			return err
		}
		c.deferred = true
		return nil
	}

	_, err := c.s.InteractionResponseEdit(c.i.Interaction, &dgo.WebhookEdit{Content: &msg})
	return err
}

func (c *interactionContext) choices(choices []*dgo.ApplicationCommandOptionChoice) error {
	return c.s.InteractionRespond(c.i.Interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionApplicationCommandAutocompleteResult,
		Data: &dgo.InteractionResponseData{
			Choices: choices,
		},
	})
}

func (c *interactionContext) deferCmd() error {
	if err := c.s.InteractionRespond(c.i.Interaction, &dgo.InteractionResponse{
		Type: dgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		return fmt.Errorf("unable to defer interaction: %w", err)
	}
	c.deferred = true
	return nil
}

func newIntOption(value int) *float64 {
	i := float64(value)
	return &i
}

func newInt(value int) *int {
	return &value
}
