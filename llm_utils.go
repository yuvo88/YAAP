package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
)

type LLMResponse struct {
	Response        string `json:"response"`
	PromptEvalCount int    `json:"prompt_eval_count"`
}
type LinksList struct {
	Links []string `json:"links"`
}
type QueriesList struct {
	Queries []string `json:"queries"`
}

type Decision struct {
	Decision bool `json:"decision"`
}

func ollamaGenerate(client *http.Client, baseURL, model, system string, prompt string, format *jsonschema.Schema) (*LLMResponse, error) {
	ctx, cancelLLM := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelLLM()
	out := &LLMResponse{}
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"system": system,
		"stream": false,
		// You can tune for speed:
		"options": map[string]any{
			"temperature": 0.2,
		},
		"format": format,
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(baseURL, "/")+"/api/generate",
		bytes.NewReader(b),
	)
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return out, err
	}
	return out, nil
}

func getDecisionFromLightLLM(modelSettings Settings, prompt string, system string) *Decision {
	client := &http.Client{}
	schema := jsonschema.Reflect(&Decision{})

	answer, err := ollamaGenerate(client, modelSettings.OllamaUrl, "gemma-128k", system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	decision := &Decision{}
	json.Unmarshal([]byte(answer.Response), decision)

	return decision
}
func getQueriesFromLightLLM(modelSettings Settings, prompt string, system string) *QueriesList {
	client := &http.Client{}
	schema := jsonschema.Reflect(&QueriesList{})

	answer, err := ollamaGenerate(client, modelSettings.OllamaUrl, modelSettings.LightModel, system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	queries := &QueriesList{}
	json.Unmarshal([]byte(answer.Response), queries)

	return queries
}
func getLinksFromLightLLM(modelSettings Settings, prompt string, system string) *LinksList {
	client := &http.Client{}
	schema := jsonschema.Reflect(&LinksList{})

	answer, err := ollamaGenerate(client, modelSettings.OllamaUrl, modelSettings.LightModel, system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	linksList := &LinksList{}
	json.Unmarshal([]byte(answer.Response), linksList)

	return linksList
}
func callLightLLM(modelSettings Settings, prompt string, system string) *LLMResponse {
	client := &http.Client{}
	answer, err := ollamaGenerate(client, modelSettings.OllamaUrl, modelSettings.LightModel, system, prompt, nil)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func callHeavyLLM(modelSettings Settings, prompt string, system string) *LLMResponse {
	client := &http.Client{}
	answer, err := ollamaGenerate(client, modelSettings.OllamaUrl, modelSettings.HeavyModel, system, prompt, nil)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
