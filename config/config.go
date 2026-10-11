package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	AppID          int32
	AppHash        string
	BotToken       string
	MongoURL       string
	SupportGroup   string
	SupportChannel string

	FsubIDs []int64

	StartImage  string
	VoteImage   string
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		panic("missing required env var: " + key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func Load() {
	godotenv.Load()

	appID, err := strconv.Atoi(mustEnv("APP_ID"))
	if err != nil {
		panic("APP_ID must be an integer: " + err.Error())
	}
	AppID = int32(appID)

	AppHash = mustEnv("APP_HASH")
	BotToken = mustEnv("BOT_TOKEN")
	MongoURL = mustEnv("MONGO_URL")
	SupportGroup = mustEnv("SUPPORT_GROUP")
	SupportChannel = mustEnv("SUPPORT_CHANNEL")

	raw := mustEnv("FSUB_CHAT")
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			panic("FSUB_CHAT must be a comma-separated list of numeric channel IDs (e.g. -100123,-100456): " + err.Error())
		}
		FsubIDs = append(FsubIDs, id)
	}
	if len(FsubIDs) == 0 {
		panic("FSUB_CHAT must contain at least one channel ID")
	}

	StartImage = envOr("START_IMAGE", "")
	VoteImage = envOr("VOTE_IMAGE", "")
}
