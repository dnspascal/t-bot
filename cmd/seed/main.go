package main

import (
	"context"
	"log"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/seed"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {

	cfg, err := config.Load()

	if err != nil {
		log.Fatal(err)
	}

	db, err := pgxpool.New(context.Background(), cfg.DatabaseURL)

	if err != nil {
		log.Fatal(err)
	}

	defer db.Close()

	ctx := context.Background()

	if err := seed.SeedSymbols(ctx, db); err != nil {
		log.Fatal(err)
	}

	log.Println("seeding complete")

}
