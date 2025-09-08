package main

import (
	"context"
	"fmt"
	"log"

	"github.com/danielgtaylor/humaclient/example/exampleapiclient"
)

func main() {
	ctx := context.Background()
	client := exampleapiclient.New("http://localhost:8080")

	for item, err := range client.ListThingsPaginator(ctx) {
		if err != nil {
			log.Printf("Error during pagination: %v", err)
			break
		}
		fmt.Printf("Thing: %v\n", item)

		_, thing, err := client.GetThingsByID(ctx, item.ID)
		if err != nil {
			log.Printf("Error getting thing by ID %v: %v", item.ID, err)
			continue
		}
		fmt.Printf("Fetched thing by ID: %v, name: %s\n", thing.ID, thing.Name)
	}
}
