package bot

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
	const query = `
SELECT
	id::text
FROM
	bot.creator_channels
WHERE
	(id = $1::int8 OR $1::int8 IS NULL)
`
	return database.One[CreatorChannel](ctx, db.pool, query, id)
}

func (db Database) createTemporaryVoiceChannel(ctx context.Context, params TemporaryVoiceChannel) (TemporaryVoiceChannel, error) {
	const query = `
INSERT INTO 
	bot.temporary_voice_channels (id)
VALUES
	($1::int8)
RETURNING *
`
	return database.One[TemporaryVoiceChannel](ctx, db.pool, query, params.ID)
}

func (db Database) temporaryVoiceChannel(ctx context.Context, id string) (TemporaryVoiceChannel, error) {
	const query = `
SELECT
	id::text
FROM
	bot.temporary_voice_channels
WHERE
	(id = $1::int8 OR $1::int8 IS NULL)
`
	return database.One[TemporaryVoiceChannel](ctx, db.pool, query, id)
}
