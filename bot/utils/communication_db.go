package utils

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const communicationDateLayout = "2006-01-02"

type Account struct {
	AccountID int64
	Phone     string
}

type CommunicationTask struct {
	TaskID     int64
	AccountA   int64
	AccountB   int64
	StartDate  string
	EndDate    string
	Enabled    bool
	CountDays  int64
	SheetRowID int64
}

func InitGoogleSheetsService(ctx context.Context, credentialsPath string) (*sheets.Service, error) {
	srv, err := sheets.NewService(ctx,
		option.WithCredentialsFile(credentialsPath),
		option.WithScopes(sheets.SpreadsheetsScope),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize google sheets client: %w", err)
	}
	return srv, nil
}

func GetAccounts(
	ctx context.Context,
	srv *sheets.Service,
	spreadsheetID string,
	sheetName string,
) (map[int64]Account, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, sheetName+"!A:Z").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to load accounts sheet: %w", err)
	}
	if len(resp.Values) == 0 {
		return nil, fmt.Errorf("accounts sheet %q is empty", sheetName)
	}

	headers := parseHeaders(resp.Values[0])
	accountIDIdx, ok := headers["account_id"]
	if !ok {
		return nil, fmt.Errorf("accounts sheet %q must contain column account_id", sheetName)
	}
	phoneIdx, ok := headers["ph_number"]
	if !ok {
		return nil, fmt.Errorf("accounts sheet %q must contain column ph_number", sheetName)
	}

	accounts := make(map[int64]Account)
	for row := 1; row < len(resp.Values); row++ {
		line := resp.Values[row]
		accountID, err := getIntCell(line, accountIDIdx)
		if err != nil {
			continue
		}
		phone := getStringCell(line, phoneIdx)
		if phone == "" {
			continue
		}
		accounts[accountID] = Account{AccountID: accountID, Phone: normalizePhone(phone)}
	}

	return accounts, nil
}

func GetCommunicationTasks(
	ctx context.Context,
	srv *sheets.Service,
	spreadsheetID string,
	sheetName string,
) ([]CommunicationTask, error) {
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, sheetName+"!A:Z").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to load communications sheet: %w", err)
	}
	if len(resp.Values) == 0 {
		return nil, fmt.Errorf("communications sheet %q is empty", sheetName)
	}

	headers := parseHeaders(resp.Values[0])
	taskIDIdx, ok := headers["task_id"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column task_id", sheetName)
	}
	accountAIdx, ok := headers["account_a"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column account_a", sheetName)
	}
	accountBIdx, ok := headers["account_b"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column account_b", sheetName)
	}
	startDateIdx, ok := headers["start_date"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column start_date", sheetName)
	}
	endDateIdx, ok := headers["end_date"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column end_date", sheetName)
	}
	enabledIdx, ok := headers["enabled"]
	if !ok {
		return nil, fmt.Errorf("communications sheet %q must contain column enabled", sheetName)
	}
	countDaysIdx, hasCountDays := headers["count_days"]

	tasks := make([]CommunicationTask, 0, len(resp.Values)-1)
	for row := 1; row < len(resp.Values); row++ {
		line := resp.Values[row]
		taskID, err := getIntCell(line, taskIDIdx)
		if err != nil {
			continue
		}
		accountA, err := getIntCell(line, accountAIdx)
		if err != nil {
			continue
		}
		accountB, err := getIntCell(line, accountBIdx)
		if err != nil {
			continue
		}

		startDate := getStringCell(line, startDateIdx)
		endDate := getStringCell(line, endDateIdx)
		if err := validateCommunicationDates(startDate, endDate); err != nil {
			continue
		}

		task := CommunicationTask{
			TaskID:     taskID,
			AccountA:   accountA,
			AccountB:   accountB,
			StartDate:  startDate,
			EndDate:    endDate,
			Enabled:    parseBoolCell(line, enabledIdx),
			CountDays:  calculateCountDays(startDate, endDate),
			SheetRowID: int64(row + 1),
		}
		if hasCountDays {
			if sheetCountDays, err := getIntCell(line, countDaysIdx); err == nil {
				task.CountDays = sheetCountDays
			}
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

func DisableExpiredCommunicationTasks(
	ctx context.Context,
	srv *sheets.Service,
	spreadsheetID string,
	sheetName string,
	today string,
) (int, error) {
	if _, err := time.Parse(communicationDateLayout, today); err != nil {
		return 0, fmt.Errorf("invalid date %q: expected %s", today, communicationDateLayout)
	}

	tasks, err := GetCommunicationTasks(ctx, srv, spreadsheetID, sheetName)
	if err != nil {
		return 0, err
	}

	updates := make([]*sheets.ValueRange, 0)
	disabledCount := 0
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		if task.EndDate < today {
			disabledCount++
			updates = append(updates,
				&sheets.ValueRange{Range: fmt.Sprintf("%s!F%d", sheetName, task.SheetRowID), Values: [][]interface{}{{"FALSE"}}},
				&sheets.ValueRange{Range: fmt.Sprintf("%s!G%d", sheetName, task.SheetRowID), Values: [][]interface{}{{calculateCountDays(task.StartDate, task.EndDate)}}},
			)
		}
	}

	if len(updates) == 0 {
		return 0, nil
	}

	_, err = srv.Spreadsheets.Values.BatchUpdate(spreadsheetID, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "USER_ENTERED",
		Data:             updates,
	}).Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("failed to update expired tasks in sheet: %w", err)
	}

	return disabledCount, nil
}

func GetActiveCommunicationTasks(tasks []CommunicationTask, date string) ([]CommunicationTask, error) {
	if _, err := time.Parse(communicationDateLayout, date); err != nil {
		return nil, fmt.Errorf("invalid date %q: expected %s", date, communicationDateLayout)
	}

	active := make([]CommunicationTask, 0)
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		if task.StartDate <= date && task.EndDate >= date {
			active = append(active, task)
		}
	}

	return active, nil
}

func parseHeaders(headerRow []interface{}) map[string]int {
	headers := make(map[string]int)
	for idx, cell := range headerRow {
		headers[strings.ToLower(strings.TrimSpace(fmt.Sprint(cell)))] = idx
	}
	return headers
}

func getStringCell(row []interface{}, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(row[idx]))
}

func getIntCell(row []interface{}, idx int) (int64, error) {
	value := getStringCell(row, idx)
	if value == "" {
		return 0, fmt.Errorf("empty integer value")
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		return number, nil
	}
	floatValue, floatErr := strconv.ParseFloat(value, 64)
	if floatErr != nil {
		return 0, fmt.Errorf("invalid integer value %q", value)
	}
	return int64(floatValue), nil
}

func parseBoolCell(row []interface{}, idx int) bool {
	value := strings.ToLower(getStringCell(row, idx))
	return value == "true" || value == "1" || value == "yes"
}

func calculateCountDays(startDate string, endDate string) int64 {
	start, err := time.Parse(communicationDateLayout, startDate)
	if err != nil {
		return 0
	}
	end, err := time.Parse(communicationDateLayout, endDate)
	if err != nil || end.Before(start) {
		return 0
	}
	return int64(end.Sub(start).Hours()/24) + 1
}

func normalizePhone(phone string) string {
	builder := strings.Builder{}
	for _, ch := range phone {
		if ch >= '0' && ch <= '9' {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
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
