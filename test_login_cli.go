package main

import (
	"context"
	"fmt"
	"log"

	"github.com/joho/godotenv"

	"TDrive/backend/auth"
)

func main() {
	fmt.Println("--- Telegram Login Test ---")

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file. Make sure it exists in this folder!")
	}

	client, err := auth.Connect()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()

	err = client.Run(ctx, func(ctx context.Context) error {
		fmt.Println("Connection established. Starting login flow...")
		return auth.StartLogin(ctx, client)
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n*********************************")
	fmt.Println("SUCCESS: You are logged in!")
	fmt.Println("*********************************")
}
