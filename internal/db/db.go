// Package db opens the configured database via GORM, which handles dialect
// differences (placeholder style, generated-ID retrieval, identifier quoting)
// for sqlite/mysql/postgres uniformly so the repo layer stays portable.
package db

import (
	"fmt"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"servicedesk/internal/models"
)

// Open connects, auto-migrates the schema from the models package, and
// applies the one dialect-specific extra AutoMigrate can't express: sqlite's
// FTS5 virtual tables for full-text search. MySQL gets a FULLTEXT index via
// a gorm tag on Ticket/Note; Postgres computes to_tsvector() at query time.
func Open(driver, dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "mysql":
		dialector = mysql.Open(dsn)
	case "postgres", "postgresql":
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported db driver %q (want sqlite, mysql, or postgres)", driver)
	}

	gdb, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	if driver == "sqlite" {
		sqlDB, err := gdb.DB()
		if err != nil {
			return nil, err
		}
		sqlDB.SetMaxOpenConns(1) // sqlite: single writer, avoids SQLITE_BUSY under WAL
	}

	if err := gdb.AutoMigrate(
		&models.User{}, &models.Organization{}, &models.OrgMembership{},
		&models.Queue{}, &models.QueueMembership{}, &models.CustomFieldDef{}, &models.Ticket{},
		&models.Tag{}, &models.TicketTag{}, &models.Note{}, &models.Attachment{}, &models.Watcher{},
		&models.Problem{}, &models.ProblemTicket{}, &models.Webhook{}, &models.WebhookDelivery{},
		&models.Workflow{}, &models.WorkflowTask{}, &models.Approval{}, &models.EventLog{},
	); err != nil {
		return nil, fmt.Errorf("automigrate: %w", err)
	}

	if err := seedDefaultQueue(gdb); err != nil {
		return nil, fmt.Errorf("seed default queue: %w", err)
	}

	if err := renameQueueAdminRole(gdb); err != nil {
		return nil, fmt.Errorf("rename QueueAdmin role: %w", err)
	}

	if err := backfillStageTimestamps(gdb); err != nil {
		return nil, fmt.Errorf("backfill stage timestamps: %w", err)
	}

	switch driver {
	case "sqlite":
		if err := applySQLiteFTS(gdb); err != nil {
			return nil, fmt.Errorf("sqlite fts setup: %w", err)
		}
	case "mysql":
		if err := applyMySQLFTS(gdb); err != nil {
			return nil, fmt.Errorf("mysql fts setup: %w", err)
		}
	}

	return gdb, nil
}

// applyMySQLFTS adds the FULLTEXT indexes MATCH()/AGAINST() needs (3.7).
// Plain CREATE INDEX has no IF NOT EXISTS in MySQL, so duplicate-key-name
// errors (1061) from a second run are swallowed instead.
func applyMySQLFTS(gdb *gorm.DB) error {
	stmts := []string{
		`ALTER TABLE tickets ADD FULLTEXT INDEX idx_tickets_fts (title, description)`,
		`ALTER TABLE notes ADD FULLTEXT INDEX idx_notes_fts (body)`,
	}
	for _, stmt := range stmts {
		if err := gdb.Exec(stmt).Error; err != nil && !strings.Contains(err.Error(), "Duplicate key name") {
			return err
		}
	}
	return nil
}

// renameQueueAdminRole is a one-time data fix for the RoleQueueAdmin ->
// RoleManager rename (DESIGN/02 §2.1.1): any pre-existing user row still
// carrying the old role string is updated in place. Idempotent - a no-op on
// every run after the first, since no row will match "QueueAdmin" again.
func renameQueueAdminRole(gdb *gorm.DB) error {
	return gdb.Exec(`UPDATE users SET role = ? WHERE role = ?`, string(models.RoleManager), "QueueAdmin").Error
}

func seedDefaultQueue(gdb *gorm.DB) error {
	var count int64
	if err := gdb.Model(&models.Queue{}).Where("id = ?", 1).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return gdb.Exec(`INSERT INTO queues (id, name, default_priority, default_category) VALUES (?, ?, ?, ?)`,
		1, "General", "P3", "General").Error
}

func applySQLiteFTS(gdb *gorm.DB) error {
	return gdb.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS tickets_fts USING fts5(title, description, content='tickets', content_rowid='id');
CREATE TRIGGER IF NOT EXISTS tickets_ai AFTER INSERT ON tickets BEGIN
    INSERT INTO tickets_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;
CREATE TRIGGER IF NOT EXISTS tickets_ad AFTER DELETE ON tickets BEGIN
    INSERT INTO tickets_fts(tickets_fts, rowid, title, description) VALUES('delete', old.id, old.title, old.description);
END;
CREATE TRIGGER IF NOT EXISTS tickets_au AFTER UPDATE ON tickets BEGIN
    INSERT INTO tickets_fts(tickets_fts, rowid, title, description) VALUES('delete', old.id, old.title, old.description);
    INSERT INTO tickets_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(body, content='notes', content_rowid='id');
CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts(rowid, body) VALUES (new.id, new.body);
END;
CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, body) VALUES('delete', old.id, old.body);
END;
CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, body) VALUES('delete', old.id, old.body);
    INSERT INTO notes_fts(rowid, body) VALUES (new.id, new.body);
END;
`).Error
}
