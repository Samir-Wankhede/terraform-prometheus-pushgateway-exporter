package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	Timestamp       string           `json:"timestamp"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

func parseLogStats(path string) (added, changed, destroyed, imported int) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Apply complete!") {
			// Terraform summary: Apply complete! Resources: 1 added, 0 changed, 0 destroyed.
			fields := strings.Split(line, ":")
			if len(fields) < 2 {
				continue
			}
			stats := strings.Split(fields[1], ",")
			for _, stat := range stats {
				parts := strings.Fields(strings.TrimSpace(stat))
				if len(parts) < 2 {
					continue
				}
				count, _ := strconv.Atoi(parts[0])
				switch parts[1] {
				case "added":
					added = count
				case "changed":
					changed = count
				case "destroyed":
					destroyed = count
				case "imported":
					imported = count
				}
			}
		}
	}
	return
}

func detectDrift(logPath string) float64 {
	data, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Println("Error reading refresh log:", err)
		return 0
	}
	logContent := string(data)

	if strings.Contains(logContent, "No changes. Your infrastructure still matches the configuration.") {
		return 0
	}
	// fallback: check for resource refresh logs (conservative)
	if strings.Contains(logContent, "Refreshing state...") {
		return 1
	}
	return 0
}

func collectMetrics() error {
	planPath := os.Getenv("TERRAFORM_PLAN_PATH")
	applyLogPath := os.Getenv("TERRAFORM_APPLY_LOG_PATH")
	refreshLogPath := os.Getenv("TERRAFORM_REFRESH_LOG_PATH")
	startTimeEnv := os.Getenv("TERRAFORM_START_TIME")

	job := os.Getenv("PUSHGATEWAY_JOB")
	instance := os.Getenv("GITHUB_RUN_ID")
	workflowName := os.Getenv("GITHUB_WORKFLOW")
	commitMsg := os.Getenv("COMMIT_MESSAGE")

	startUnix, _ := strconv.ParseInt(startTimeEnv, 10, 64)
	execDuration := time.Since(time.Unix(startUnix, 0)).Seconds()
	timestamp := float64(time.Now().Unix())
	drift := detectDrift(refreshLogPath)

	// Metrics
	metrics := map[string]prometheus.Gauge{}

	makeGauge := func(name, help string, value float64) {
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
		g.Set(value)
		metrics[name] = g
	}

	// Plan-only data
	var plan PlanJSON
	planFile, err := os.ReadFile(planPath)
	if err == nil {
		json.Unmarshal(planFile, &plan)
	}

	// Tally resource changes
	total, toAdd, toChange, toDestroy, toImport := 0, 0, 0, 0, 0
	for _, rc := range plan.ResourceChanges {
		total++
		actions := rc.Change.Actions
		if contains(actions, "create") {
			toAdd++
		}
		if contains(actions, "update") {
			toChange++
		}
		if contains(actions, "delete") {
			toDestroy++
		}
		if contains(actions, "import") {
			toImport++
		}
	}

	if plan.Timestamp != "" {
		parsedTime, err := time.Parse(time.RFC3339, plan.Timestamp)
		if err == nil {
			timestamp = float64(parsedTime.Unix())
		}
	}

	// Export common metrics
	makeGauge("terraform_execution_duration_seconds", "Time taken for execution", execDuration)
	makeGauge("terraform_timestamp", "Unix timestamp of run", timestamp)
	makeGauge("terraform_drift_detected", "Drift found during refresh", float64(drift))
	makeGauge("terraform_resources_total", "Total planned resource changes", float64(total))
	makeGauge("terraform_to_add", "Resources planned to be added", float64(toAdd))
	makeGauge("terraform_to_change", "Resources planned to be changed", float64(toChange))
	makeGauge("terraform_to_destroy", "Resources planned to be destroyed", float64(toDestroy))
	makeGauge("terraform_to_import", "Resources planned to be imported", float64(toImport))

	if applyLogPath != "" {
		// Apply context
		added, changed, destroyed, imported := parseLogStats(applyLogPath)
		makeGauge("terraform_added", "Resources actually added", float64(added))
		makeGauge("terraform_changed", "Resources actually changed", float64(changed))
		makeGauge("terraform_destroyed", "Resources actually destroyed", float64(destroyed))
		makeGauge("terraform_imported", "Resources actually imported", float64(imported))
	}

	resultLogPath := planPath
	if applyLogPath != "" {
		resultLogPath = applyLogPath
	}
	if isTerraformRunSuccessful(resultLogPath) {
		makeGauge("terraform_result", "1=success, 0=failure", 1)
	} else {
		makeGauge("terraform_result", "1=success, 0=failure", 0)
	}

	// Push
	pushURL := "http://" + os.Getenv("PUSHGATEWAY_URL") + ":9091"
	pusher := push.New(pushURL, job).
		Grouping("instance", instance).
		Grouping("commit_message", commitMsg).
		Grouping("workflow_name", workflowName).
		Grouping("job", job)

	for _, g := range metrics {
		pusher.Collector(g)
	}
	return pusher.Push()
}

func contains(slice []string, val string) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func isTerraformRunSuccessful(logPath string) bool {
	file, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Error:") || strings.Contains(line, "│ Error") {
			return false
		}
	}
	return true
}

func main() {
	if err := collectMetrics(); err != nil {
		fmt.Println("Error pushing metrics:", err)
		os.Exit(1)
	}
}
