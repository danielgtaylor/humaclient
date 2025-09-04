package main

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/humaclient/example/exampleapiclient"
)

func main() {
	client := exampleapiclient.New("http://localhost:8080")

	_, things, err := client.ListThings(context.Background())
	if err != nil {
		fmt.Printf("Error listing things: %v\n", err)
		return
	}

	fmt.Printf("Things: %v\n", things)
}
