package input_files

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type idCountPair struct {
	ID    string
	Count int
}

type Record struct {
	ResourceType       string                 `json:"resource_type"`
	ReporterType       string                 `json:"reporter_type"`
	ReporterInstanceID string                 `json:"reporter_instance_id"`
	LocalResourceID    string                 `json:"local_resource_id"`
	APIHref            string                 `json:"api_href"`
	ConsoleHref        string                 `json:"console_href"`
	ReporterVersion    string                 `json:"reporter_version"`
	Common             map[string]string      `json:"common,omitempty"`
	Reporter           map[string]interface{} `json:"reporter,omitempty"`
}

func Categorize(idNum uint64) string {
	switch {
	case idNum < 30:
		return "cat1"
	case idNum < 60:
		return "cat2"
	case idNum < 70:
		return "cat3"
	case idNum < 80:
		return "cat4"
	case idNum < 90:
		return "cat5"
	default:
		return "cat6"
	}
}

func generateK8sClusterReporter() map[string]interface{} {
	return map[string]interface{}{
		"external_cluster_id": randomString(8),
		"cluster_status":      "READY",
		"kube_version":        fmt.Sprintf("1.%d", rand.Intn(10)+20),
		"kube_vendor":         "OPENSHIFT",
		"vendor_version":      fmt.Sprintf("4.%d", rand.Intn(20)+1),
		"cloud_platform":      "AWS_UPI",
		"nodes": []map[string]interface{}{
			{
				"name":   fmt.Sprintf("%s.example.com", randomString(6)),
				"cpu":    fmt.Sprintf("%dm", rand.Intn(10000)),
				"memory": fmt.Sprintf("%dKi", rand.Intn(40000000)),
				"labels": []map[string]string{
					{"key": "has_monster_gpu", "value": "yes"},
				},
			},
		},
	}
}

func generateHostReporter() map[string]interface{} {
	return map[string]interface{}{
		"satellite_id":          uuid.New().String(),
		"sub_manager_id":        uuid.New().String(),
		"insights_inventory_id": uuid.New().String(),
		"ansible_host":          fmt.Sprintf("host-%d", rand.Intn(100)),
	}
}

func GenerateZipfIDsWithModuloCategory(n int, zipfMax uint64, modBase uint64, s, v float64, outputPath string) map[string][]string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	zipf := rand.NewZipf(r, s, v, zipfMax)

	categorized := map[string][]string{
		"cat1": {}, "cat2": {}, "cat3": {},
		"cat4": {}, "cat5": {}, "cat6": {},
	}

	counts := make(map[string]int)
	records := []Record{}

	for i := 0; i < n; i++ {
		raw := zipf.Uint64()
		modID := raw % modBase
		idStr := "id-" + strconv.FormatUint(raw, 10)
		fmt.Println(idStr)
		counts[idStr]++

		cat := Categorize(modID)
		categorized[cat] = append(categorized[cat], idStr)

		record := Record{
			ResourceType:       "host",
			ReporterInstanceID: "abc",
			LocalResourceID:    strconv.FormatUint(raw, 10),
			APIHref:            "www.example.com",
			ConsoleHref:        "www.example.com",
			ReporterVersion:    "123.2",
		}

		switch cat {
		case "cat1":
			record.ReporterType = "hbi"
			record.Reporter = generateHostReporter()
		case "cat2":
			record.ReporterType = "acm"
			record.Reporter = generateK8sClusterReporter()
		case "cat3":
			record.ReporterType = "hbi"
			record.Common = map[string]string{"workspaceId": randomString(6)}
		case "cat4":
			record.ReporterType = "acm"
			record.Common = map[string]string{"workspaceId": randomString(6)}
		case "cat5":
			record.ReporterType = "hbi"
			record.Reporter = generateHostReporter()
			record.Common = map[string]string{"workspaceId": randomString(6)}
		case "cat6":
			record.ReporterType = "acm"
			record.Reporter = generateK8sClusterReporter()
			record.Common = map[string]string{"workspaceId": randomString(6)}
		}

		records = append(records, record)
	}

	// Write to file
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

	// Print sorted frequency summary
	fmt.Println("\nSorted IDs by frequency:")
	var sorted []idCountPair
	for id, count := range counts {
		sorted = append(sorted, idCountPair{ID: id, Count: count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Count > sorted[j].Count
	})
	for _, pair := range sorted {
		fmt.Printf("%s: %d\n", pair.ID, pair.Count)
	}

	return categorized
}

func randomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}
