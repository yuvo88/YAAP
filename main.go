package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	markdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/charmbracelet/glamour"
)

type Settings struct {
	Model      string
	OllamaUrl  string
	SearxNGUrl string
}

func getRequest(client *http.Client, link string) string {

	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return ""
	}
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
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	markdown, err := markdown.ConvertString(string(bodyBytes))
	if err != nil {
		return ""
	}

	return markdown
}

func searxSearch(client *http.Client, baseURL, q string, page_number int) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/search")
	if err != nil {
		return "", err
	}
	body :=
		fmt.Appendf(nil, "q=%s&categories=general&language=auto&time_range=&safesearch=0&theme=simple&pageno=%d", url.QueryEscape(q), page_number)

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}

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

	markdown, err := markdown.ConvertString(string(bodyBytes))
	if err != nil {
		return "", err
	}

	return markdown, nil
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

func writeToFile(prompt string) {
	file, err := os.OpenFile("prompts.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}

	defer file.Close()

	content := []byte(prompt)
	_, err = file.Write(content)
	if err != nil {
		return
	}

}

func callLLM(model_settings Settings, prompt string, system string) string {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.Model, system, prompt)
	writeToFile(prompt)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func researchMode(model_settings Settings, question string, history History) (string, string) {
	client := &http.Client{}

	queries_string := strings.Split(callLLM(
		model_settings,
		buildPrompt(question, "", history),
		`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google searches
		- You reply with between 1 and 3 short google queries separated by a newline character
		- Keep the queries short ( between 3 to 5 words )
		`), "\n")

	var queries strings.Builder

	for _, query := range queries_string {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		result, _ := searxSearch(client, model_settings.SearxNGUrl, query, 1)
		fmt.Fprintf(&queries, "%s", result)
	}

	links_string := callLLM(
		model_settings,
		buildPrompt(question, queries.String(), history),
		fmt.Sprintf(
			`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- Respond with 1-5 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, time.Now().Month().String(), time.Now().Year(),
		),
	)
	links := strings.Split(links_string, "\n")

	var sb strings.Builder

	for _, link := range links {
		trimmed := strings.TrimSpace(link)
		if trimmed == "" {
			continue
		}
		result := getRequest(client, trimmed)
		fmt.Fprintf(&sb, "# Original link: %s\n# Page:\n%s\n\n", trimmed, result)
	}

	return callLLM(
		model_settings,
		buildPrompt(question, sb.String(), history),
		`You answer quickly and accurately using the provided markdown web pages.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- Use the provided links and fetched pages as context
		- If you see information not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Please provide the exact answer for the user's question according to the markdown web pages provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided pages please say so explicitly.
		- You always respond in markdown
		`,
	), links_string
}
func lookupMode(model_settings Settings, question string, history History) string {
	client := &http.Client{}
	google_queries_string := callLLM(
		model_settings,
		buildPrompt(question, "", history),
		`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google searches
		- You reply with between 1 and 10 short google queries separated by a newline character
		- Keep the queries short ( between 3 to 5 words )
		`)

	lines := strings.Split(google_queries_string, "\n")

	var sb strings.Builder

	for _, line := range lines {
		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}

		result, err := searxSearch(client, model_settings.SearxNGUrl, query, 1)

		if err != nil {
			fmt.Println("search failed for query:", query, err)
			continue
		}
		fmt.Fprintf(&sb, "Original query: %s\n\nAnswer:%s\n\n", query, result)
	}

	return callLLM(
		model_settings,
		buildPrompt(question, sb.String(), history),
		`You answer quickly and accurately using the provided markdown web snippets.
		Rules:
		- Please **always provide a link** to the article that you got your information from.
		- Please **always cite your sources**
		- Use the provided original queries and responses as context
		- Please provide the exact answer for the user's question according to the markdown web snippets provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided markdown web snippets please say so explicitly.
		- You always respond in markdown
		`,
	)
}

type OperatingMode int

const (
	Auto OperatingMode = iota
	Research
	Simple
	Search
)

func getMode(question string) (OperatingMode, bool) {
	if question == "/research" {
		fmt.Println("Switched to research mode")
		return Research, true
	}
	if question == "/auto" {
		fmt.Println("Switched to auto mode")
		return Auto, true
	}
	if question == "/search" {
		fmt.Println("Switched to search mode")
		return Search, true
	}
	if question == "/simple" {
		fmt.Println("Switched to simple mode")
		return Simple, true
	}

	return -1, false
}

func buildPrompt(prompt string, context string, history History) string {
	return fmt.Sprintf(`
		[history]
		%s
		[context]
		%s
		[question]
		%s
	`, history.GetHistoryForModel(), context, prompt)
}

func main() {
	searx_url := flag.String(
		"searx-url",
		getenv("SEARXNG_URL", "http://localhost:8080"),
		"SearxNG server address",
	)
	model := flag.String(
		"model",
		getenv("MODEL", "qwen-32k"),
		"The name of the model that will run inference",
	)
	ollama_url := flag.String(
		"ollama-url",
		getenv("OLLAMA_URL", "http://localhost:11434"),
		"The link to the ollama server",
	)
	model_settings := Settings{
		Model:      *model,
		OllamaUrl:  *ollama_url,
		SearxNGUrl: *searx_url,
	}

	flag.Parse()
	mode := Auto
	remember := false
	history := History{}

	for {
		fmt.Print("> ")
		reader := bufio.NewReader(os.Stdin)

		prompt, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Couldn't scan input try again")
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			continue
		}
		if prompt == "/remember" {
			fmt.Println("Remembering!")
			remember = true
			continue
		}
		if prompt == "/forget" {
			fmt.Println("Forgetting!")
			remember = false
			history.Interactions = []ChatInteraction{}
			continue
		}
		new_mode, changed := getMode(prompt)

		if changed {
			mode = new_mode
			continue
		}

		start := time.Now()
		var final_answer string
		var sources string
		switch mode {
		case Auto:
			fmt.Println("Figuring out the best way to respond!")
			should_search := callLLM(
				model_settings,
				fmt.Sprintf("Question: %s", prompt),
				`You answer quickly and accurately.
				Rules:
					- Your job is decide whether a different agent needs to search online
					- If you feel like you're not sure opt for yes
					- If the request is to summarize information that you are clearly given in the question answer no
					- Please answer only in yes or no
			`)
			if should_search == "yes" {
				fmt.Println("Looking it up!")
				final_answer = lookupMode(model_settings, prompt, history)
			} else {
				fmt.Println("Answering from memory!")
				final_answer = callLLM(
					model_settings,
					buildPrompt(prompt, "", history),
					`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
				)

			}
		case Research:
			fmt.Println("Researching!")
			final_answer, sources = researchMode(model_settings, prompt, history)
		case Search:
			fmt.Println("Looking it up!")
			final_answer = lookupMode(model_settings, prompt, history)
		case Simple:
			fmt.Println("Answering from memory!")
			final_answer = callLLM(
				model_settings,
				buildPrompt(prompt, "", history),
				`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
			)
		}
		if remember {
			history.Interactions = append(history.Interactions, ChatInteraction{Question: prompt, Answer: final_answer})
		}

		r, _ := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(200),
		)
		out, err := r.Render(final_answer)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Println(final_answer)
		} else {
			fmt.Println(out)
		}
		fmt.Println(sources)

		fmt.Println("Took:", elapsed)

	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
