package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"

	"my-whatsapp-bot/bot/utils"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
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

	var deviceStore *store.Device

	if len(devices) == 0 {
		fmt.Println("Нет сохранённых сессий → создаём новую")
		deviceStore = container.NewDevice()
	} else {
		fmt.Println("Найдена существующая сессия")
		deviceStore = devices[0] // пока берём первую
	}

	clientLog := waLog.Stdout("Client", "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)
	client.AddEventHandler(eventHandler)

	bot := &utils.MyBot{
		WAClient: client,
		JsonPath: os.Getenv("SENTENCES_PATH"),
	}

	if client.Store.ID == nil {
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
	}

	go func() {
		for {
			bot.SendRandomText("79381248273")
			time.Sleep(15 * time.Second)
			break
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	client.Disconnect()
	fmt.Println("Bot disconnected")
}