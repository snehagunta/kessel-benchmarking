package input_files

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"time"
)

func GenerateAllUniqueHBIMigrationRecords(n int, outputPath string) []string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	ids := make([]uint64, n)
	for i := uint64(0); i < uint64(n); i++ {
		ids[i] = i
	}

	// Shuffle for randomness
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	records := []Record{}
	idList := []string{}

	for _, raw := range ids {
		idStr := "id-" + strconv.FormatUint(raw, 10)
		idList = append(idList, idStr)

		record := Record{
			ResourceType:       "host",
			ReporterType:       "hbi",
			ReporterInstanceID: "abc",
			LocalResourceID:    strconv.FormatUint(raw, 10),
			APIHref:            "www.example.com",
			ConsoleHref:        "www.example.com",
			ReporterVersion:    "123.2",
			Reporter:           generateHostReporter(),
			Common:             map[string]string{"workspaceId": randomString(6)},
		}
		records = append(records, record)
	}

	// Write JSONL file
	file, err := os.Create(outputPath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, r := range records {
		if err := encoder.Encode(r); err != nil {
			fmt.Fprintf(os.Stderr, "error writing record: %v\n", err)
		}
	}

	return idList
}
