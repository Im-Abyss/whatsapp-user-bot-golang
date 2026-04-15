package utils

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

const communicationsTableDDL = `
CREATE TABLE IF NOT EXISTS communications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_1 INTEGER NOT NULL,
    account_2 INTEGER NOT NULL,
    start_date TEXT NOT NULL,
    end_date TEXT NOT NULL,
    is_enabled INTEGER NOT NULL DEFAULT 0 CHECK (is_enabled IN (0, 1)),
    days_count INTEGER GENERATED ALWAYS AS (
        CASE
            WHEN julianday(end_date) >= julianday(start_date)
                THEN CAST(julianday(end_date) - julianday(start_date) + 1 AS INTEGER)
            ELSE 0
        END
    ) STORED,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (date(start_date) IS NOT NULL),
    CHECK (date(end_date) IS NOT NULL)
);

CREATE TRIGGER IF NOT EXISTS communications_set_updated_at
AFTER UPDATE ON communications
FOR EACH ROW
BEGIN
    UPDATE communications
    SET updated_at = CURRENT_TIMESTAMP
    WHERE id = OLD.id;
END;
`

func InitCommunicationDB() (*sql.DB, error) {
	if err := os.MkdirAll("data", os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	db, err := sql.Open("sqlite3", "data/communications.db")
	if err != nil {
		return nil, fmt.Errorf("failed to open communication db: %w", err)
	}

	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to connect communication db: %w", err)
	}

	if _, err = db.Exec(communicationsTableDDL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create communications schema: %w", err)
	}

	return db, nil
}

const communicationDateLayout = "2006-01-02"

type CommunicationTask struct {
	ID        int64
	Account1  int64
	Account2  int64
	StartDate string
	EndDate   string
	IsEnabled bool
	DaysCount int64
	CreatedAt string
	UpdatedAt string
}

func InsertCommunicationTask(
	db *sql.DB,
	account1 int64,
	account2 int64,
	startDate string,
	endDate string,
	isEnabled bool,
) (int64, error) {
	if err := validateCommunicationDates(startDate, endDate); err != nil {
		return 0, err
	}

	status := 0
	if isEnabled {
		status = 1
	}

	result, err := db.Exec(
		`INSERT INTO communications (account_1, account_2, start_date, end_date, is_enabled)
		 VALUES (?, ?, ?, ?, ?)`,
		account1,
		account2,
		startDate,
		endDate,
		status,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert communication task: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch inserted communication task id: %w", err)
	}

	return id, nil
}

func GetCommunicationTasks(db *sql.DB) ([]CommunicationTask, error) {
	rows, err := db.Query(
		`SELECT id, account_1, account_2, start_date, end_date, is_enabled, days_count, created_at, updated_at
		 FROM communications
		 ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query communication tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]CommunicationTask, 0)
	for rows.Next() {
		task, err := scanCommunicationTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate communication tasks: %w", err)
	}

	return tasks, nil
}

func GetActiveCommunicationTasks(db *sql.DB, date string) ([]CommunicationTask, error) {
	if _, err := time.Parse(communicationDateLayout, date); err != nil {
		return nil, fmt.Errorf("invalid date %q: expected %s", date, communicationDateLayout)
	}

	rows, err := db.Query(
		`SELECT id, account_1, account_2, start_date, end_date, is_enabled, days_count, created_at, updated_at
		 FROM communications
		 WHERE start_date <= ? AND end_date >= ?
		 ORDER BY id`,
		date,
		date,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active communication tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]CommunicationTask, 0)
	for rows.Next() {
		task, err := scanCommunicationTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate active communication tasks: %w", err)
	}

	return tasks, nil
}

func UpdateCommunicationStatus(db *sql.DB, taskID int64, isEnabled bool) error {
	status := 0
	if isEnabled {
		status = 1
	}

	_, err := db.Exec(`UPDATE communications SET is_enabled = ? WHERE id = ?`, status, taskID)
	if err != nil {
		return fmt.Errorf("failed to update communication task status: %w", err)
	}
	return nil
}

func DisableExpiredCommunicationTasks(db *sql.DB, today string) (int64, error) {
	if _, err := time.Parse(communicationDateLayout, today); err != nil {
		return 0, fmt.Errorf("invalid date %q: expected %s", today, communicationDateLayout)
	}

	result, err := db.Exec(
		`UPDATE communications
		 SET is_enabled = 0
		 WHERE end_date < ? AND is_enabled = 1`,
		today,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to disable expired communication tasks: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to fetch affected rows for expired tasks update: %w", err)
	}

	return affected, nil
}

func scanCommunicationTask(scanner interface {
	Scan(dest ...any) error
}) (CommunicationTask, error) {
	var task CommunicationTask
	var enabled int
	if err := scanner.Scan(
		&task.ID,
		&task.Account1,
		&task.Account2,
		&task.StartDate,
		&task.EndDate,
		&enabled,
		&task.DaysCount,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return CommunicationTask{}, fmt.Errorf("failed to scan communication task: %w", err)
	}

	task.IsEnabled = enabled == 1
	return task, nil
}

func validateCommunicationDates(startDate string, endDate string) error {
	start, err := time.Parse(communicationDateLayout, startDate)
	if err != nil {
		return fmt.Errorf("invalid start_date %q: expected %s", startDate, communicationDateLayout)
	}
	end, err := time.Parse(communicationDateLayout, endDate)
	if err != nil {
		return fmt.Errorf("invalid end_date %q: expected %s", endDate, communicationDateLayout)
	}

	if end.Before(start) {
		return fmt.Errorf("invalid date range: end_date (%s) is before start_date (%s)", endDate, startDate)
	}

	return nil
}
