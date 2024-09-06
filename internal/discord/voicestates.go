package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/tombuente/omni/internal/apperrors"
)

// voiceStates adds VoiceStateUpdate event handlers to the session.
func (d Discord) voiceStates() {
	d.session.AddHandler(wrapVoiceStateUpdate(d.voiceStateUpdate))
}

func wrapVoiceStateUpdate(handleFunc func(s *dgo.Session, e *dgo.VoiceStateUpdate) error) func(s *dgo.Session, e *dgo.VoiceStateUpdate) {
	return func(s *dgo.Session, e *dgo.VoiceStateUpdate) {
		if err := handleFunc(s, e); err != nil {
			slog.Error(err.Error())
		}
	}
}

func (d Discord) voiceStateUpdate(s *dgo.Session, e *dgo.VoiceStateUpdate) error {
	if e.ChannelID != "" {
		if err := d.joinedChannel(s, e); err != nil {
			return err
		}
	}

	if e.BeforeUpdate != nil {
		return d.leftChannel(s, e.BeforeUpdate)
	}

	return nil
}

func (d Discord) joinedChannel(s *dgo.Session, e *dgo.VoiceStateUpdate) error {
	ok, err := d.isCreatorChannel(e.ChannelID)
	if err != nil {
		return err
	}
	if ok {
		d.joinedCreatorChannel(s, e)
	}

	return nil
}

func (d Discord) leftChannel(s *dgo.Session, state *dgo.VoiceState) error {
	ok, err := d.isTemporaryChannel(state.ChannelID)
	if err != nil {
		return err
	}
	if ok {
		d.leftTemporaryChannel(s, state)
	}

	return nil
}

func (d Discord) joinedCreatorChannel(s *dgo.Session, e *dgo.VoiceStateUpdate) error {
	channel, err := s.Channel(e.ChannelID)
	if err != nil {
		return fmt.Errorf("unable to get channel: %w", err)
	}

	data := dgo.GuildChannelCreateData{
		Name:      e.Member.User.Username,
		Type:      dgo.ChannelTypeGuildVoice,
		UserLimit: channel.UserLimit,
		Position:  channel.Position + 1,
		ParentID:  channel.ParentID,
	}
	tempChannel, err := s.GuildChannelCreateComplex(e.GuildID, data)
	if err != nil {
		return err
	}

	if _, err := d.db.createTemporaryChannel(context.Background(), TemporaryChannel{ID: tempChannel.ID, GuildID: tempChannel.GuildID}); err != nil {
		if _, err := s.ChannelDelete(channel.ID); err != nil {
			slog.Warn("A temporary channel was created but is not tracked in the database")
		}

		return err
	}

	// Try to move the user into the newly created temporary channel.
	// If not possible, try deleting the now empty temporary channel.
	if err := s.GuildMemberMove(e.GuildID, e.UserID, &tempChannel.ID); err != nil {
		if _, err := s.ChannelDelete(tempChannel.ID); err != nil {
			slog.Warn("Unable to delete orphaned temporary channel")
		}
		return fmt.Errorf("unable to move user to temporary channel: %w", err)
	}

	return nil
}

func (d Discord) leftTemporaryChannel(s *dgo.Session, state *dgo.VoiceState) error {
	guild, err := s.State.Guild(state.GuildID)
	if err != nil {
		return fmt.Errorf("unable to get guild from state cache: %w", err)
	}

	channelID := state.ChannelID
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

	if _, err := s.ChannelDelete(channelID); err != nil {
		return fmt.Errorf("unable to delete channel: %w", err)
	}

	return nil
}

func (d Discord) isCreatorChannel(id string) (bool, error) {
	_, err := d.db.creatorChannel(context.Background(), id)
	if err == nil {
		return true, nil
	} else if errors.Is(err, apperrors.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (d Discord) isTemporaryChannel(id string) (bool, error) {
	_, err := d.db.temporaryChannel(context.Background(), id)
	if err == nil {
		return true, nil
	} else if errors.Is(err, apperrors.ErrNotFound) {
		return false, err
	}
	return false, nil
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
