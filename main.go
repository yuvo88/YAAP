package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"database/sql"

	markdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/glamour"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const memories_db_name string = ".memories.db"
const memories_directory_name string = ".memories"

type Settings struct {
	Model      string
	OllamaUrl  string
	SearxNGUrl string
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

func callLLM(model_settings Settings, prompt string, system string) string {
	client := &http.Client{}
	llmCtx, cancelLLM := context.WithTimeout(context.Background(), 3600*time.Second)
	defer cancelLLM()
	answer, err := ollamaGenerate(llmCtx, client, model_settings.OllamaUrl, model_settings.Model, system, prompt)
	if err != nil {
		fmt.Println("LLM failed:", err)
		os.Exit(1)
	}

	return answer
}
func researchMode(state *State, question string) (string, string) {
	month := time.Now().Month().String()
	year := time.Now().Year()
	client := &http.Client{}

	queries_string := strings.Split(callLLM(
		state.Settings,
		buildPrompt(question, "", state.Memory),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google searches
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 3 short google queries separated by a newline character
		- Keep the queries short ( between 3 to 5 words )
		`, month, year)), "\n")

	var queries strings.Builder

	for _, query := range queries_string {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		result, _ := searxSearch(client, state.Settings.SearxNGUrl, query, 1)
		fmt.Fprintf(&queries, "%s", result)
	}

	links_string := callLLM(
		state.Settings,
		buildPrompt(question, queries.String(), state.Memory),
		fmt.Sprintf(
			`You answer quickly and accurately using the provided markdown web snippets.
			Rules:
			- Use the provided markdown web snippets and only the provided markdown web snippets as context
			- Respond with 1-5 links that the most relavant to the users question and closest to %s %d
			- Please make sure that you cover all parts of the user's question with the links you provide
			- Only return links separated by newline characters nothing else
			`, month, year,
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
		state.Settings,
		buildPrompt(question, sb.String(), state.Memory),
		`You answer quickly and accurately using the provided markdown web pages.
		Rules:
		- Please **always provide a link** to the web page that you got your information from.
		- Please **always cite your sources**
		- Use the provided links and fetched pages as context
		- If you see information not relavant to the question in the page please disregard it
		- Only reply to the user's question
		- Respond with information closest to %s %d
		- Look for dates in provided pages and specify it in your response
		- Please provide the exact answer for the user's question according to the markdown web pages provided, not suggestions to how the user can figure out the answer by themselves.
		- If you don't find the answer in the provided pages please say so explicitly.
		- You always respond in markdown
		`,
	), links_string
}
func lookupMode(state *State, question string) string {
	month := time.Now().Month().String()
	year := time.Now().Year()
	client := &http.Client{}
	google_queries_string := callLLM(
		state.Settings,
		buildPrompt(question, "", state.Memory),
		fmt.Sprintf(`You answer quickly and accurately.
		Rules:
		- Your job is to turn a question into google searches
		- The current date is %s %d if the user asks about something happening now
		- You reply with between 1 and 10 short google queries separated by a newline character
		- Keep the queries short ( between 3 to 5 words )
		`, month, year))

	lines := strings.Split(google_queries_string, "\n")

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

	return callLLM(
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
}

type OperatingMode int

const (
	Auto OperatingMode = iota
	Research
	Normal
	Search
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
		fmt.Println("l - list memories")
		fmt.Println("u - use memory")
		fmt.Println("n - create a new memory and save the old one")
		fmt.Println("nf - create a new memory and don't save the old one")
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
	if command == "h" {
		fmt.Println("Mode handler help")
		fmt.Println("r - research mode (use if you want code examples or have the model deep dive)")
		fmt.Println("a - auto mode (use if you're not sure what to choose)")
		fmt.Println("s - search mode (use if you want the model to quickly search the web for current information)")
		fmt.Println("n - normal mode (use if you want the model to reply by itself)")
	}
}

func buildPrompt(prompt string, context string, memory Memory) string {
	return fmt.Sprintf(`
		[history]
		%s
		[context]
		%s
		[question]
		%s
	`, memory.GetMemoryForModel(), context, prompt)
}
func saveMemory(state *State) {
	dir := memories_directory_name
	id := uuid.New().String()

	file_path := filepath.Join(dir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic("")
	}

	file, err := os.Create(file_path)
	if err != nil {
		panic("")
	}

	defer file.Close()

	encoder := gob.NewEncoder(file)

	if err := encoder.Encode(state.Memory); err != nil {
		panic("")
	}
	_, err = state.Database.Exec("INSERT INTO memories (id, title, updated) VALUES (?, ?, ?)", id, state.Memory.Title, time.Now().Unix())
	if err != nil {
		panic("")
	}
}
func deleteMemory(state *State, memory_id string) {
	file_path := filepath.Join(memories_directory_name, memory_id)
	_, err := state.Database.Exec("DELETE FROM memories WHERE id=?", memory_id)
	if err != nil {
		panic("Couldn't delete memory from database")
	}
	err = os.Remove(file_path)
	if err != nil {
		panic("Couldn't remove memory from disk")
	}
}
func forgetMemory(state *State) {
	state.Remember = false
	state.Memory.Interactions = []ChatInteraction{}
	state.Memory.Title = ""
}
func rememberMemory(state *State) {
	state.Remember = true
}

type MemoryDto struct {
	Id      string
	Title   string
	Updated string
}

func listMemories(database *sql.DB) {
	rows, err := database.Query("SELECT * FROM memories ORDER BY updated")

	if err != nil {
		panic("")
	}
	defer rows.Close()
	var memories []MemoryDto
	for rows.Next() {
		var memory MemoryDto
		if err := rows.Scan(&memory.Id, &memory.Title, &memory.Updated); err != nil {
			panic("")
		}

		memories = append(memories, memory)

	}

	if err := rows.Err(); err != nil {
		panic("")
	}
	for _, memory := range memories {
		fmt.Printf("%s | %s | %s\n", memory.Id, memory.Title, memory.Updated)
	}

}

func loadMemory(state *State, memory_id string) {

	file_path := filepath.Join(memories_directory_name, memory_id)
	file, _ := os.Open(file_path)
	defer file.Close()

	decoder := gob.NewDecoder(file)

	var memory Memory

	err := decoder.Decode(&memory)
	if err != nil {
		fmt.Println("Memory not found")
	}
	state.Memory = memory
	state.Memory.PrintMemory(state.Renderer)
}

func initDb() *sql.DB {
	db, err := sql.Open("sqlite3", memories_db_name)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			updated INTEGER NOT NULL
		)`,
	)

	if err != nil {

		panic(err)

	}

	return db
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
	list_memories := flag.Bool(
		"list-memories",
		false,
		"Should list memories",
	)
	load_memory := flag.String(
		"load-memory",
		"",
		"Memory id of memory to load from the list of memories. (--list-memories to see memories)",
	)
	settings := Settings{
		Model:      *model,
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
		if prompt == "/exit" {
			saveMemory(state)
			break
		}
		if prompt == "/current" {
			fmt.Println(state.Memory.Title)
			continue
		}
		if len(prompt) > 5 && prompt[:5] == "/mode" {
			command := strings.TrimSpace(prompt[5:])
			modeHandler(state, command)
			continue
		}
		if len(prompt) > 7 && prompt[:7] == "/memory" {
			command := strings.TrimSpace(prompt[7:])
			memoryHandler(state, command)
			continue
		}

		start := time.Now()
		var final_answer string
		var sources string
		switch state.OperatingMode {
		case Auto:
			fmt.Println("Figuring out the best way to respond!")
			should_search := callLLM(
				settings,
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
				final_answer = lookupMode(state, prompt)
			} else {
				fmt.Println("Answering from memory!")
				final_answer = callLLM(
					settings,
					buildPrompt(prompt, "", state.Memory),
					`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
				)

			}
		case Research:
			fmt.Println("Researching!")
			final_answer, sources = researchMode(state, prompt)
		case Search:
			fmt.Println("Looking it up!")
			final_answer = lookupMode(state, prompt)
		case Normal:
			fmt.Println("Answering from memory!")
			final_answer = callLLM(
				settings,
				buildPrompt(prompt, "", state.Memory),
				`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
			)
		}
		if state.Remember {
			if len(state.Memory.Interactions) == 0 {
				state.Memory.Title = prompt
			}
			state.Memory.Interactions = append(state.Memory.Interactions, ChatInteraction{Question: prompt, Answer: final_answer})
		}

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

//TODO: if a memory is loaded save the new version and update the updated time
//TODO: figure out how to make auto mode decide whether to research
//TODO: help for inline commands
//TODO: dedicated handler for memories
//TODO: auto-complete for inline commands
//TODO: enable multiline prompts
//TODO: Make workspaces for different conversations
