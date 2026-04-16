package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	accounts, err := utils.GetAccounts(ctx, sheetService, spreadsheetID, accountsSheet)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Загружено аккаунтов из таблицы: %d\n", len(accounts))

	today := time.Now().Format("2006-01-02")
	disabledCount, err := utils.DisableExpiredCommunicationTasks(ctx, sheetService, spreadsheetID, communicationsSheet, today)
	if err != nil {
		panic(err)
	}
	if disabledCount > 0 {
		fmt.Printf("Отключено завершенных коммуникаций: %d\n", disabledCount)
	}

	tasks, err := utils.GetCommunicationTasks(ctx, sheetService, spreadsheetID, communicationsSheet)
	if err != nil {
		panic(err)
	}
	activeTasks, err := utils.GetActiveCommunicationTasks(tasks, today)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Активных коммуникаций на %s: %d\n", today, len(activeTasks))

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

	bot := &utils.MyBot{
		WAClient: clients[0],
		JsonPath: os.Getenv("SENTENCES_PATH"),
	}

	if len(activeTasks) > 0 {
		firstTask := activeTasks[0]
		if account, ok := accounts[firstTask.AccountB]; ok {
			go func(phone string) {
				for {
					bot.SendRandomText(phone)
					time.Sleep(15 * time.Second)
					break
				}
			}(account.Phone)
		} else {
			fmt.Printf("Аккаунт account_b=%d не найден в листе аккаунтов\n", firstTask.AccountB)
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	for _, client := range clients {
		client.Disconnect()
	}
	fmt.Println("Bot disconnected")
}

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
