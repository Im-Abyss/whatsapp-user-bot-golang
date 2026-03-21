package utils

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type AccountConfig struct {
	AccountID int
	PhNumber  string
}

type SheetsManager struct {
	Service       *sheets.Service
	SpreadsheetID string
}

func NewSheetsManager(spreadsheetID string, credentialsPath string) (*SheetsManager, error) {
	ctx := context.Background()
	srv, err := sheets.NewService(ctx, option.WithCredentialsFile(credentialsPath))
	if err != nil {
		return nil, fmt.Errorf("ошибка API: %v", err)
	}
	return &SheetsManager{Service: srv, SpreadsheetID: spreadsheetID}, nil
}

func (sm *SheetsManager) GetPhNumber(accountID int) (string, error) {
	readRange := "WhatsApp Config!A2:B"
	resp, err := sm.Service.Spreadsheets.Values.Get(sm.SpreadsheetID, readRange).Do()
	if err != nil {
		return "", fmt.Errorf("не удалось получить данные: %v", err)
	}

	for _, row := range resp.Values {
		if len(row) < 2 {
			continue
		}

		id, _ := strconv.Atoi(fmt.Sprintf("%v", row[0]))

		if id == accountID {
			return fmt.Sprintf("%v", row[1]), nil
		}
	}

	return "", fmt.Errorf("account_id %d не найден", accountID)
}
