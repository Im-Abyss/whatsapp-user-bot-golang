package main

import (
	"fmt"
	"strings"

	"my-whatsapp-bot/bot/utils"

	"go.mau.fi/whatsmeow"
)

func mapClientsToAccounts(accounts map[int64]utils.Account, clients []*whatsmeow.Client) (map[int64]*whatsmeow.Client, sessionMappingReport) {
	result := make(map[int64]*whatsmeow.Client)
	phoneToAccountID := make(map[string]int64)
	report := sessionMappingReport{
		TotalAccounts: len(accounts),
		TotalSessions: len(clients),
	}

	for accountID, account := range accounts {
		normalized := normalizePhone(account.Phone)
		if normalized == "" {
			continue
		}
		phoneToAccountID[normalized] = accountID
	}

	matchedSessionPhone := make(map[string]bool)
	for _, client := range clients {
		if client.Store == nil || client.Store.ID == nil {
			continue
		}
		clientPhone := normalizePhone(client.Store.ID.User)
		if accountID, ok := phoneToAccountID[clientPhone]; ok {
			result[accountID] = client
			matchedSessionPhone[clientPhone] = true
			continue
		}
		report.UnmappedSessionPhone = append(report.UnmappedSessionPhone, clientPhone)
	}

	for accountID, account := range accounts {
		accountPhone := normalizePhone(account.Phone)
		if accountPhone == "" {
			report.UnmappedAccountIDs = append(report.UnmappedAccountIDs, accountID)
			continue
		}
		if !matchedSessionPhone[accountPhone] {
			report.UnmappedAccountIDs = append(report.UnmappedAccountIDs, accountID)
		}
	}

	report.Mapped = len(result)
	return result, report
}

func printSessionMappingReport(report sessionMappingReport) {
	fmt.Printf(
		"Сопоставление сессий: аккаунтов=%d, сессий=%d, сопоставлено=%d\n",
		report.TotalAccounts,
		report.TotalSessions,
		report.Mapped,
	)
	if len(report.UnmappedAccountIDs) > 0 {
		fmt.Printf("Не найдено сессий для account_id: %v\n", report.UnmappedAccountIDs)
	}
	if len(report.UnmappedSessionPhone) > 0 {
		fmt.Printf("Найдены сессии без account_id в таблице accounts (по номеру): %v\n", report.UnmappedSessionPhone)
	}
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
