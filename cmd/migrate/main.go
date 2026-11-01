package main

import (
	"context"
	"flag"
	"log"

	"llm-gateway/db"
	"llm-gateway/internal/config"
)

func main() {
	path := flag.String("config", "configs/local.yaml", "configuration file")
	flag.Parse()
	c, e := config.Load(*path)
	if e == nil {
		e = db.Migrate(context.Background(), c.DatabaseURL)
	}
	if e != nil {
		log.Fatal(e)
	}
}
