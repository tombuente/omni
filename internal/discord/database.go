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
	const query = `
SELECT
	id::text
FROM
	bot.creator_channels
WHERE
	(id = $1::int8 OR $1 IS NULL)
`
	return database.One[CreatorChannel](ctx, db.pool, query, id)
}

func (db Database) createCreatorChannel(ctx context.Context, params CreatorChannel) (CreatorChannel, error) {
	const query = `
INSERT INTO 
	bot.creator_channels (id)
VALUES
	($1::int8)
RETURNING *
`
	return database.One[CreatorChannel](ctx, db.pool, query, params.ID)
}

func (db Database) temporaryChannel(ctx context.Context, id string) (TemporaryChannel, error) {
	const query = `
SELECT
	id::text
FROM
	bot.temporary_channels
WHERE
	(id = $1::int8 OR $1 IS NULL)
`
	return database.One[TemporaryChannel](ctx, db.pool, query, id)
}

func (db Database) createTemporaryChannel(ctx context.Context, params TemporaryChannel) (TemporaryChannel, error) {
	const query = `
INSERT INTO 
	bot.temporary_channels (id)
VALUES
	($1::int8)
RETURNING *
`
	return database.One[TemporaryChannel](ctx, db.pool, query, params.ID)
}
