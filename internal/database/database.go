package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tombuente/omni/internal/apperrors"
)

func One[T any](ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (T, error) {
	var defaultT T

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return defaultT, translateError(err)
	}

	i, err := pgx.CollectOneRow[T](rows, pgx.RowToStructByNameLax)
	if err != nil {
		return defaultT, translateError(err)
	}

	return i, nil
}

func Many[T any](ctx context.Context, pool *pgxpool.Pool, query string, args ...any) ([]T, error) {
	var defaultT []T

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return defaultT, translateError(err)
	}
	defer rows.Close()

	is, err := pgx.CollectRows[T](rows, pgx.RowToStructByNameLax)
	if err != nil {
		return defaultT, translateError(err)
	}

	if len(is) == 0 {
		return defaultT, translateError(pgx.ErrNoRows)
	}

	return is, nil
}

// translateError translates a Postgres error into an app error.
// If err is nil, nil is returned and no translation takes place.
func translateError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", apperrors.ErrNotFound, err)
	}

	return err
}
