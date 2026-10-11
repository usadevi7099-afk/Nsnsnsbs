package main

import (
	"log"

	"main/config"
	"main/database"
	"main/modules"

	"github.com/amarnathcjd/gogram/telegram"
)

func main() {
	config.Load()

	cleanupDB, err := database.Init(config.MongoURL)
	if err != nil {
		log.Fatalf("failed to connect to MongoDB: %v", err)
	}
	defer cleanupDB()

	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:    config.AppID,
		AppHash:  config.AppHash,
		LogLevel: telegram.LogInfo,
	})
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	if _, err := client.Conn(); err != nil {
		log.Fatalf("failed to connect: %v", err)
	}

	if err := client.LoginBot(config.BotToken); err != nil {
		log.Fatalf("failed to login as bot: %v", err)
	}

	me, err := client.GetMe()
	if err != nil {
		log.Fatalf("failed to fetch bot identity: %v", err)
	}
	log.Printf("[main] logged in as @%s", me.Username)

	if err := modules.Load(client); err != nil {
		log.Fatalf("failed to load modules: %v", err)
	}

	log.Println("[main] vote bot is up and running")
	client.Idle()
}
