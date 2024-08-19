package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func One[T any](ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (T, error) {
	var defaultT T

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return defaultT, err
	}

	i, err := pgx.CollectOneRow[T](rows, pgx.RowToStructByNameLax)
	if err != nil {
		return defaultT, err
	}

	return i, nil
}

func Many[T any](ctx context.Context, pool *pgxpool.Pool, query string, args ...any) ([]T, error) {
	var defaultT []T

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return defaultT, err
	}
	defer rows.Close()

	is, err := pgx.CollectRows[T](rows, pgx.RowToStructByNameLax)
	if err != nil {
		return defaultT, err
	}

	if len(is) == 0 {
		return defaultT, pgx.ErrNoRows
	}

	return is, nil
}
