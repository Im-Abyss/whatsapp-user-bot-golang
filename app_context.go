package main

import (
	"context"

	"my-whatsapp-bot/bot/utils"

	"go.mau.fi/whatsmeow"
	"google.golang.org/api/sheets/v4"
)

const (
	defaultRunTime       = "10:00"
	defaultRunLocation   = "UTC"
	defaultIntervalMin   = 40
	defaultIntervalMax   = 60
	communicationRounds  = 3
	communicationDateFmt = "2006-01-02"
)

type appContext struct {
	ctx                 context.Context
	sheetService        *sheets.Service
	spreadsheetID       string
	accountsSheet       string
	communicationsSheet string
	sentencesPath       string
	accounts            map[int64]utils.Account
	clientsByAccountID  map[int64]*whatsmeow.Client
}

type sessionMappingReport struct {
	TotalAccounts        int
	TotalSessions        int
	Mapped               int
	UnmappedAccountIDs   []int64
	UnmappedSessionPhone []string
}
