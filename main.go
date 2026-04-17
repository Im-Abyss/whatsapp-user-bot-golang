package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/mdp/qrterminal/v3"

	"my-whatsapp-bot/bot/utils"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		fmt.Println("Received a message:", v.Message.GetConversation())
	}
}

func main() {
	_ = godotenv.Load()

	err := os.MkdirAll("sessions", os.ModePerm)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	sheetService, err := utils.InitGoogleSheetsService(ctx, envOrDefault("CREDENTIALS_PATH", "data/service_account.json"))
	if err != nil {
		panic(err)
	}

	spreadsheetID := os.Getenv("SPREADSHEET_ID")
	if spreadsheetID == "" {
		panic("SPREADSHEET_ID is required")
	}

	accountsSheet := envOrDefault("ACCOUNTS_SHEET", "accounts")
	communicationsSheet := envOrDefault("COMMUNICATIONS_SHEET", "communications")
	sentencesPath := os.Getenv("SENTENCES_PATH")
	if sentencesPath == "" {
		panic("SENTENCES_PATH is required")
	}

	accounts, err := utils.GetAccounts(ctx, sheetService, spreadsheetID, accountsSheet)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Загружено аккаунтов из таблицы: %d\n", len(accounts))

	dbPath := "file:sessions/multi.db?_foreign_keys=on"
	dbLog := waLog.Stdout("Database", "INFO", true)

	container, err := sqlstore.New(ctx, "sqlite3", dbPath, dbLog)
	if err != nil {
		panic(err)
	}

	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		panic(err)
	}

	clientLog := waLog.Stdout("Client", "INFO", true)
	clients := make([]*whatsmeow.Client, 0, len(devices))

	if len(devices) == 0 {
		fmt.Println("Нет сохранённых сессий → создаём новую")
		devices = append(devices, container.NewDevice())
	} else {
		fmt.Printf("Найдено существующих сессий: %d\n", len(devices))
	}

	for idx, deviceStore := range devices {
		client := whatsmeow.NewClient(deviceStore, clientLog)
		client.AddEventHandler(eventHandler)

		if len(devices) == 1 && client.Store.ID == nil {
			qrChan, _ := client.GetQRChannel(ctx)

			err = client.Connect()
			if err != nil {
				panic(err)
			}

			fmt.Println("Scan QR code:")
			for evt := range qrChan {
				if evt.Event == "code" {
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				} else {
					fmt.Println("Login event:", evt.Event)
				}
			}
		} else {
			err = client.Connect()
			if err != nil {
				panic(err)
			}
			fmt.Printf("Клиент #%d подключен\n", idx+1)
		}

		clients = append(clients, client)
	}

	clientsByAccountID, mappingReport := mapClientsToAccounts(accounts, clients)
	printSessionMappingReport(mappingReport)

	if envBoolOrDefault("REQUIRE_ALL_ACCOUNTS_MAPPED", false) && len(mappingReport.UnmappedAccountIDs) > 0 {
		panic(fmt.Sprintf("не все account_id сопоставлены с сессиями WhatsApp: %v", mappingReport.UnmappedAccountIDs))
	}

	app := &appContext{
		ctx:                 ctx,
		sheetService:        sheetService,
		spreadsheetID:       spreadsheetID,
		accountsSheet:       accountsSheet,
		communicationsSheet: communicationsSheet,
		sentencesPath:       sentencesPath,
		accounts:            accounts,
		clientsByAccountID:  clientsByAccountID,
	}

	runCommunicationCycle(app)
	go runDailyScheduler(app)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	for _, client := range clients {
		client.Disconnect()
	}
	fmt.Println("Bot disconnected")
}
