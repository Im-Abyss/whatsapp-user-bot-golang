package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"my-whatsapp-bot/bot/utils"
)

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
