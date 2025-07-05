package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

type ResourceChange struct {
	Type   string `json:"type"`
	Change struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type PlanJSON struct {
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

var (
	resourcesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_resources_total",
		Help: "Total number of resource changes",
	})
	resourcesAdded = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_resources_added",
		Help: "Resources to be created",
	})
	resourcesChanged = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_resources_changed",
		Help: "Resources to be updated",
	})
	resourcesDestroyed = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_resources_destroyed",
		Help: "Resources to be deleted",
	})
	executionDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_execution_duration_seconds",
		Help: "Duration of terraform operation",
	})
	executionSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_execution_success",
		Help: "1 if success, 0 if failed",
	})
	driftDetected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "terraform_drift_detected",
		Help: "1 if drift detected, 0 otherwise",
	})
)

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

func parseTerraformPlan(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var plan PlanJSON
	err = json.Unmarshal(data, &plan)
	if err != nil {
		return err
	}

	total, added, changed, destroyed := 0, 0, 0, 0
	drift := 0

	for _, rc := range plan.ResourceChanges {
		total++
		actions := rc.Change.Actions
		if contains(actions, "create") {
			added++
		}
		if contains(actions, "update") {
			changed++
		}
		if contains(actions, "delete") {
			destroyed++
		}
		if len(actions) == 1 && actions[0] == "update" {
			drift++
		}
	}

	resourcesTotal.Set(float64(total))
	resourcesAdded.Set(float64(added))
	resourcesChanged.Set(float64(changed))
	resourcesDestroyed.Set(float64(destroyed))
	driftDetected.Set(float64(min(1, drift)))

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	start := time.Now()

	logPath := os.Getenv("TERRAFORM_PLAN_PATH")
	pushIP := os.Getenv("PUSHGATEWAY_URL")
	job := os.Getenv("PUSHGATEWAY_JOB")
	instance := os.Getenv("GITHUB_RUN_ID")

	err := parseTerraformPlan(logPath)
	if err != nil {
		fmt.Println("Error parsing plan:", err)
		executionSuccess.Set(0)
	} else {
		executionSuccess.Set(1)
	}
	executionDuration.Set(time.Since(start).Seconds())
	pushURL := "http://" + pushIP + ":9091"
	err = push.New(pushURL, job).
		Grouping("instance", instance).
		Collector(resourcesTotal).
		Collector(resourcesAdded).
		Collector(resourcesChanged).
		Collector(resourcesDestroyed).
		Collector(driftDetected).
		Collector(executionDuration).
		Collector(executionSuccess).
		Push()
	if err != nil {
		fmt.Println("Push failed:", err)
		os.Exit(1)
		return
	}
	os.Exit(0)
}
