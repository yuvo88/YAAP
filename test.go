package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/glamour"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func searxSearch(ctx context.Context, client *http.Client, baseURL, q string, page_number int) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/search")
	if err != nil {
		return "", err
	}
	body :=
		fmt.Appendf(nil, "q=%s&categories=general&language=auto&time_range=&safesearch=0&theme=simple&pageno=%d", url.QueryEscape(q), page_number)

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		panic(err)
	}

	// Headers copied from curl
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:147.0) Gecko/20100101 Firefox/147.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("searxng status %d: %s", resp.StatusCode, string(b))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}
func ollamaGenerate(ctx context.Context, client *http.Client, baseURL, model, system string, prompt string) (string, error) {
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"system": system,
		"stream": false,
		// You can tune for speed:
		"options": map[string]any{
			"temperature": 0.2,
		},
	}
	b, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(baseURL, "/")+"/api/generate",
		bytes.NewReader(b),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Response), nil
}

func callQwen(prompt string, system string) string {
	ollamaURL := getenv("OLLAMA_URL", "http://localhost:11434")
	model := "qwen-32k"
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()

	answer, err := ollamaGenerate(llmCtx, client, ollamaURL, model, system, prompt)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func callGpt(prompt string, system string) string {
	ollamaURL := getenv("OLLAMA_URL", "http://localhost:11434")
	model := "gpt-32k"
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()

	answer, err := ollamaGenerate(llmCtx, client, ollamaURL, model, system, prompt)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func main() {
	// Configure via env vars (easy to run)
	searxURL := getenv("SEARXNG_URL", "http://localhost:8080")

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go \"your question here\"")
		os.Exit(1)
	}
	question := strings.Join(os.Args[1:], " ")
	start := time.Now()
	// Shared HTTP client with sane timeouts
	client := &http.Client{}
	should_search := callQwen(
		fmt.Sprintf("Question: %s", question),
		`You answer quickly and accurately.
			Rules:
				- Your job is decide whether a different agent needs to search online
				- Please answer only in yes or no
				- If you feel like you're not sure opt for yes
	`)
	var final_answer string
	if should_search == "yes" {
		fmt.Println("Looking it up!")
		google_queries_string := callQwen(
			fmt.Sprintf("Question: %s", question),
			`You answer quickly and accurately.
			Rules:
				- Your job is to turn a question into google searches
				- You reply with between 1 and 10 short google queries separated by a newline character
				- Keep the queries short ( between 3 to 5 words )
		`)

		lines := strings.Split(google_queries_string, "\n")

		// Prepare results slice
		var sb strings.Builder

		for _, line := range lines {
			// Trim whitespace and skip empty lines
			query := strings.TrimSpace(line)
			if query == "" {
				continue
			}

			searchCtx, cancelSearch := context.WithTimeout(context.Background(), 20*time.Second)
			result, err := searxSearch(searchCtx, client, searxURL, query, 1)
			cancelSearch()

			if err != nil {
				// decide whether to continue or fail hard
				fmt.Println("search failed for query:", query, err)
				continue
			}
			sb.WriteString(fmt.Sprintf("Original query: %s\n\nAnswer:%s\n\n", query, result))
		}

		final_answer = callQwen(
			fmt.Sprintf("Question: %s\nQueries:\n%s", question, sb.String()),
			`You answer quickly and accurately using the provided web snippets.
				Rules:
				- Please **always provide a link** to the article that you got your information from.
				- Please **always cite your sources**
				- Use the provided original queries and responses as context
				- Please provide the exact answer for the user's question according to the HTML provided, not suggestions to how the user can figure out the answer by themselves.
				- If you don't find the answer in the provided HTML please say so explicitly.
				- You always respond in markdown
			`,
		)
	} else {
		fmt.Println("Answering from memory!")
		final_answer = callQwen(
			fmt.Sprintf("Question: %s", question),
			`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
		)

	}

	r, _ := glamour.NewTermRenderer(
		// detect background color and pick either the default dark or light theme
		glamour.WithAutoStyle(),
		// wrap output at specific width (default is 80)
		glamour.WithWordWrap(200),
	)
	out, err := r.Render(final_answer)
	if err != nil {
		panic("Something went wrong: err")
	}
	fmt.Print(out)
	elapsed := time.Since(start)
	fmt.Println("Took:", elapsed)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
