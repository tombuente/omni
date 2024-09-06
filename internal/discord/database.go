package discord

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tombuente/omni/internal/database"
)

type Database struct {
	pool *pgxpool.Pool
}

func MakeDatabase(pool *pgxpool.Pool) Database {
	return Database{
		pool: pool,
	}
}

func (db Database) creatorChannel(ctx context.Context, id string) (CreatorChannel, error) {
	const sql = `
	SELECT
		id::text
	FROM
		discord.creator_channels
	WHERE
		(id = $1::int8 OR $1 IS NULL)
	`
	return database.One[CreatorChannel](ctx, db.pool, sql, id)
}

func (db Database) creatorChannels(ctx context.Context, filter creatorChannelFilter) ([]CreatorChannel, error) {
	const sql = `
	SELECT
		id, guild_id::text
	FROM
		discord.creator_channels
	WHERE
		(guild_id = $1::int8 OR $1 IS NULL)
	`
	return database.Many[CreatorChannel](ctx, db.pool, sql, filter.guildID)
}

func (db Database) createCreatorChannel(ctx context.Context, params CreatorChannel) (CreatorChannel, error) {
	const sql = `
	INSERT INTO 
		discord.creator_channels (id, guild_id)
	VALUES
		($1::int8, $2::int8)
	RETURNING *
	`
	return database.One[CreatorChannel](ctx, db.pool, sql, params.ID, params.GuildID)
}

func (db Database) temporaryChannel(ctx context.Context, id string) (TemporaryChannel, error) {
	const sql = `
	SELECT
		id::text
	FROM
		discord.temporary_channels
	WHERE
		(id = $1::int8 OR $1 IS NULL)
	`
	return database.One[TemporaryChannel](ctx, db.pool, sql, id)
}

func (db Database) createTemporaryChannel(ctx context.Context, params TemporaryChannel) (TemporaryChannel, error) {
	const sql = `
	INSERT INTO 
		discord.temporary_channels (id, guild_id)
	VALUES
		($1::int8, $2::int8)
	RETURNING *
	`
	return database.One[TemporaryChannel](ctx, db.pool, sql, params.ID, params.GuildID)
}

func (db Database) group(ctx context.Context, id int64) (Group, error) {
	const sql = `
	SELECT
		*
	FROM
		discord.groups
	WHERE
		(id = $1 OR $1 IS NULL)
	`
	return database.One[Group](ctx, db.pool, sql, id)
}

func (db Database) groups(ctx context.Context, filter GroupFilter) ([]Group, error) {
	const sql = `
	SELECT
		id, name, guild_id::text
	FROM
		discord.groups
	WHERE
		(guild_id = $1::int8 OR $1 IS NULL)
	`
	return database.Many[Group](ctx, db.pool, sql, filter.guildID)
}

func (db Database) createGroup(ctx context.Context, params GroupParams) (Group, error) {
	const sql = `
	INSERT INTO 
		discord.groups (name, guild_id)
	VALUES
		($1, $2)
	RETURNING *
	`
	return database.One[Group](ctx, db.pool, sql, params.name, params.guildID)
}
