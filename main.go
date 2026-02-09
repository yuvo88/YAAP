package main

import (
	"bufio"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed templates/*
var templates embed.FS

type Settings struct {
	HeavyModel string
	OllamaUrl  string
	SearxNGUrl string
	LightModel string
}

type OperatingMode int

const (
	Research OperatingMode = iota
	Normal
	Search
	Code
	FastCode
)

func memoryHandler(state *State, command string) string {
	if command == "l" {
		return listMemories(state.Database)
	}

	if command[:1] == "u" {
		memoryId := strings.TrimSpace(command[1:])
		return loadMemory(state, memoryId)
	}

	if command == "n" {
		saveMemory(state)
		forgetMemory(state)
		rememberMemory(state)
		return "Creating a new memory"

	}

	if command == "r" {
		saveMemory(state)
		return resumeLastMemory(state)
	}
	if command == "rf" {
		resumeLastMemory(state)
		return "Resuming last memory and not remembering this session"
	}

	if command == "nf" {
		forgetMemory(state)
		rememberMemory(state)
		return "Creating a new memory and not remembering this session"
	}

	if command[:1] == "d" {
		memoryId := strings.TrimSpace(command[1:])
		deleteMemory(state, memoryId)
		return "Deleting memory"

	}

	if command == "h" {
		return `Memory handler help

		This way you can handle your program's memories
		Usage:
		  /memory <Flag> <Flag Value>
		Flags:
		  l - list memories
		  u <Memory Id> - Load a specific memory
		  r - Resume last memory
		  rf - Rusume last memory and don't save the old one
		  n - create a new memory and save the old one
		  nf - create a new memory and don't save the old one
		`
	}
	return ""
}
func modeHandler(state *State, command string) string {
	if command == "r" {
		state.OperatingMode = Research
		return "Switched to research mode"
	}
	if command == "s" {
		state.OperatingMode = Search
		return "Switched to search mode"
	}
	if command == "n" {
		state.OperatingMode = Normal
		return "Switched to normal mode"
	}
	if command == "c" {
		state.OperatingMode = Code
		return "Switched to code mode"
	}
	if command == "fc" {
		state.OperatingMode = FastCode
		return "Switched to fast code mode"
	}
	if command == "h" {
		return `Mode handler help

		This is the way you can decide what modes your llm works in
		Usage:
		  /mode <Flag>
		flags:
		  r - research mode (use if you want to have the model deep dive)
		  s - search mode (use if you want the model to quickly search the web for current information)
		  n - normal mode (use if you want the model to reply by itself)
		  c - code mode (Use for accurate code examples with explanations)
		  fc - fast code mode (Use for quick code prototyping)
		`
	}
	return ""
}
func fileHandler(state *State, command string) string {
	if command[0] == 'o' {
		fileName := strings.TrimSpace(command[1:])
		state.FileName = fileName
		return fmt.Sprintf("Reading file %s\n", fileName)
	}
	if command == "d" {
		state.FileName = ""
		return fmt.Sprintln("Discarding file")
	}
	if command == "c" {
		return fmt.Sprintln(state.FileName)
	}
	if command == "h" {
		return `File handler help

		This is the way to give your agent file context
		Usage:
		  /file <Flag>
		flags:
		  o <File Name> - File to have the LLM answer by
		  d - Discard file you're using
		  c - Print current file you're working on
		`
	}
	return ""
}
func commandHandler(state *State, command string) string {
	parsedCommand := strings.Split(command, " ")
	commandName := parsedCommand[0]
	switch commandName {
	case "current":
		return state.Memory.Title
	case "mode":
		return modeHandler(state, strings.Join(parsedCommand[1:], " "))
	case "memory":
		return memoryHandler(state, strings.Join(parsedCommand[1:], " "))
	case "file":
		return fileHandler(state, strings.Join(parsedCommand[1:], " "))
	case "help":
		return `YAAP - Yet Another Ai Program

		commands:
		  /mode: change the execution mode (/mode h) for help
		  /memory: memory commands (/memory h) for help
		  /file: file commands (/file h) for help
		  /current: look at the name of the current loaded memory
		  /exit: exit the program
		`
	default:
		return "Couldn't find command"
	}
}

func executePrompt(state *State, prompt string) FinalAnswer {
	var finalAnswer string
	var sources []string
	switch state.OperatingMode {
	case Research:
		finalAnswer, sources = researchMode(state, prompt)
	case Search:
		finalAnswer = lookupMode(state, prompt)
	case Normal:
		answer := callHeavyLLM(
			state,
			buildPrompt(state, prompt, ""),
			`You answer quickly and accurately using your own abilities.
				Rules:
				- If you don't know the answer always say you don't know
				- You always respond in markdown
			`,
		)
		finalAnswer = answer.Response
	case Code:
		finalAnswer, sources = codeMode(state, prompt)
	case FastCode:
		finalAnswer, sources = lightCodeMode(state, prompt)

	}
	return FinalAnswer{finalAnswer, sources}
}

type FinalAnswer struct {
	FinalAnswer string
	Sources     []string
}

func elapsedTime(resultChan chan FinalAnswer, ticker *time.Ticker, start time.Time) FinalAnswer {
	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(start)
			if elapsed.Seconds() > 0.3 {
				fmt.Printf("\x1b[?2K")
				fmt.Printf("\r")
				fmt.Printf("Elapsed: %s", elapsed.Round(time.Second))

			}

		case answer := <-resultChan:
			return answer
		}
	}
}

func getPrompt(state *State) string {
	fmt.Print(">>>> ")
	reader := bufio.NewReader(os.Stdin)
	terminator := "!@#"

	prompt, err := reader.ReadString('\n')
	if err != nil {
		state.Logger.Error("Couldn't read user input", slog.Any("err", err))
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == terminator {
		var multiLinePrompt strings.Builder
		for {
			prompt, err = reader.ReadString('\n')
			prompt = strings.TrimSpace(prompt)
			if err != nil {
				state.Logger.Error("Couldn't read user input", slog.Any("err", err))
				break
			}
			if prompt == terminator {
				break
			}

			fmt.Fprintf(&multiLinePrompt, "%s\n", prompt)
		}
		return multiLinePrompt.String()

	}

	return prompt

}

func respondToPrompt(state *State, prompt string) string {
	extensions := parser.CommonExtensions
	renderer := html.NewRenderer(html.RendererOptions{})
	if prompt[0] == '/' {
		return string(adaptForPrettify(markdown.ToHTML([]byte(commandHandler(state, prompt[1:])), parser.NewWithExtensions(extensions), renderer)))
	}

	answer := executePrompt(state, prompt)
	if state.Remember {
		if len(state.Memory.Interactions) == 0 {
			state.Memory.Title = prompt
			state.Memory.Id = uuid.New().String()
		}
		state.Memory.Interactions = append(state.Memory.Interactions, ChatInteraction{Question: prompt, Answer: answer.FinalAnswer, Links: answer.Sources})
	}

	html := adaptForPrettify(markdown.ToHTML([]byte(answer.FinalAnswer), parser.NewWithExtensions(extensions), renderer))
	return string(html)
}

func cliHandler(state *State) {
	resultChan := make(chan FinalAnswer)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		prompt := getPrompt(state)
		if prompt == "" {
			continue
		}

		if prompt[0] == '/' {
			if prompt[1:] == "exit" {
				saveMemory(state)
				os.Exit(0)
			}
			fmt.Println(commandHandler(state, prompt[1:]))
			continue
		}

		switch state.OperatingMode {
		case Research:
			fmt.Println("Researching!")
		case Search:
			fmt.Println("Looking it up!")
		case Normal:
			fmt.Println("Answering from memory!")
		case Code:
			fmt.Println("Coding!")
		case FastCode:
			fmt.Println("Fast Coding!")
		}
		start := time.Now()
		go func() {
			resultChan <- executePrompt(state, prompt)
		}()
		answer := elapsedTime(resultChan, ticker, start)
		if state.Remember {
			if len(state.Memory.Interactions) == 0 {
				state.Memory.Title = prompt
				state.Memory.Id = uuid.New().String()
			}
			state.Memory.Interactions = append(state.Memory.Interactions, ChatInteraction{Question: prompt, Answer: answer.FinalAnswer, Links: answer.Sources})
		}

		out, err := state.Renderer.Render(answer.FinalAnswer)
		if err != nil {
			fmt.Println(answer.FinalAnswer)
		} else {
			fmt.Println(out)
		}
		fmt.Println(strings.Join(answer.Sources, "\n"))
	}
}

var codeLangRe = regexp.MustCompile(
	`<pre><code class="language-([^"]+)">`,
)

func adaptForPrettify(html []byte) []byte {
	return codeLangRe.ReplaceAll(
		html,
		[]byte(`<pre class="prettyprint lang-$1"><code>`),
	)
}
func webHandler(state *State) {
	r := gin.Default()
	extensions := parser.CommonExtensions
	renderer := html.NewRenderer(html.RendererOptions{})

	tmpl := template.Must(
		template.ParseFS(templates, "templates/*.html"),
	)
	r.SetHTMLTemplate(tmpl)

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "home.html", gin.H{
			"answer": "Ask me anything",
		})
	})
	r.GET("/get-chat-history", func(c *gin.Context) {
		c.String(http.StatusOK, "%s", template.HTML(string(adaptForPrettify(markdown.ToHTML([]byte(state.Memory.GetPrintedMemory(state.Renderer)), parser.NewWithExtensions(extensions), renderer)))))
	})
	r.POST("/", func(c *gin.Context) {
		html := respondToPrompt(state, c.PostForm("value"))

		c.HTML(http.StatusOK, "home.html", gin.H{
			"answer": template.HTML(html),
		})
	})

	r.Run("0.0.0.0:12345")

}

func main() {
	searxUrl := flag.String(
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
	ollamaUrl := flag.String(
		"ollama-url",
		getenv("OLLAMA_URL", "http://localhost:11434"),
		"The link to the ollama server",
	)
	shouldListMemories := flag.Bool(
		"list-memories",
		false,
		"Should list memories",
	)
	memoryToDelete := flag.String(
		"delete-memory",
		"",
		"Should list memories",
	)
	resume := flag.Bool(
		"resume",
		false,
		"Should resume last session",
	)
	memoryToLoad := flag.String(
		"load-memory",
		"",
		"Memory id of memory to load from the list of memories. (--list-memories to see memories)",
	)
	webServer := flag.Bool(
		"web-server",
		false,
		"EXPERIMENTAL!! Start webserver",
	)
	db := initDb()
	defer db.Close()
	flag.Parse()
	settings := Settings{
		HeavyModel: *heavyModel,
		LightModel: *lightModel,
		OllamaUrl:  *ollamaUrl,
		SearxNGUrl: *searxUrl,
	}
	if *shouldListMemories {
		listMemories(db)
		return
	}
	logFile, err := os.OpenFile(".YAAP.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic("failed to open log file: " + err.Error())
	}
	state := NewState(settings, db, logFile)
	state.Logger.Info("Run started")
	if *memoryToDelete != "" {
		deleteMemory(state, *memoryToDelete)
		return
	}
	if *memoryToLoad != "" {
		loadMemory(state, *memoryToLoad)
	}

	if *resume {
		resumeLastMemory(state)
	}

	if *webServer {
		state.OperatingMode = Normal
		webHandler(state)
	} else {
		cliHandler(state)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

//TODO: enable memory exporting
//TODO: auto-complete for inline commands
