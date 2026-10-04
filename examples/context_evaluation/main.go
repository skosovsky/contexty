// Context evaluation runs deterministic offline callbacks and emits JSON.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/skosovsky/contexty/examples/context_evaluation/evaluation"
)

func main() {
	report, err := evaluation.Run(context.Background(), evaluation.OfflineConfig())
	if err != nil {
		log.Fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		log.Fatal(err)
	}
}
