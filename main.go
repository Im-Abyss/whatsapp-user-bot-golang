package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/mdp/qrterminal/v3"

	"my-whatsapp-bot/bot/utils"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
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

func runDailyScheduler(app *appContext) {
	runTime := envOrDefault("DAILY_COMMUNICATION_TIME", defaultRunTime)
	locationName := envOrDefault("DAILY_COMMUNICATION_TZ", defaultRunLocation)

	loc, err := time.LoadLocation(locationName)
	if err != nil {
		fmt.Printf("Некорректная timezone %q, используем UTC\n", locationName)
		loc = time.UTC
	}

	for {
		nextRun, err := getNextRunTime(time.Now().In(loc), runTime, loc)
		if err != nil {
			fmt.Printf("Некорректное DAILY_COMMUNICATION_TIME=%q, используем %s\n", runTime, defaultRunTime)
			nextRun, _ = getNextRunTime(time.Now().In(loc), defaultRunTime, loc)
		}
		wait := time.Until(nextRun)
		fmt.Printf("Следующая ежедневная проверка коммуникаций: %s (%s)\n", nextRun.Format(time.RFC3339), loc)
		time.Sleep(wait)
		runCommunicationCycle(app)
	}
}

func runCommunicationCycle(app *appContext) {
	today := time.Now().UTC().Format(communicationDateFmt)
	fmt.Printf("Запуск коммуникационного цикла за %s\n", today)

	disabledCount, err := utils.DisableExpiredCommunicationTasks(app.ctx, app.sheetService, app.spreadsheetID, app.communicationsSheet, today)
	if err != nil {
		fmt.Printf("Ошибка отключения завершённых коммуникаций: %v\n", err)
		return
	}
	if disabledCount > 0 {
		fmt.Printf("Отключено завершенных коммуникаций: %d\n", disabledCount)
	}

	tasks, err := utils.GetCommunicationTasks(app.ctx, app.sheetService, app.spreadsheetID, app.communicationsSheet)
	if err != nil {
		fmt.Printf("Ошибка загрузки коммуникаций: %v\n", err)
		return
	}

	activeTasks, err := getDateActiveTasks(tasks, today)
	if err != nil {
		fmt.Printf("Ошибка отбора активных задач: %v\n", err)
		return
	}
	fmt.Printf("Активных коммуникаций на %s: %d\n", today, len(activeTasks))

	var wg sync.WaitGroup
	for _, task := range activeTasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			runTaskDialogue(app, task)
		}()
	}
	wg.Wait()
}

func runTaskDialogue(app *appContext, task utils.CommunicationTask) {
	accountA, ok := app.accounts[task.AccountA]
	if !ok {
		fmt.Printf("Task %d: account_a=%d не найден в листе аккаунтов\n", task.TaskID, task.AccountA)
		return
	}
	accountB, ok := app.accounts[task.AccountB]
	if !ok {
		fmt.Printf("Task %d: account_b=%d не найден в листе аккаунтов\n", task.TaskID, task.AccountB)
		return
	}

	clientA, ok := app.clientsByAccountID[task.AccountA]
	if !ok {
		fmt.Printf("Task %d: WhatsApp-клиент для account_a=%d не найден\n", task.TaskID, task.AccountA)
		return
	}
	clientB, ok := app.clientsByAccountID[task.AccountB]
	if !ok {
		fmt.Printf("Task %d: WhatsApp-клиент для account_b=%d не найден\n", task.TaskID, task.AccountB)
		return
	}

	if err := utils.EnableCommunicationTask(app.ctx, app.sheetService, app.spreadsheetID, app.communicationsSheet, task); err != nil {
		fmt.Printf("Task %d: не удалось обновить enabled/count_days: %v\n", task.TaskID, err)
	}

	intervalMin, intervalMax := getIntervalRange()
	fmt.Printf("Task %d: старт диалога %d ↔ %d, интервал %d-%d минут\n", task.TaskID, task.AccountA, task.AccountB, intervalMin, intervalMax)

	for round := 1; round <= communicationRounds; round++ {
		if err := utils.SendRandomTextWithClient(clientA, app.sentencesPath, accountB.Phone); err != nil {
			fmt.Printf("Task %d: ошибка отправки A→B (round %d): %v\n", task.TaskID, round, err)
			return
		}
		if round == communicationRounds {
			break
		}
		time.Sleep(randomDuration(intervalMin, intervalMax))

		if err := utils.SendRandomTextWithClient(clientB, app.sentencesPath, accountA.Phone); err != nil {
			fmt.Printf("Task %d: ошибка отправки B→A (round %d): %v\n", task.TaskID, round, err)
			return
		}
		time.Sleep(randomDuration(intervalMin, intervalMax))
	}

	if err := utils.SendRandomTextWithClient(clientB, app.sentencesPath, accountA.Phone); err != nil {
		fmt.Printf("Task %d: ошибка финальной отправки B→A: %v\n", task.TaskID, err)
		return
	}
	fmt.Printf("Task %d: диалог завершен\n", task.TaskID)
}

func getDateActiveTasks(tasks []utils.CommunicationTask, date string) ([]utils.CommunicationTask, error) {
	if _, err := time.Parse(communicationDateFmt, date); err != nil {
		return nil, fmt.Errorf("invalid date %q: expected %s", date, communicationDateFmt)
	}

	active := make([]utils.CommunicationTask, 0)
	for _, task := range tasks {
		if task.StartDate <= date && task.EndDate >= date {
			active = append(active, task)
		}
	}
	return active, nil
}

func getNextRunTime(now time.Time, hhmm string, loc *time.Location) (time.Time, error) {
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid HH:MM format")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("hour/minute out of range")
	}

	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next, nil
}

func getIntervalRange() (int, int) {
	minInterval := envIntOrDefault("COMMUNICATION_INTERVAL_MINUTES_MIN", defaultIntervalMin)
	maxInterval := envIntOrDefault("COMMUNICATION_INTERVAL_MINUTES_MAX", defaultIntervalMax)
	if minInterval > maxInterval {
		minInterval, maxInterval = maxInterval, minInterval
	}
	if minInterval <= 0 {
		minInterval = defaultIntervalMin
	}
	if maxInterval <= 0 {
		maxInterval = defaultIntervalMax
	}
	return minInterval, maxInterval
}

func randomDuration(minMinutes, maxMinutes int) time.Duration {
	if minMinutes == maxMinutes {
		return time.Duration(minMinutes) * time.Minute
	}
	n := rand.Intn(maxMinutes-minMinutes+1) + minMinutes
	return time.Duration(n) * time.Minute
}

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

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
