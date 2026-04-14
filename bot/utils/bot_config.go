package utils

import (
    "encoding/json"
    "fmt"
    "math/rand"
    "os"
    "time"
	"context"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	waProto "go.mau.fi/whatsmeow/binary/proto"
)


type MessageGenerator struct {
    Sentences []string `json:"sentences"`
}

func GetRandomMessage(filePath string) (string, error) {
    file, err := os.ReadFile(filePath)
    if err != nil {
        return "", fmt.Errorf("ошибка чтения файла: %v", err)
    }

    var data MessageGenerator
    if err := json.Unmarshal(file, &data); err != nil {
        return "", fmt.Errorf("ошибка парсинга JSON: %v", err)
    }

    r := rand.New(rand.NewSource(time.Now().UnixNano()))
    
    n := len(data.Sentences)
    if n < 2 {
        return "", fmt.Errorf("недостаточно фраз в файле")
    }

    idx1 := r.Intn(n)
    idx2 := r.Intn(n)
    for idx1 == idx2 {
        idx2 = r.Intn(n)
    }

    return fmt.Sprintf("%s. %s.", data.Sentences[idx1], data.Sentences[idx2]), nil
}


type MyBot struct {
    WAClient       *whatsmeow.Client
    AccountID      int
    JsonPath      string
}


func NewBot(accountID int, deviceStore *store.Device, jsonPath string) *MyBot {
    client := whatsmeow.NewClient(deviceStore, nil)
    return &MyBot{
        WAClient:  client,
        AccountID: accountID,
        JsonPath:  jsonPath,
    }
}


func (bot *MyBot) SendRandomText(phoneNumber string) {
    jid := types.NewJID(phoneNumber, types.DefaultUserServer)

    text, err := GetRandomMessage(bot.JsonPath)
	if err != nil {
		fmt.Println("Ошибка генерации текста:", err)
		return
	}

	msg := &waProto.Message{
		Conversation: proto.String(text),
	}

    bot.WAClient.SendMessage(context.Background(), jid, msg)
}


