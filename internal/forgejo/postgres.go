// forgejo/postgres.go
package forgejo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct {
	pool *pgxpool.Pool
}

var _ Store = (*Postgres)(nil)

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) ResolveRepo(ctx context.Context, user, name string) (*Repo, error) {
	var repo Repo
	var created int64
	err := p.pool.QueryRow(ctx, `
		SELECT r.id, r.lower_name, lower(r.owner_name), coalesce(r.description, ''),
		       r.default_branch, r.created_unix
		FROM repository r
		JOIN "user" u ON u.id = r.owner_id
		WHERE u.lower_name = $1 AND r.lower_name = $2 AND NOT r.is_private`,
		strings.ToLower(user), strings.ToLower(name),
	).Scan(&repo.ID, &repo.Name, &repo.OwnerName, &repo.Description, &repo.DefaultBranch, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	repo.CreatedAt = time.Unix(created, 0)
	return &repo, nil
}

func (p *Postgres) Languages(ctx context.Context, repoID int64) (map[string]int64, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT language, size FROM language_stat WHERE repo_id = $1`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sizes := make(map[string]int64)
	for rows.Next() {
		var lang string
		var size int64
		if err := rows.Scan(&lang, &size); err != nil {
			return nil, err
		}
		sizes[lang] = size
	}
	return sizes, rows.Err()
}

func (p *Postgres) PublicKeys(ctx context.Context, user string) ([]PublicKey, error) {
	// type = 1 is KeyTypeUser (2 = deploy key, 3 = principal).
	rows, err := p.pool.Query(ctx, `
		SELECT k.content, k.created_unix
		FROM public_key k
		JOIN "user" u ON u.id = k.owner_id
		WHERE u.lower_name = $1 AND k.type = 1`,
		strings.ToLower(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []PublicKey
	for rows.Next() {
		var k PublicKey
		var created int64
		if err := rows.Scan(&k.Key, &created); err != nil {
			return nil, err
		}
		k.CreatedAt = time.Unix(created, 0)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (p *Postgres) ListRepos(ctx context.Context, user string) ([]Repo, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT r.id, r.lower_name, lower(r.owner_name), coalesce(r.description, ''),
		       r.default_branch, r.created_unix
		FROM repository r
		JOIN "user" u ON u.id = r.owner_id
		WHERE u.lower_name = $1 AND NOT r.is_private
		ORDER BY r.lower_name`,
		strings.ToLower(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []Repo
	for rows.Next() {
		var repo Repo
		var created int64
		if err := rows.Scan(&repo.ID, &repo.Name, &repo.OwnerName, &repo.Description, &repo.DefaultBranch, &created); err != nil {
			return nil, err
		}
		repo.CreatedAt = time.Unix(created, 0)
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}
