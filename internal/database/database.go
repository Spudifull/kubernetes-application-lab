package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const OperationTimeout = 2 * time.Second

//go:embed migrations/*.sql
var migrations embed.FS

type Config struct {
	Host, Port, Name, User, Password string
}

func ConfigFromEnv() (Config, error) {
	c := Config{Host: os.Getenv("LAB_DB_HOST"), Port: os.Getenv("LAB_DB_PORT"), Name: os.Getenv("LAB_DB_NAME"), User: os.Getenv("LAB_DB_USER"), Password: os.Getenv("LAB_DB_PASSWORD")}
	if c.Host == "" || c.Port == "" || c.Name == "" || c.User == "" || c.Password == "" {
		return Config{}, errors.New("database profile requires LAB_DB_HOST, LAB_DB_PORT, LAB_DB_NAME, LAB_DB_USER and LAB_DB_PASSWORD")
	}
	if _, err := strconv.Atoi(c.Port); err != nil {
		return Config{}, errors.New("LAB_DB_PORT must be numeric")
	}
	return c, nil
}

func Open(c Config) (*sql.DB, error) {
	u := &url.URL{Scheme: "postgres", User: url.UserPassword(c.User, c.Password), Host: net.JoinHostPort(c.Host, c.Port), Path: c.Name}
	q := u.Query()
	q.Set("sslmode", "disable") // kind-only laboratory transport; never use for untrusted networks.
	q.Set("connect_timeout", "2")
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return nil, errors.New("open database driver failed")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func Ready(ctx context.Context, db *sql.DB) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var table sql.NullString
	err := db.QueryRowContext(ctx, "SELECT to_regclass('public.lab_records')::text").Scan(&table)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "28P01" {
			return false, "authentication_failed"
		}
		return false, "database_unavailable"
	}
	if !table.Valid {
		return false, "schema_missing"
	}
	return true, "ready"
}

func Put(ctx context.Context, db *sql.DB, key, value string) error {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	_, err := db.ExecContext(ctx, "INSERT INTO lab_records (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value", key, value)
	return err
}

func Get(ctx context.Context, db *sql.DB, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM lab_records WHERE key = $1", key).Scan(&value)
	return value, err
}

// Migrate serializes migration jobs with a PostgreSQL advisory lock. Each
// version is applied and recorded in the same transaction.
func Migrate(ctx context.Context, db *sql.DB, failLab bool) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.New("database connection unavailable")
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(641006)"); err != nil {
		return errors.New("migration lock unavailable")
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockContext, "SELECT pg_advisory_unlock(641006)")
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("migration transaction unavailable")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS lab_schema_migrations (version text PRIMARY KEY)"); err != nil {
		return errors.New("migration ledger unavailable")
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version := strings.TrimSuffix(entry.Name(), ".sql")
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM lab_schema_migrations WHERE version=$1)", version).Scan(&exists); err != nil {
			return errors.New("migration ledger read failed")
		}
		if exists {
			continue
		}
		sqlText, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlText)); err != nil {
			return fmt.Errorf("migration %s failed", version)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO lab_schema_migrations (version) VALUES ($1)", version); err != nil {
			return errors.New("migration ledger write failed")
		}
	}
	if failLab {
		if _, err := tx.ExecContext(ctx, "CREATE TABLE public.lab_migration_rollback_probe (id integer PRIMARY KEY)"); err != nil {
			return errors.New("migration rollback probe setup failed")
		}
		if _, err := tx.ExecContext(ctx, "SELECT * FROM lab_intentionally_missing_table"); err != nil {
			return errors.New("intentional migration failed after rollback probe creation")
		}
		return errors.New("intentional migration unexpectedly succeeded")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("migration commit failed")
	}
	return nil
}
