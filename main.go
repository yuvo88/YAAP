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
	"regexp"
	"strings"
	"time"

	"database/sql"

	markdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/glamour"
	"github.com/google/uuid"
	"github.com/invopop/jsonschema"
	_ "github.com/mattn/go-sqlite3"
)

const memories_db_name string = ".memories.db"
const memories_directory_name string = ".memories"

type Settings struct {
	HeavyModel string
	OllamaUrl  string
	SearxNGUrl string
	LightModel string
}

type State struct {
	Settings      Settings
	OperatingMode OperatingMode
	Memory        Memory
	Remember      bool
	Database      *sql.DB
	Renderer      *glamour.TermRenderer
}

func NewState(settings Settings, database *sql.DB, renderer *glamour.TermRenderer) *State {
	return &State{
		Settings:      settings,
		OperatingMode: Auto,
		Remember:      true,
		Database:      database,
		Renderer:      renderer,
	}
}

func getRequest(client *http.Client, link string) string {
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:147.0) Gecko/20100101 Firefox/147.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
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

	return escapeOutput(markdown)
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
	html := string(bodyBytes)
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	parsed, _ := doc.Find("#urls").First().Html()

	markdown, err := markdown.ConvertString(parsed)
	if err != nil {
		return "", err
	}

	return escapeOutput(markdown), nil
}

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

func ollamaGenerate(ctx context.Context, client *http.Client, baseURL, model, system string, prompt string, format *jsonschema.Schema) (*LLMResponse, error) {
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
func getDecisionFromLightLLM(model_settings Settings, prompt string, system string) *Decision {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	schema := jsonschema.Reflect(&Decision{})

	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, "gemma-128k", system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	decision := &Decision{}
	json.Unmarshal([]byte(answer.Response), decision)

	return decision
}
func getQueriesFromLightLLM(model_settings Settings, prompt string, system string) *QueriesList {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	schema := jsonschema.Reflect(&QueriesList{})

	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.LightModel, system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	queries := &QueriesList{}
	json.Unmarshal([]byte(answer.Response), queries)

	return queries
}
func getLinksFromLightLLM(model_settings Settings, prompt string, system string) *LinksList {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	schema := jsonschema.Reflect(&LinksList{})

	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.LightModel, system, prompt, schema)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}
	linksList := &LinksList{}
	json.Unmarshal([]byte(answer.Response), linksList)

	return linksList
}
func callLightLLM(model_settings Settings, prompt string, system string) *LLMResponse {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.LightModel, system, prompt, nil)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func callHeavyLLM(model_settings Settings, prompt string, system string) *LLMResponse {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.HeavyModel, system, prompt, nil)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func escapeOutput(input string) string {
	re := regexp.MustCompile(`[\s\n\t]+`)
	return re.ReplaceAllString(input, "|")
}
func getLinks(state *State, client *http.Client, question string) []string {
	month := time.Now().Month().String()
	year := time.Now().Year()

	queriesList := getQueriesFromLightLLM(
		state.Settings,
		buildPrompt(question, "", state.Memory),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google queries
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 3 short google queries separated by a newline character
		- Each query is a sentence built of multiple words
		- **NEVER** have a query with only one word
		- Keep the queries short ( between 3 to 5 words )
		`, month, year)).Queries

	var queries strings.Builder

	for _, query := range queriesList {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		result, _ := searxSearch(client, state.Settings.SearxNGUrl, query, 1)
		fmt.Fprintf(&queries, "%s", result)
	}

	links := getLinksFromLightLLM(
		state.Settings,
		buildPrompt(question, queries.String(), state.Memory),
		fmt.Sprintf(
			`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- Respond with 1-3 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, month, year,
		),
	)

	linkList := make(map[string]struct{})
	for _, link := range links.Links {
		trimmed := strings.TrimSpace(link)
		if trimmed == "" {
			continue
		}
		result := getLinksFromLightLLM(
			state.Settings,
			buildPrompt(question, getRequest(client, link), state.Memory),
			fmt.Sprintf(
				`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- Respond with 1-3 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, month, year,
			),
		)
		for _, l := range result.Links {
			linkList[l] = struct{}{}
		}
		// fmt.Fprintf(&sb, "# Original link: %s\n# Summarized Page:\n%s\n\n", trimmed, result)
	}
	items := make([]string, 0, len(linkList))
	for k := range linkList {
		items = append(items, k)
	}
	return items

}
func researchMode(state *State, question string) (string, []string) {
	client := &http.Client{}
	links := getLinks(state, client, question)

	var toParse strings.Builder
	for _, article := range links {
		result := callLightLLM(
			state.Settings,
			fmt.Sprintf(`
			[web page]
			%s
			[question]
			%s	
			[prompt]
			Please get relavant information for the question from the web page 
			`, getRequest(client, article), question),
			`You get relavant to a question from a web page
			Rules:
			- Always return a summary of only information relavant to the user question
			- Please make sure that you return the whole context for the question
			- **NEVER** respond with anything that is not code
			`,
		)
		// result := getRequest(client, item)
		fmt.Fprintf(&toParse, "%s", result.Response)
	}
	final_answer := callHeavyLLM(
		state.Settings,
		buildPrompt(question, toParse.String(), state.Memory),
		`You answer quickly and accurately using the provided markdown web pages.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- Use the provided links and fetched pages as context
		- If you don't understand the context of the user's question look for it in the history section
		- If you see information not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Respond with information closest to %s %d
		- Look for dates in provided pages and specify it in your response
		- Please provide the exact answer for the user's question according to the markdown web pages provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided pages please say so explicitly.
		- You always respond in markdown
		`,
	)

	fmt.Printf("Token count: %d\n", final_answer.PromptEvalCount)

	return final_answer.Response, links
}

func codeMode(state *State, question string) (string, []string) {
	client := &http.Client{}

	links := getLinks(state, client, question)

	var toParse strings.Builder
	for _, link := range links {
		result := callLightLLM(
			state.Settings,
			fmt.Sprintf(`
			[web page]
			%s
			[question]
			%s	
			[prompt]
			Please get relavant code example from this web page
			`, getRequest(client, link), question),
			`You get code examples from web pages
			Rules:
			- Always only return code examples
			- Return code examples relavant to the question
			- **NEVER** respond with anything that is not code
			`,
		)
		// result := getRequest(client, item)
		fmt.Fprintf(&toParse, "%s", result.Response)
	}
	final_answer := callHeavyLLM(
		state.Settings,
		buildPrompt(question, toParse.String(), state.Memory),
		`You answer quickly and accurately using the provided code examples.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- If you don't understand the context of the user's question look for it in the history section
		- Use the provided code examples as context for your answer
		- If you see information or code not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Respond with information closest to %s %d
		- Look for dates in provided pages and specify it in your response
		- Please provide the exact example for the user's question according to the code examples provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided examples please say so explicitly.
		- You always respond in markdown
		`,
	)

	fmt.Printf("Token count: %d\n", final_answer.PromptEvalCount)

	return final_answer.Response, links
}
func lightCodeMode(state *State, question string) (string, []string) {
	client := &http.Client{}
	links := getLinks(state, client, question)
	var toParse strings.Builder
	for _, link := range links {
		result := getRequest(client, link)
		fmt.Fprintf(&toParse, "%s", result)
	}
	final_answer := callLightLLM(
		state.Settings,
		fmt.Sprintf(`
		[web pages]
		%s
		[history]
		%s
		[question]
		%s	
		[prompt]
		Please get relavant code example from these web pages
		`, toParse.String(), state.Memory.GetMemoryForModel(), question),
		`You get code examples from web pages
		Rules:
		- Always only return code examples
		- Return code examples relavant to the question
		- **NEVER** respond with anything that is not code
		`,
	)

	fmt.Printf("Token count: %d\n", final_answer.PromptEvalCount)

	return final_answer.Response, links
}
func lookupMode(state *State, question string) string {
	month := time.Now().Month().String()
	year := time.Now().Year()
	client := &http.Client{}
	lines := getQueriesFromLightLLM(
		state.Settings,
		buildPrompt(question, "", state.Memory),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google queries
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 3 short google queries
		- Keep the queries short ( between 3 to 5 words )
		`, month, year)).Queries

	var sb strings.Builder

	for _, line := range lines {
		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}

		result, err := searxSearch(client, state.Settings.SearxNGUrl, query, 1)

		if err != nil {
			fmt.Println("search failed for query:", query, err)
			continue
		}
		fmt.Fprintf(&sb, "Original query: %s\n\nAnswer:%s\n\n", query, result)
	}

	final_answer := callHeavyLLM(
		state.Settings,
		buildPrompt(question, sb.String(), state.Memory),
		fmt.Sprintf(`You answer quickly and accurately using the provided markdown web snippets.
		Rules:
		- Please **always provide a link** to the article that you got your information from.
		- Please **always cite your sources**
		- Respond with information closest to %s %d
		- Use the provided original queries and responses as context
		- Please provide the exact answer for the user's question according to the markdown web snippets provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided markdown web snippets please say so explicitly.
		- You always respond in markdown
		`, month, year),
	)
	fmt.Printf("Token Count: %d", final_answer.PromptEvalCount)
	return final_answer.Response
}

type OperatingMode int

const (
	Auto OperatingMode = iota
	Research
	Normal
	Search
	Code
	FastCode
)

func memoryHandler(state *State, command string) {
	if command == "l" {
		fmt.Println("Listing memories")
		listMemories(state.Database)
	}

	if command[:1] == "u" {
		fmt.Println("Using memory")
		memory_id := strings.TrimSpace(command[1:])
		loadMemory(state, memory_id)
	}

	if command == "n" {
		fmt.Println("Creating a new memory")
		saveMemory(state)
		forgetMemory(state)
		rememberMemory(state)

	}

	if command == "r" {
		fmt.Println("Resuming last memory")
		resumeLastMemory(state)
	}

	if command == "nf" {
		fmt.Println("Creating a new memory and forgetting")
		forgetMemory(state)
		rememberMemory(state)
	}

	if command[:1] == "d" {
		fmt.Println("Deleting memory")
		memory_id := strings.TrimSpace(command[1:])
		deleteMemory(state, memory_id)

	}

	if command == "h" {
		fmt.Println("Memory handler help")
		fmt.Println("This way you can handle your program's memories")
		fmt.Println("Usage:")
		fmt.Println("  /memory <Flag> <Flag Value>")
		fmt.Println("Flags:")
		fmt.Println("  l - list memories")
		fmt.Println("  u - use memory - value UUID")
		fmt.Println("  n - create a new memory and save the old one")
		fmt.Println("  nf - create a new memory and don't save the old one")
	}
}
func modeHandler(state *State, command string) {
	if command == "r" {
		fmt.Println("Switched to research mode")
		state.OperatingMode = Research
	}
	if command == "a" {
		fmt.Println("Switched to auto mode")
		state.OperatingMode = Auto
	}
	if command == "s" {
		fmt.Println("Switched to search mode")
		state.OperatingMode = Search
	}
	if command == "n" {
		fmt.Println("Switched to normal mode")
		state.OperatingMode = Normal
	}
	if command == "c" {
		fmt.Println("Switched to code mode")
		state.OperatingMode = Code
	}
	if command == "fc" {
		fmt.Println("Switched to fast code mode")
		state.OperatingMode = FastCode
	}
	if command == "h" {
		fmt.Println("Mode handler help")
		fmt.Println("This is the way you can decide what modes your llm works in")
		fmt.Println("Usage:")
		fmt.Println("  /mode <Flag>")
		fmt.Println("flags:")
		fmt.Println("  r - research mode (use if you want to have the model deep dive)")
		fmt.Println("  a - auto mode (use if you're not sure what to choose)")
		fmt.Println("  s - search mode (use if you want the model to quickly search the web for current information)")
		fmt.Println("  n - normal mode (use if you want the model to reply by itself)")
		fmt.Println("  c - code mode (Use for accurate code examples with explanations)")
		fmt.Println("  fc - fast code mode (Use for quick code prototyping)")
	}
}

func commandHandler(state *State, command string) {
	parsed_command := strings.Split(command, " ")
	command_name := parsed_command[0]
	switch command_name {
	case "exit":
		saveMemory(state)
		os.Exit(0)
	case "current":
		fmt.Println(state.Memory.Title)
	case "mode":
		modeHandler(state, strings.Join(parsed_command[1:], " "))
	case "memory":
		memoryHandler(state, strings.Join(parsed_command[1:], " "))
	case "help":
		fmt.Println("YAAP - Yet Another Ai Program")
		fmt.Println("commands:")
		fmt.Println("  /mode: change the execution mode (/mode h) for help")
		fmt.Println("  /memory: memory commands (/memory h) for help")
		fmt.Println("  /current: look at the name of the current loaded memory")
		fmt.Println("  /exit: exit the program")
	default:
		fmt.Println("Couldn't find command")
	}

}

func buildPrompt(prompt string, context string, memory Memory) string {
	return fmt.Sprintf(`
		[context]
		%s
		[history]
		%s
		[question]
		%s
	`, context, memory.GetMemoryForModel(), prompt)
}

func main() {
	searx_url := flag.String(
		"searx-url",
		getenv("SEARXNG_URL", "http://localhost:8080"),
		"SearxNG server address",
	)
	heavyModel := flag.String(
		"heavy-model",
		getenv("HEAVY_MODEL", "qwen-40k"),
		"The name of the model that will run inference",
	)
	lightModel := flag.String(
		"light-model",
		getenv("LIGHT_MODEL", "gemma-128k"),
		"The name of the model that will run inference",
	)
	ollama_url := flag.String(
		"ollama-url",
		getenv("OLLAMA_URL", "http://localhost:11434"),
		"The link to the ollama server",
	)
	list_memories := flag.Bool(
		"list-memories",
		false,
		"Should list memories",
	)
	resume := flag.Bool(
		"resume",
		false,
		"Should resume last session",
	)
	load_memory := flag.String(
		"load-memory",
		"",
		"Memory id of memory to load from the list of memories. (--list-memories to see memories)",
	)
	fmt.Println(*lightModel)
	settings := Settings{
		HeavyModel: *heavyModel,
		LightModel: *lightModel,
		OllamaUrl:  *ollama_url,
		SearxNGUrl: *searx_url,
	}
	db := initDb()
	defer db.Close()
	flag.Parse()
	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(-1),
	)
	if *list_memories {
		listMemories(db)
		return
	}
	state := NewState(settings, db, r)
	if *load_memory != "" {
		loadMemory(state, *load_memory)
	}

	if *resume {
		resumeLastMemory(state)
	}

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
		if prompt[0] == '/' {
			commandHandler(state, prompt[1:])
			continue
		}

		start := time.Now()
		var final_answer string
		var sources []string
		switch state.OperatingMode {
		case Auto:
			fmt.Println("Figuring out the best way to respond!")
			should_search := callHeavyLLM(
				settings,
				fmt.Sprintf("Question: %s", prompt),
				`You answer quickly and accurately.
				Rules:
					- Your job is decide whether a different agent needs to search online
					- If you feel like you're not sure opt for yes
					- If the request is to summarize information that you are clearly given in the question answer no
					- Please answer only in yes or no
			`).Response
			if should_search == "yes" {
				fmt.Println("Looking it up!")
				final_answer = lookupMode(state, prompt)
			} else {
				fmt.Println("Answering from memory!")
				final_answer = callHeavyLLM(
					settings,
					buildPrompt(prompt, "", state.Memory),
					`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
				).Response

			}
		case Research:
			fmt.Println("Researching!")
			final_answer, sources = researchMode(state, prompt)
		case Search:
			fmt.Println("Looking it up!")
			final_answer = lookupMode(state, prompt)
		case Normal:
			fmt.Println("Answering from memory!")
			answer := callHeavyLLM(
				settings,
				buildPrompt(prompt, "", state.Memory),
				`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
			)
			fmt.Printf("Token Count: %d", answer.PromptEvalCount)
			final_answer = answer.Response
		case Code:
			fmt.Println("Coding!")
			final_answer, sources = codeMode(state, prompt)
		case FastCode:
			fmt.Println("Fast Coding!")
			final_answer, sources = lightCodeMode(state, prompt)

		}
		if state.Remember {
			if len(state.Memory.Interactions) == 0 {
				state.Memory.Title = prompt
				state.Memory.Id = uuid.New().String()
			}
			state.Memory.Interactions = append(state.Memory.Interactions, ChatInteraction{Question: prompt, Answer: final_answer, Links: sources})
		}

		out, err := r.Render(final_answer)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Println(final_answer)
		} else {
			fmt.Println(out)
		}
		fmt.Println(strings.Join(sources, "\n"))

		fmt.Println("Took:", elapsed)

	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

//TODO: Add links to the output of the load page
//TODO: jump to top of response on response
//TODO: Pretty print the timestamps on list
//TODO: add elapsed time counter
//TODO: Add logs
//TODO: enable memory exporting
//TODO: auto-complete for inline commands
//TODO: enable multiline prompts
//TODO: Make workspaces for different conversations
//TODO: figure out how to make auto mode decide whether to research
