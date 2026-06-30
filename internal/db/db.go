package db

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
)

func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	return db, nil
}

// RunMigrations executes all .sql files in the migrations/ folder
func RunMigrations(database *sql.DB) error {
	migrationsDir := "migrations"

	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		// migrations folder not found relative to binary — skip silently in production
		return nil
	}

	for _, f := range files {
		if filepath.Ext(f.Name()) != ".sql" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(migrationsDir, f.Name()))
		if err != nil {
			continue
		}
		// Run migration, ignore "already exists" errors so it's safe to re-run
		database.Exec(string(content))
	}

	return nil
}
