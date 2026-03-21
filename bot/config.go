package bot

import (
    "log"
    "os"

    "github.com/joho/godotenv"
)

type Settings struct {
	SpreadsheetID   string
	CredentialsPath string
	SentencesPath   string
}

var SettingsInstance Settings

func LoadSettings() {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	SettingsInstance = Settings{
		SpreadsheetID:   os.Getenv("SPREADSHEET_ID"),
		CredentialsPath: os.Getenv("CREDENTIALS_PATH"),
		SentencesPath:   os.Getenv("SENTENCES_PATH"),
	}

	if SettingsInstance.SpreadsheetID == "" || SettingsInstance.CredentialsPath == "" || SettingsInstance.SentencesPath == "" {
		log.Fatal("One or more required environment variables are missing")
	}
}

func getEnv(key, defaultVal string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultVal
}