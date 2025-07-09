package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
)

func QueryGemini(runID string) error {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GOOGLE_API_KEY is not set")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}
	// fmt.Println(client.ClientConfig().APIKey)

	logs := map[string]string{
		"refresh": fmt.Sprintf("terraform-refresh-%s.log", runID),
		"plan":    fmt.Sprintf("terraform-plan-%s.log", runID),
		"apply":   fmt.Sprintf("terraform-apply-%s.log", runID),
	}

	var builder strings.Builder
	builder.WriteString("Here are three (two if apply is not present) logs from a Terraform execution:\n---\n")
	for label, name := range logs {
		path := filepath.Join("exporter", name)
		// Check if the file exists before attempting to read
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Printf("Warning: Log file %s not found. Skipping.\n", path)
			continue // Skip this file if it doesn't exist
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading log file %s: %w", path, err)
		}
		builder.WriteString(fmt.Sprintf("📄 %s Log:\n%s\n\n", strings.ToUpper(label), data))
	}
	builder.WriteString(`Generate a human-readable summary of:
1. What was changed (added, updated, deleted)?
2. Any errors or warnings?
3. Overall outcome (success, failed, partial)?
4. Highlight risky or unusual changes.
Keep it under 250 words.`)

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(builder.String()), nil)
	if err != nil {
		return fmt.Errorf("gemini generate content failed: %w", err)
	}

	text := resp.Text()

	if len(text) == 0 {
		return fmt.Errorf("no content returned from Gemini")
	}

	outputPath := filepath.Join("exporter", fmt.Sprintf("terraform-gemini-summary-%s.log", runID))
	if err := os.WriteFile(outputPath, []byte(text), 0644); err != nil {
		return fmt.Errorf("writing summary to file: %w", err)
	}

	fmt.Println("Gemini summary written to", outputPath)
	return nil
}
